//go:build darwin && cgo

package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDarwinNativeSystemProxySnapshots(t *testing.T) {
	for _, tt := range []struct{ name, settings, target, want string }{
		{"HTTPS uses CONNECT proxy", "<key>HTTPSEnable</key><integer>1</integer><key>HTTPSProxy</key><string>127.0.0.1</string><key>HTTPSPort</key><integer>7890</integer>", "https://public.example", "http://127.0.0.1:7890"},
		{"SOCKS retains hostname", "<key>SOCKSEnable</key><integer>1</integer><key>SOCKSProxy</key><string>127.0.0.1</string><key>SOCKSPort</key><integer>1080</integer>", "https://public.example", "socks5h://127.0.0.1:1080"},
		{"OS exception", "<key>HTTPSEnable</key><integer>1</integer><key>HTTPSProxy</key><string>127.0.0.1</string><key>HTTPSPort</key><integer>7890</integer><key>ExceptionsList</key><array><string>*.internal</string></array>", "https://service.internal", ""},
		{"no configured proxy VPN TUN transparent", "", "https://public.example", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, tt.target, nil)
			snapshot := `<?xml version="1.0"?><plist version="1.0"><dict>` + tt.settings + `</dict></plist>`
			p, err := darwinSystemProxy(req, snapshot)
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

func TestDarwinSystemPACUsesPerTargetDecision(t *testing.T) {
	pac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = io.WriteString(w, `function FindProxyForURL(url,host){return host=="proxy.example"?"PROXY 127.0.0.1:18080":"DIRECT";}`)
	}))
	defer pac.Close()
	snapshot := `<?xml version="1.0"?><plist version="1.0"><dict><key>ProxyAutoConfigEnable</key><integer>1</integer><key>ProxyAutoConfigURLString</key><string>` + pac.URL + `</string></dict></plist>`
	for _, host := range []string{"proxy.example", "direct.example"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host, nil)
		p, err := darwinSystemProxy(req, snapshot)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if host == "proxy.example" && (p == nil || p.String() != "http://127.0.0.1:18080") {
			t.Fatalf("PAC proxy=%v", p)
		}
		if host == "direct.example" && p != nil {
			t.Fatalf("PAC DIRECT=%v", p)
		}
	}
}

func TestDarwinInvalidPACDoesNotBypassSystemProxy(t *testing.T) {
	pac := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer pac.Close()
	req, _ := http.NewRequest(http.MethodGet, "https://public.example", nil)
	snapshot := `<?xml version="1.0"?><plist version="1.0"><dict><key>ProxyAutoConfigEnable</key><integer>1</integer><key>ProxyAutoConfigURLString</key><string>` + pac.URL + `</string></dict></plist>`
	if p, err := darwinSystemProxy(req, snapshot); err == nil {
		t.Fatalf("PAC failure permitted bypass: %v", p)
	}
}
