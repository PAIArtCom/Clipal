package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
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

	account, err := cp.oauth.ResolveAccountRequestContext(original.Context(), provider.NormalizedOAuthProvider(), provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if err != nil {
		return nil, fmt.Errorf("load oauth credential: %w", err)
	}
	cred := account.Credential
	cp.recordAccountAttempt(account)
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
	var originalRoot map[string]any
	if payload != nil {
		originalRoot = payload.jsonRoot()
	}
	accountClient, err := cp.accountHTTPClientForContext(account, providerIndex)
	if err != nil {
		return nil, fmt.Errorf("prepare antigravity account transport: %w", err)
	}
	rewrittenBody, err = cp.resolveAntigravityRequestModel(original, cred, providerIndex, accountClient, path, rewrittenBody, originalRoot)
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
	return applyOAuthRequestContext(proxyReq, account)
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
