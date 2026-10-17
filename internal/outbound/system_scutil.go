package outbound

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// scutil is a read-only fallback for macOS builds without native frameworks.
func scutilSystemProxy(req *http.Request, data string) (*url.URL, error) {
	fields := map[string]string{}
	var exceptions []string
	depth := 0
	inExceptions := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "}" {
			depth--
			if depth == 1 {
				inExceptions = false
			}
			continue
		}
		if strings.HasSuffix(line, "{") {
			if depth == 1 && strings.HasPrefix(line, "ExceptionsList ") {
				inExceptions = true
			}
			depth++
			continue
		}
		key, value, ok := strings.Cut(line, " : ")
		if !ok {
			continue
		}
		if depth == 1 {
			fields[key] = value
		} else if depth == 2 && inExceptions {
			exceptions = append(exceptions, value)
		}
	}
	if depth != 0 || !strings.Contains(data, "<dictionary>") {
		return nil, fmt.Errorf("invalid system proxy settings")
	}
	if fields["ProxyAutoConfigEnable"] == "1" || fields["ProxyAutoDiscoveryEnable"] == "1" {
		return nil, fmt.Errorf("automatic system proxy on macOS requires a CGO-enabled build")
	}
	protocol := "HTTP"
	if req.URL.Scheme == "https" {
		protocol = "HTTPS"
	}
	scheme := "http"
	if fields[protocol+"Enable"] != "1" {
		protocol = "SOCKS"
		scheme = "socks5h"
	}
	if fields["ExcludeSimpleHostnames"] == "1" {
		exceptions = append(exceptions, "<local>")
	}
	if fields[protocol+"Enable"] != "1" {
		return nil, nil
	}
	host, port := fields[protocol+"Proxy"], fields[protocol+"Port"]
	if host == "" || port == "" {
		return nil, fmt.Errorf("invalid system proxy endpoint")
	}
	return staticSystemProxy(req, scheme+"://"+net.JoinHostPort(host, port), exceptions)
}
