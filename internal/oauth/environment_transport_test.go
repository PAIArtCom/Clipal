package oauth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type environmentTestProviderClient struct {
	httpClient *http.Client
}

func (c *environmentTestProviderClient) Provider() config.OAuthProvider {
	return config.OAuthProviderAntigravity
}
func (c *environmentTestProviderClient) StartLogin(time.Time, time.Duration) (*LoginSession, error) {
	return nil, nil
}
func (c *environmentTestProviderClient) ExchangeSessionCode(context.Context, *LoginSession, string) (*Credential, error) {
	return nil, nil
}
func (c *environmentTestProviderClient) Refresh(context.Context, *Credential) (*Credential, error) {
	return nil, nil
}
func (c *environmentTestProviderClient) WithHTTPClient(client *http.Client) ProviderClient {
	return &environmentTestProviderClient{httpClient: client}
}

func TestProviderClientWithEnvironmentDoesNotDoubleWrapMatchingAccountTransport(t *testing.T) {
	environment := &RequestEnvironment{DNS: "cloudflare", Country: "US", Language: "en-US", Locale: "en-US", Timezone: "America/New_York"}
	base := &http.Client{Transport: &http.Transport{}}
	accountClient, err := EnvironmentHTTPClient(base, environment, config.OAuthProviderAntigravity)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := providerClientWithEnvironment(&environmentTestProviderClient{}, accountClient, environment)
	if err != nil {
		t.Fatalf("providerClientWithEnvironment: %v", err)
	}
	got, ok := configured.(*environmentTestProviderClient)
	if !ok || got.httpClient != accountClient {
		t.Fatalf("provider client transport was wrapped again: %#v", configured)
	}
}
