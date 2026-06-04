package oauth

import (
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

type Credential struct {
	Ref          string               `json:"ref"`
	Provider     config.OAuthProvider `json:"provider"`
	Email        string               `json:"email,omitempty"`
	AccountID    string               `json:"account_id,omitempty"`
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time            `json:"expires_at,omitempty"`
	LastRefresh  time.Time            `json:"last_refresh,omitempty"`
	Metadata     map[string]string    `json:"metadata,omitempty"`
}

func (c *Credential) Clone() *Credential {
	if c == nil {
		return nil
	}
	clone := *c
	if c.Metadata != nil {
		clone.Metadata = make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			clone.Metadata[k] = v
		}
	}
	return &clone
}

func (c *Credential) NeedsRefresh(now time.Time, skew time.Duration) bool {
	if c == nil || c.ExpiresAt.IsZero() {
		return false
	}
	skewRefreshAt := c.ExpiresAt.Add(-skew)
	if !c.LastRefresh.IsZero() && c.ExpiresAt.After(c.LastRefresh) {
		lifecycleRefreshAt := c.LastRefresh.Add(c.ExpiresAt.Sub(c.LastRefresh) * 4 / 5)
		refreshAt := lifecycleRefreshAt
		if skewRefreshAt.Before(refreshAt) {
			refreshAt = skewRefreshAt
		}
		return !now.Before(refreshAt)
	}
	return !now.Before(skewRefreshAt)
}

type LoginStatus string

const (
	LoginStatusPending   LoginStatus = "pending"
	LoginStatusCompleted LoginStatus = "completed"
	LoginStatusError     LoginStatus = "error"
	LoginStatusExpired   LoginStatus = "expired"
)

type LoginSession struct {
	ID            string               `json:"id"`
	Provider      config.OAuthProvider `json:"provider"`
	AuthURL       string               `json:"auth_url,omitempty"`
	Status        LoginStatus          `json:"status"`
	CredentialRef string               `json:"credential_ref,omitempty"`
	Email         string               `json:"email,omitempty"`
	Error         string               `json:"error,omitempty"`
	ExpiresAt     time.Time            `json:"expires_at,omitempty"`

	pkce        PKCECodes
	redirectURI string
	callback    *callbackServer
	httpClient  *http.Client

	completionDone chan struct{}
	terminalAt     time.Time
}

func (s *LoginSession) Clone() *LoginSession {
	if s == nil {
		return nil
	}
	clone := *s
	clone.callback = nil
	clone.completionDone = nil
	clone.terminalAt = time.Time{}
	return &clone
}

func normalizeProvider(provider config.OAuthProvider) config.OAuthProvider {
	return config.OAuthProvider(strings.ToLower(strings.TrimSpace(string(provider))))
}
