package outbound

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func gnomeSystemProxy(req *http.Request, read func(string, string) (string, error)) (*url.URL, error) {
	shared, err := read("org.gnome.system.proxy", "use-same-proxy")
	if err != nil {
		return nil, err
	}
	// GNOME defines fixed http/https/socks schemas. Never derive a command
	// argument from the request's raw URL scheme.
	schemes := []string{"http"}
	if shared != "true" && req.URL.Scheme == "https" {
		schemes = []string{"https", "http"}
	}
	schemes = append(schemes, "socks")
	for _, scheme := range schemes {
		host, err := read("org.gnome.system.proxy."+scheme, "host")
		if err != nil {
			return nil, err
		}
		host, err = gsettingsString(host)
		if err != nil {
			return nil, err
		}
		if host == "" {
			continue
		}
		port, err := read("org.gnome.system.proxy."+scheme, "port")
		if err != nil {
			return nil, err
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 0 || number > 65535 {
			return nil, fmt.Errorf("invalid desktop proxy port")
		}
		if number == 0 {
			continue
		}
		ignores, err := read("org.gnome.system.proxy", "ignore-hosts")
		if err != nil {
			return nil, err
		}
		var exceptions []string
		for _, quoted := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(ignores, -1) {
			exceptions = append(exceptions, quoted[1])
		}
		protocol := "http"
		if scheme == "socks" {
			protocol = "socks5h"
		}
		proxy := &url.URL{Scheme: protocol, Host: net.JoinHostPort(host, strconv.Itoa(number))}
		if scheme == "http" {
			auth, err := read("org.gnome.system.proxy.http", "use-authentication")
			if err != nil {
				return nil, err
			}
			if auth == "true" {
				user, err := read("org.gnome.system.proxy.http", "authentication-user")
				if err != nil {
					return nil, err
				}
				user, err = gsettingsString(user)
				if err != nil {
					return nil, err
				}
				password, err := read("org.gnome.system.proxy.http", "authentication-password")
				if err != nil {
					return nil, err
				}
				password, err = gsettingsString(password)
				if err != nil {
					return nil, err
				}
				proxy.User = url.UserPassword(user, password)
			}
		}
		return staticSystemProxy(req, proxy.String(), exceptions)
	}
	return nil, nil
}

func gsettingsString(value string) (string, error) {
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(value[1 : len(value)-1]), nil
	}
	if strings.HasPrefix(value, `"`) {
		return strconv.Unquote(value)
	}
	return "", fmt.Errorf("invalid desktop proxy string")
}
