package outbound

import (
	"net/http"
	"net/url"
	"os"

	"golang.org/x/net/http/httpproxy"
)

// NetworkProxy inherits the process environment and OS proxy settings without
// changing either. Explicit direct/custom policies do not call this function.
func NetworkProxy(req *http.Request) (*url.URL, error) {
	return networkProxy(req, httpproxy.FromEnvironment(), os.Getenv, systemProxy)
}

func networkProxy(req *http.Request, env *httpproxy.Config, getenv func(string) string, system func(*http.Request) (*url.URL, error)) (*url.URL, error) {
	// Apply NO_PROXY even when no environment proxy is set, so a bypass cannot
	// accidentally fall through to an OS proxy. This also keeps loopback local.
	bypass := (&httpproxy.Config{HTTPProxy: "http://bypass.invalid", HTTPSProxy: "http://bypass.invalid", NoProxy: env.NoProxy}).ProxyFunc()
	if p, err := bypass(req.URL); err != nil || p == nil {
		return nil, err
	}
	copy := *env
	all := getenv("ALL_PROXY")
	if all == "" {
		all = getenv("all_proxy")
	}
	if copy.HTTPProxy == "" {
		copy.HTTPProxy = all
	}
	if copy.HTTPSProxy == "" {
		copy.HTTPSProxy = all
	}
	configured := (req.URL.Scheme == "http" && copy.HTTPProxy != "") || (req.URL.Scheme == "https" && copy.HTTPSProxy != "")
	if configured {
		return copy.ProxyFunc()(req.URL)
	}
	return system(req)
}
