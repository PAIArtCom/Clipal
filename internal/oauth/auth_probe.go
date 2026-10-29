package oauth

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// AuthProbeResult reports only the authentication boundary. It never includes
// provider response bodies, tokens, or account identifiers.
type AuthProbeResult struct {
	State  EnvironmentEvidenceState
	Status int
	Detail string
}

var authProbeHTTPStatus = regexp.MustCompile(`\bstatus\s+(\d{3})\b`)

// ProbeOAuthAuth performs a provider-specific, non-generation authenticated
// request. Codex and Claude use their usage/metadata endpoints; Antigravity
// uses its quota endpoint through the same account-aware transport.
func (s *Service) ProbeOAuthAuth(ctx context.Context, provider config.OAuthProvider, ref string, httpClient *http.Client) (AuthProbeResult, error) {
	account, err := s.ResolveAccountRequestContext(ctx, provider, ref, httpClient)
	if err != nil {
		return classifyAuthProbeError(err), nil
	}
	client, ok := s.providerClient(provider)
	if !ok {
		return AuthProbeResult{State: EnvironmentEvidenceUnknown, Detail: "OAuth provider client is unavailable."}, nil
	}
	configured, err := providerClientWithEnvironment(client, httpClient, account.Effective)
	if err != nil {
		return classifyAuthProbeError(err), nil
	}
	defer closeEnvironmentClient(configured)

	switch provider {
	case config.OAuthProviderCodex:
		fetcher, ok := configured.(codexUsageFetcher)
		if !ok {
			return AuthProbeResult{State: EnvironmentEvidenceUnknown, Detail: "Codex authentication probe is unavailable."}, nil
		}
		_, err = fetcher.FetchUsage(ctx, account.Credential)
	case config.OAuthProviderClaude:
		fetcher, ok := configured.(claudeUsageFetcher)
		if !ok {
			return AuthProbeResult{State: EnvironmentEvidenceUnknown, Detail: "Claude authentication probe is unavailable."}, nil
		}
		_, err = fetcher.FetchUsage(ctx, account.Credential)
	case config.OAuthProviderAntigravity:
		fetcher, ok := configured.(geminiUsageFetcher)
		if !ok {
			return AuthProbeResult{State: EnvironmentEvidenceUnknown, Detail: "Antigravity authentication probe is unavailable."}, nil
		}
		_, err = fetcher.FetchUsage(ctx, account.Credential)
	default:
		return AuthProbeResult{State: EnvironmentEvidenceUnknown, Detail: fmt.Sprintf("OAuth authentication probe is unsupported for %q.", provider)}, nil
	}
	if err != nil {
		return classifyAuthProbeError(err), nil
	}
	return AuthProbeResult{State: EnvironmentEvidenceVerified, Detail: "Authenticated provider metadata endpoint accepted the OAuth credential."}, nil
}

func classifyAuthProbeError(err error) AuthProbeResult {
	if err == nil {
		return AuthProbeResult{State: EnvironmentEvidenceVerified}
	}
	message := strings.ToLower(err.Error())
	status := 0
	if match := authProbeHTTPStatus.FindStringSubmatch(message); len(match) == 2 {
		status, _ = strconv.Atoi(match[1])
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || strings.Contains(message, "no access token") || strings.Contains(message, "no refresh token"):
		return AuthProbeResult{State: EnvironmentEvidenceBlocked, Status: status, Detail: "The upstream rejected the OAuth credential."}
	case status == http.StatusTooManyRequests || strings.Contains(message, "usage limit") || strings.Contains(message, "rate limit"):
		return AuthProbeResult{State: EnvironmentEvidenceObserved, Status: status, Detail: "The upstream recognized the OAuth credential but limited the account or request rate."}
	default:
		return AuthProbeResult{State: EnvironmentEvidenceUnknown, Status: status, Detail: "The authentication probe could not determine whether the credential was accepted."}
	}
}
