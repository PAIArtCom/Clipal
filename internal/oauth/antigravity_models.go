package oauth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// FetchModels is shared by quota discovery and Gemini-to-OAuth model resolution.
func (c *AntigravityClient) FetchModels(ctx context.Context, cred *Credential) (map[string]any, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	data, err := c.callCloudCode(ctx, cred.AccessToken, c.dailyCloudCodeURL(), "fetchAvailableModels", map[string]any{"project": googleCredentialProjectID(cred)})
	if err != nil {
		return nil, err
	}
	models, ok := data["models"].(map[string]any)
	if data["models"] != nil && !ok {
		return nil, fmt.Errorf("invalid antigravity model catalog")
	}
	return models, nil
}

// AntigravityModelsWithHTTPClient uses the already-refreshed account and the
// same proxy, DNS, and account environment as the generation request.
func (s *Service) AntigravityModelsWithHTTPClient(ctx context.Context, cred *Credential, httpClient *http.Client) (map[string]any, error) {
	if s == nil || cred == nil || cred.Provider != config.OAuthProviderAntigravity {
		return nil, fmt.Errorf("antigravity credential is required")
	}
	client, ok := s.providerClient(cred.Provider)
	if !ok {
		return nil, fmt.Errorf("antigravity client is unavailable")
	}
	cred = s.PrepareEnvironment(cred)
	configured, err := providerClientWithEnvironment(client, httpClient, cred.Environment)
	if err != nil {
		return nil, err
	}
	defer closeEnvironmentClient(configured)
	fetcher, ok := configured.(interface {
		FetchModels(context.Context, *Credential) (map[string]any, error)
	})
	if !ok {
		return nil, fmt.Errorf("antigravity client does not support model discovery")
	}
	return fetcher.FetchModels(ctx, cred)
}
