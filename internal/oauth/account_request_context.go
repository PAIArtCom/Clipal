package oauth

import (
	"context"
	"fmt"
	"net/http"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// AccountRequestContext is the internal, request-scoped OAuth account view.
// It keeps the refreshed credential, derived identity, and effective request
// environment together so proxy attempts cannot mix fields from two accounts.
// All fields are excluded from JSON serialization because this is not a public
// API DTO and must never become a credential-bearing response shape.
type AccountRequestContext struct {
	Credential *Credential               `json:"-"`
	Profile    AccountEnvironmentProfile `json:"-"`
	Effective  *RequestEnvironment       `json:"-"`
}

func (c *AccountRequestContext) Clone() *AccountRequestContext {
	if c == nil {
		return nil
	}
	clone := *c
	clone.Credential = c.Credential.Clone()
	clone.Profile = c.Profile.Clone()
	clone.Effective = c.Effective.Clone()
	return &clone
}

// ResolveAccountRequestContext loads the selected OAuth account, refreshes it
// when needed, and derives the effective environment used for this attempt.
// The supplied client is only the provider's base network policy; refresh and
// usage clients wrap it with the account environment after loading the
// credential.
func (s *Service) ResolveAccountRequestContext(ctx context.Context, provider config.OAuthProvider, ref string, httpClient *http.Client) (*AccountRequestContext, error) {
	if s == nil {
		return nil, fmt.Errorf("oauth service is nil")
	}
	cred, err := s.RefreshIfNeededWithHTTPClient(ctx, provider, ref, httpClient)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("oauth credential %q is unavailable", ref)
	}
	cred = s.PrepareEnvironment(cred)
	profile := cred.EnvironmentProfile()
	return &AccountRequestContext{
		Credential: cred.Clone(),
		Profile:    profile,
		Effective:  profile.EffectiveRequest().Clone(),
	}, nil
}
