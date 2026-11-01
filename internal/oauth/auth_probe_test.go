package oauth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type authProbeCodexClient struct {
	probeErr error
}

func (c *authProbeCodexClient) Provider() config.OAuthProvider { return config.OAuthProviderCodex }
func (c *authProbeCodexClient) StartLogin(time.Time, time.Duration) (*LoginSession, error) {
	return nil, nil
}
func (c *authProbeCodexClient) ExchangeSessionCode(context.Context, *LoginSession, string) (*Credential, error) {
	return nil, nil
}
func (c *authProbeCodexClient) Refresh(context.Context, *Credential) (*Credential, error) {
	return nil, nil
}
func (c *authProbeCodexClient) WithHTTPClient(*http.Client) ProviderClient { return c }
func (c *authProbeCodexClient) FetchUsage(context.Context, *Credential) (*CodexUsageDetails, error) {
	return &CodexUsageDetails{PlanType: "test"}, c.probeErr
}

func TestProbeOAuthAuthUsesAccountProviderClientWithoutGeneration(t *testing.T) {
	svc := NewService(t.TempDir(), WithProviderClient(&authProbeCodexClient{}))
	cred := &Credential{Provider: config.OAuthProviderCodex, Ref: "account", AccountID: "acct", AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour), Environment: &RequestEnvironment{Country: "US", Timezone: "America/New_York", Source: "user"}}
	if err := svc.Store().Save(cred); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ProbeOAuthAuth(context.Background(), config.OAuthProviderCodex, cred.Ref, nil)
	if err != nil || result.State != EnvironmentEvidenceVerified {
		t.Fatalf("probe = %+v err=%v", result, err)
	}
}

func TestProbeOAuthAuthNormalizesProvider(t *testing.T) {
	svc := NewService(t.TempDir(), WithProviderClient(&authProbeCodexClient{}))
	cred := &Credential{Provider: config.OAuthProviderCodex, Ref: "account", AccountID: "acct", AccessToken: "token", ExpiresAt: time.Now().Add(time.Hour), Environment: &RequestEnvironment{Country: "US", Timezone: "America/New_York", Source: "user"}}
	if err := svc.Store().Save(cred); err != nil {
		t.Fatal(err)
	}
	result, err := svc.ProbeOAuthAuth(context.Background(), config.OAuthProvider("  CODEX  "), cred.Ref, nil)
	if err != nil || result.State != EnvironmentEvidenceVerified {
		t.Fatalf("probe = %+v err=%v", result, err)
	}
}

func TestClassifyAuthProbeErrorSeparatesAuthQuotaAndUnknown(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		state  EnvironmentEvidenceState
		status int
	}{
		{name: "unauthorized", err: errors.New("request failed with status 401"), state: EnvironmentEvidenceBlocked, status: http.StatusUnauthorized},
		{name: "quota", err: errors.New("request failed with status 429: usage limit reached"), state: EnvironmentEvidenceObserved, status: http.StatusTooManyRequests},
		{name: "network", err: errors.New("dial tcp timeout"), state: EnvironmentEvidenceUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyAuthProbeError(tc.err)
			if got.State != tc.state || got.Status != tc.status {
				t.Fatalf("result = %+v, want state=%q status=%d", got, tc.state, tc.status)
			}
		})
	}
}
