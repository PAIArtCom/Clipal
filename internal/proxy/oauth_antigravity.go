package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

const defaultAntigravityBaseURL = "https://daily-cloudcode-pa.googleapis.com"

func (cp *ClientProxy) createAntigravityOAuthRequestWithPayloadForProvider(original *http.Request, provider config.Provider, providerIndex int, path string, payload *requestPayload) (*http.Request, error) {
	if original == nil {
		return nil, fmt.Errorf("original request is nil")
	}

	requestCtx, ok := requestContextFromRequest(original)
	if !ok {
		requestCtx = requestContextForClientPath(cp.clientType, path, false)
	}

	cred, err := cp.oauth.RefreshIfNeededWithHTTPClient(original.Context(), provider.NormalizedOAuthProvider(), provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if err != nil {
		return nil, fmt.Errorf("load oauth credential: %w", err)
	}
	accessToken := ""
	projectID := ""
	if cred != nil {
		accessToken = strings.TrimSpace(cred.AccessToken)
		projectID = strings.TrimSpace(cred.Metadata[geminiOAuthProjectMetadata])
	}
	if accessToken == "" {
		return nil, fmt.Errorf("oauth credential %q has no access token", provider.NormalizedOAuthRef())
	}

	targetPath, rewrittenBody, err := payload.antigravityOAuthRequest(original, requestCtx, provider, path, projectID)
	if err != nil {
		return nil, err
	}

	targetURL, err := buildTargetURL(antigravityOAuthBaseURL(), targetPath, "")
	if err != nil {
		return nil, err
	}
	proxyReq, err := http.NewRequestWithContext(original.Context(), http.MethodPost, targetURL, bytes.NewReader(rewrittenBody))
	if err != nil {
		return nil, err
	}
	copyHeaderAllowingApplicationAuth(proxyReq.Header, original.Header)
	addForwardedHeaders(proxyReq, original)
	clearAuthCarriers(proxyReq)
	applyAntigravityOAuthHeaderDefaults(proxyReq, requestCtx.Capability, accessToken)
	proxyReq.ContentLength = int64(len(rewrittenBody))
	proxyReq.Header.Del("Content-Length")
	return proxyReq, nil
}

func antigravityOAuthBaseURL() string {
	for _, key := range []string{
		"CLIPAL_OAUTH_ANTIGRAVITY_PROXY_BASE_URL",
		"CLIPAL_OAUTH_ANTIGRAVITY_DAILY_CLOUD_CODE_URL",
		"CLIPAL_OAUTH_ANTIGRAVITY_CLOUD_CODE_URL",
	} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return defaultAntigravityBaseURL
}
