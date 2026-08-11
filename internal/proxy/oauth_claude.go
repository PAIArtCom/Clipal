package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const defaultClaudeOAuthBaseURL = "https://api.anthropic.com"

func (cp *ClientProxy) createClaudeOAuthRequestWithPayloadForProvider(original *http.Request, provider config.Provider, providerIndex int, path string, payload *requestPayload) (*http.Request, error) {
	if original == nil {
		return nil, fmt.Errorf("original request is nil")
	}

	requestCtx, ok := requestContextFromRequest(original)
	if !ok {
		requestCtx = requestContextForClientPath(cp.clientType, path, false)
	}
	switch requestCtx.Capability {
	case CapabilityClaudeMessages, CapabilityClaudeCountTokens:
	default:
		return nil, fmt.Errorf("claude oauth does not support %s requests", requestCtx.Capability)
	}

	cred, err := cp.oauth.RefreshIfNeededWithHTTPClient(original.Context(), provider.NormalizedOAuthProvider(), provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if err != nil {
		return nil, fmt.Errorf("load oauth credential: %w", err)
	}
	accessToken := ""
	if cred != nil {
		accessToken = strings.TrimSpace(cred.AccessToken)
	}
	if accessToken == "" {
		return nil, fmt.Errorf("oauth credential %q has no access token", provider.NormalizedOAuthRef())
	}

	body := payload.providerBody(original, requestCtx, provider)
	targetURL, err := buildTargetURL(defaultClaudeOAuthBaseURL, path, original.URL.RawQuery)
	if err != nil {
		return nil, err
	}

	proxyReq, err := http.NewRequestWithContext(original.Context(), original.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyHeaderAllowingApplicationAuth(proxyReq.Header, original.Header)
	addForwardedHeaders(proxyReq, original)
	clearAuthCarriers(proxyReq)
	proxyReq.Header.Del("Cookie")
	proxyReq.Header.Del("Cookie2")
	proxyReq.Header.Del("Proxy-Authorization")
	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
	body = normalizeClaudeOAuthRequest(body, proxyReq, original, requestCtx)
	proxyReq.Body = io.NopCloser(bytes.NewReader(body))
	if strings.TrimSpace(proxyReq.Header.Get("Content-Type")) == "" && len(body) > 0 {
		proxyReq.Header.Set("Content-Type", "application/json")
	}
	proxyReq.ContentLength = int64(len(body))
	proxyReq.Header.Del("Content-Length")
	return proxyReq, nil
}

func copyHeaderAllowingApplicationAuth(dst http.Header, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
