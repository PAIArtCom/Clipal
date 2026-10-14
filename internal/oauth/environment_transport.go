package oauth

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/outbound"
)

type environmentTransport struct {
	base        http.RoundTripper
	environment *RequestEnvironment
}

func (t *environmentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	ApplyEnvironmentHeaders(copy, t.environment)
	return t.base.RoundTrip(copy)
}
func (t *environmentTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// EnvironmentHTTPClient preserves client timeouts and transport behavior. No
// credentials are sent to DNS services; the DNS layer receives only hostnames.
func EnvironmentHTTPClient(client *http.Client, e *RequestEnvironment, provider ...config.OAuthProvider) (*http.Client, error) {
	if e == nil {
		return client, nil
	}
	if client == nil {
		client = &http.Client{}
	}
	copyBase := *client
	var alternative http.RoundTripper
	if base, ok := client.Transport.(*http.Transport); ok {
		copyBase.Transport = base.Clone()
	} else if base, ok := client.Transport.(*anthropicFallbackRoundTripper); ok {
		if fallback, ok := base.fallback.(*http.Transport); ok {
			// Capture routing before WithDialContext clears Proxy: the account
			// dialer must inherit the native client's standard proxy policy.
			copyBase.Transport = fallback.Clone()
			alternative = base
		} else {
			copyBase.Transport = base.WithDialContext((&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
		}
	}
	client = &copyBase
	if alternative == nil && len(provider) > 0 && provider[0] == config.OAuthProviderClaude {
		if _, ok := client.Transport.(*http.Transport); ok {
			native, ok := newAnthropicHTTPClient(0).Transport.(*anthropicFallbackRoundTripper)
			if !ok {
				return nil, fmt.Errorf("anthropic transport is unavailable")
			}
			native.fallback = client.Transport
			alternative = native
		}
	}
	dns := e.DNS
	// An injected opaque transport owns its dialing. Automatic policy must not
	// replace its behavior; explicit DoH still requires a dialable transport.
	if dns == "auto" && !outbound.SupportsDNS(client) {
		dns = "system"
	}
	withDNS, err := outbound.Configure(client, dns, alternative)
	if err != nil {
		return nil, err
	}
	copy := *withDNS
	base := copy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = &environmentTransport{base: base, environment: e.Clone()}
	return &copy, nil
}

func providerClientWithEnvironment(client ProviderClient, httpClient *http.Client, e *RequestEnvironment) (ProviderClient, error) {
	if httpClient == nil {
		if native, ok := client.(interface{ httpClient() *http.Client }); ok {
			httpClient = native.httpClient()
		}
	}
	if httpClient == nil {
		if e == nil || e.DNS == "system" || e.DNS == "auto" {
			return client, nil
		}
		return nil, fmt.Errorf("OAuth provider has no HTTP client")
	}
	configured, err := EnvironmentHTTPClient(httpClient, e, client.Provider())
	if err != nil {
		return nil, err
	}
	return providerClientWithHTTPClient(client, configured), nil
}

func closeEnvironmentClient(client ProviderClient) {
	if native, ok := client.(interface{ httpClient() *http.Client }); ok {
		if _, owned := native.httpClient().Transport.(*environmentTransport); owned {
			native.httpClient().CloseIdleConnections()
		}
	}
}
