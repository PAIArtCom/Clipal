package outbound

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/http/httpproxy"
)

func TestNetworkProxyPrecedenceAndBypass(t *testing.T) {
	for _, tt := range []struct {
		name, target      string
		env               httpproxy.Config
		all, system, want string
		systemError       bool
	}{
		{name: "system HTTP", target: "http://upstream.example", system: "http://os-proxy:8080", want: "http://os-proxy:8080"},
		{name: "system HTTPS CONNECT", target: "https://upstream.example", system: "http://os-proxy:8080", want: "http://os-proxy:8080"},
		{name: "system SOCKS", target: "https://upstream.example", system: "socks5h://os-proxy:1080", want: "socks5h://os-proxy:1080"},
		{name: "environment overrides OS", target: "https://upstream.example", env: httpproxy.Config{HTTPSProxy: "http://env-proxy:8080"}, system: "http://os-proxy:8080", want: "http://env-proxy:8080"},
		{name: "ALL_PROXY", target: "https://upstream.example", all: "socks5://all-proxy:1080", system: "http://os-proxy:8080", want: "socks5://all-proxy:1080"},
		{name: "NO_PROXY bypasses OS", target: "https://upstream.example", env: httpproxy.Config{NoProxy: "upstream.example"}, system: "http://os-proxy:8080"},
		{name: "NO_PROXY overrides ALL", target: "https://upstream.example", env: httpproxy.Config{NoProxy: "*"}, all: "http://all-proxy:8080"},
		{name: "loopback", target: "http://127.0.0.1", system: "http://os-proxy:8080"},
		{name: "per scheme", target: "https://upstream.example", env: httpproxy.Config{HTTPProxy: "http://env-proxy:8080"}, system: "http://os-proxy:8080", want: "http://os-proxy:8080"},
		{name: "no proxy VPN or TUN transparent", target: "https://upstream.example"},
		{name: "OS failure is not direct permission", target: "https://upstream.example", systemError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, tt.target, nil)
			p, err := networkProxy(req, &tt.env, func(string) string { return tt.all }, func(*http.Request) (*url.URL, error) {
				if tt.systemError {
					return nil, errors.New("fixture settings unavailable")
				}
				if tt.system == "" {
					return nil, nil
				}
				return url.Parse(tt.system)
			})
			if (err != nil) != tt.systemError {
				t.Fatalf("error=%v", err)
			}
			got := ""
			if p != nil {
				got = p.String()
			}
			if got != tt.want {
				t.Fatalf("proxy=%s want=%s", got, tt.want)
			}
		})
	}
}

func TestStaticSystemProxyExceptions(t *testing.T) {
	for _, tt := range []struct {
		host, exception string
		bypass          bool
	}{
		{"service.internal", "*.internal", true}, {"service.internal", ".internal", true},
		{"printer", "<local>", true}, {"192.168.2.3", "192.168.0.0/16", true},
		{"api.example.com", "api.*.com", true}, {"public.example", "*.internal", false},
	} {
		req, _ := http.NewRequest(http.MethodGet, "https://"+tt.host, nil)
		p, err := staticSystemProxy(req, "http://os-proxy:8080", []string{tt.exception})
		if err != nil || (p == nil) != tt.bypass {
			t.Fatalf("%+v proxy=%v error=%v", tt, p, err)
		}
	}
}

func TestNetworkProxyObservesChangedSystemRoute(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://upstream.example", nil)
	current := "http://first:8080"
	system := func(*http.Request) (*url.URL, error) { return url.Parse(current) }
	for _, want := range []string{"http://first:8080", "http://second:8080"} {
		current = want
		p, err := networkProxy(req, &httpproxy.Config{}, func(string) string { return "" }, system)
		if err != nil || p.String() != want {
			t.Fatalf("proxy=%v error=%v", p, err)
		}
	}
}

