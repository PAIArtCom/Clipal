package outbound

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
)

type ExitObservation struct {
	IP       string
	Country  string
	Timezone string
	Source   string
}

// SupportsDNS distinguishes owned dialable transports from injected clients
// which implement their own network behavior.
func SupportsDNS(client *http.Client) bool {
	if client == nil || client.Transport == nil {
		return true
	}
	if _, ok := client.Transport.(*http.Transport); ok {
		return true
	}
	_, ok := client.Transport.(dialTransport)
	return ok
}

func ProxyFor(client *http.Client, target string) (*url.URL, error) {
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var policy func(*http.Request) (*url.URL, error)
	switch t := base.(type) {
	case *http.Transport:
		policy = t.Proxy
	case *transport:
		policy = t.proxy
	}
	if policy == nil {
		return nil, nil
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return policy(req)
}

// ProbeExit observes a reference service, never the model target exit. Matching
// proxy entries do not prove matching egress. Different policies are skipped,
// including NO_PROXY; no credentials are sent.
// Optional sources allow the shared fixture to keep tests entirely local.
func ProbeExit(ctx context.Context, client *http.Client, target string, sources ...string) (*ExitObservation, error) {
	if client == nil {
		client = &http.Client{}
	}
	probe := *client
	probe.Jar = nil
	probe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &probe
	if len(sources) == 0 {
		sources = []string{"https://ipwho.is/", "https://ipapi.co/json/"}
	}
	selected, err := ProxyFor(client, target)
	if err != nil {
		return nil, fmt.Errorf("target proxy policy failed")
	}
	for _, address := range sources {
		route, err := ProxyFor(client, address)
		if err != nil || !sameRoute(selected, route) {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req) // #nosec G704 -- fixed service URLs, or explicit local test fixtures; no account credentials.
		if err != nil {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 32769))
		_ = resp.Body.Close()
		if readErr != nil || len(data) > 32768 || resp.StatusCode != http.StatusOK {
			continue
		}
		var exit struct {
			IP       string          `json:"ip"`
			Country  string          `json:"country_code"`
			Timezone json.RawMessage `json:"timezone"`
			Success  *bool           `json:"success"`
			Error    bool            `json:"error"`
		}
		if json.Unmarshal(data, &exit) != nil || net.ParseIP(exit.IP) == nil || exit.Error || (exit.Success != nil && !*exit.Success) {
			continue
		}
		result := &ExitObservation{IP: exit.IP, Country: exit.Country, Source: address}
		if json.Unmarshal(exit.Timezone, &result.Timezone) != nil {
			var zone struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(exit.Timezone, &zone)
			result.Timezone = zone.ID
		}
		return result, nil
	}
	return nil, fmt.Errorf("no exit observation on the target route")
}

func sameRoute(a, b *url.URL) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.String() == b.String()
}
