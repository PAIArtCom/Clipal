package outbound

import "net/http"

type RoutingInfo struct {
	ConnectMode string `json:"connect_mode"`
	ProxySource string `json:"proxy_source"`
}

func RouteInfo(client *http.Client, target string) RoutingInfo {
	info := RoutingInfo{ConnectMode: "system_dns", ProxySource: "direct"}
	p, err := ProxyFor(client, target)
	if err != nil {
		info.ProxySource = "unavailable"
		return info
	}
	if p != nil {
		info.ConnectMode = "proxy_hostname"
		info.ProxySource = "policy"
		return info
	}
	if client != nil {
		if t, ok := client.Transport.(*transport); ok && t.dns != "" && t.dns != "system" {
			info.ConnectMode = "resolved_ip"
			if t.dns == "auto" {
				info.ConnectMode = "auto"
			}
		}
	}
	return info
}