func TestScutilSystemProxyStaticSettings(t *testing.T) {
	settings := `<dictionary> {
  ExceptionsList : <array> {
    0 : *.internal
    1 : 192.168.0.0/16
  }
  ExcludeSimpleHostnames : 1
  HTTPEnable : 1
  HTTPProxy : localhost
  HTTPPort : 7890
  HTTPSEnable : 1
  HTTPSProxy : ::1
  HTTPSPort : 7891
  SOCKSEnable : 1
  SOCKSProxy : localhost
  SOCKSPort : 1080
  __SCOPED__ : <dictionary> {
    en0 : <dictionary> {
      HTTPSProxy : ignored.invalid
    }
  }
 }`
	for _, tt := range []struct{ target, want string }{
		{"http://public.example", "http://localhost:7890"},
		{"https://public.example", "http://[::1]:7891"},
		{"https://service.internal", ""},
		{"https://192.168.1.2", ""},
		{"https://printer", ""},
	} {
		req, _ := http.NewRequest(http.MethodGet, tt.target, nil)
		p, err := scutilSystemProxy(req, settings)
		got := ""
		if p != nil {
			got = p.String()
		}
		if err != nil || got != tt.want {
			t.Fatalf("target=%s proxy=%s error=%v", tt.target, got, err)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://public.example", nil)
	p, err := scutilSystemProxy(req, "<dictionary> {\n SOCKSEnable : 1\n SOCKSProxy : localhost\n SOCKSPort : 1080\n}")
	if err != nil || p.String() != "socks5h://localhost:1080" {
		t.Fatalf("SOCKS=%v error=%v", p, err)
	}
	for _, data := range []string{"invalid", "<dictionary> {\n ProxyAutoConfigEnable : 1\n}"} {
		if p, err := scutilSystemProxy(req, data); err == nil {
			t.Fatalf("unsupported settings silently bypassed: %v", p)
		}
	}
}

func TestGnomeManualProxySelection(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings map[string]string
		want     string
		scheme   string
	}{
		{"HTTPS specific", map[string]string{"https.host": "'https-proxy'", "https.port": "8081"}, "http://https-proxy:8081", "https"},
		{"HTTPS falls back to HTTP", map[string]string{"http.host": "'http-proxy'", "http.port": "8080"}, "http://http-proxy:8080", "https"},
		{"shared HTTP overrides HTTPS", map[string]string{"use-same-proxy": "true", "http.host": "'shared'", "http.port": "8080", "https.host": "'unused'", "https.port": "8081"}, "http://shared:8080", "https"},
		{"port zero disables proxy", map[string]string{"https.host": "'disabled'", "https.port": "0", "socks.host": "'socks-proxy'", "socks.port": "1080"}, "socks5h://socks-proxy:1080", "https"},
		{"authentication", map[string]string{"http.host": "'proxy'", "http.port": "8080", "http.use-authentication": "true", "http.authentication-user": "'owner'", "http.authentication-password": "'secret'"}, "http://owner:secret@proxy:8080", "https"},
		{"exception", map[string]string{"http.host": "'proxy'", "http.port": "8080", "ignore-hosts": "['*.example']"}, "", "https"},
		{"no proxy", nil, "", "https"},
		{"HTTP ignores HTTPS proxy", map[string]string{"https.host": "'unused'", "https.port": "8081", "http.host": "'http-proxy'", "http.port": "8080"}, "http://http-proxy:8080", "http"},
		{"HTTP falls back to SOCKS", map[string]string{"socks.host": "'socks-proxy'", "socks.port": "1080"}, "socks5h://socks-proxy:1080", "http"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, tt.scheme+"://public.example", nil)
			read := func(schema, key string) (string, error) {
				prefix := strings.TrimPrefix(schema, "org.gnome.system.proxy")
				prefix = strings.TrimPrefix(prefix, ".")
				if prefix != "" {
					key = prefix + "." + key
				}
				if value, ok := tt.settings[key]; ok {
					return value, nil
				}
				switch key {
				case "use-same-proxy", "http.use-authentication":
					return "false", nil
				case "ignore-hosts":
					return "[]", nil
				}
				return "''", nil
			}
			p, err := gnomeSystemProxy(req, read)
			got := ""
			if p != nil {
				got = p.String()
			}
			if err != nil || got != tt.want {
				t.Fatalf("proxy=%s want=%s error=%v", got, tt.want, err)
			}
		})
	}
}

func TestWindowsSystemProxySchemeSelection(t *testing.T) {
	for _, tt := range []struct{ raw, scheme, want string }{
		{"http=first:80;https=second:81", "https", "second:81"},
		{"shared:8080", "https", "shared:8080"},
		{"http=first:80;socks=socks-proxy:1080", "https", "socks5h://socks-proxy:1080"},
		{"http=first:80", "https", ""},
	} {
		if got := windowsProxyForScheme(tt.raw, tt.scheme); got != tt.want {
			t.Fatalf("proxy=%s want=%s", got, tt.want)
		}
	}
}
