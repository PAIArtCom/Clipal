package oauth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type ProviderClient interface {
	Provider() config.OAuthProvider
	StartLogin(now time.Time, ttl time.Duration) (*LoginSession, error)
	ExchangeSessionCode(ctx context.Context, session *LoginSession, code string) (*Credential, error)
	Refresh(ctx context.Context, cred *Credential) (*Credential, error)
}

type httpClientProviderClient interface {
	WithHTTPClient(*http.Client) ProviderClient
}

func providerClientWithHTTPClient(client ProviderClient, httpClient *http.Client) ProviderClient {
	if client == nil || httpClient == nil {
		return client
	}
	if cloneable, ok := client.(httpClientProviderClient); ok {
		return cloneable.WithHTTPClient(httpClient)
	}
	return client
}

func startLoginSession(
	provider config.OAuthProvider,
	now time.Time,
	ttl time.Duration,
	callbackHost string,
	callbackPort int,
	callbackPath string,
	buildAuthURL func(sessionID string, redirectURI string, pkce PKCECodes) (string, error),
) (*LoginSession, error) {
	pkce, err := GeneratePKCECodes()
	if err != nil {
		return nil, err
	}
	callback, redirectURI, err := startCallbackServer(callbackHost, callbackPort, callbackPath)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			return nil, fmt.Errorf("%s callback port %d is already in use; close the existing authorization flow or the process using that port, then retry", provider, callbackPort)
		}
		return nil, err
	}

	sessionID, err := randomID()
	if err != nil {
		_ = callback.Close()
		return nil, err
	}
	authURL, err := buildAuthURL(sessionID, redirectURI, pkce)
	if err != nil {
		_ = callback.Close()
		return nil, err
	}

	return &LoginSession{
		ID:          sessionID,
		Provider:    normalizeProvider(provider),
		AuthURL:     authURL,
		Status:      LoginStatusPending,
		ExpiresAt:   now.Add(ttl),
		pkce:        pkce,
		redirectURI: redirectURI,
		callback:    callback,
	}, nil
}
