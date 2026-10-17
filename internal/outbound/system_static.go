package outbound

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// Static system settings share URL validation and bypass semantics across OSes.
func staticSystemProxy(req *http.Request, proxy string, exceptions []string) (*url.URL, error) {
	host := strings.ToLower(req.URL.Hostname())
	for _, pattern := range exceptions {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "<local>" && !strings.ContainsAny(host, ".:") {
			return nil, nil
		}
		if matched, _ := path.Match(pattern, host); matched {
			return nil, nil
		}
	}
	return (&httpproxy.Config{HTTPProxy: proxy, HTTPSProxy: proxy, NoProxy: strings.Join(exceptions, ",")}).ProxyFunc()(req.URL)
}

func windowsProxyForScheme(raw, scheme string) string {
	var fallback string
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ' ' }) {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			fallback = entry
			continue
		}
		if strings.EqualFold(key, scheme) {
			return value
		}
		if strings.EqualFold(key, "socks") {
			fallback = "socks5h://" + value
		}
	}
	return fallback
}
