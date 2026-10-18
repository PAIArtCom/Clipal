package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	oauthpkg "github.com/PAIArtCom/Clipal/internal/oauth"
)

type stubGeminiOAuthClient struct {
	provider      config.OAuthProvider
	refreshCred   *oauthpkg.Credential
	refreshErr    error
	refreshCalled int32
}

func (c *stubGeminiOAuthClient) Provider() config.OAuthProvider {
	if c.provider != "" {
		return c.provider
	}
	return config.OAuthProviderAntigravity
}
func (c *stubGeminiOAuthClient) StartLogin(_ time.Time, _ time.Duration) (*oauthpkg.LoginSession, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c *stubGeminiOAuthClient) ExchangeSessionCode(_ context.Context, _ *oauthpkg.LoginSession, _ string) (*oauthpkg.Credential, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c *stubGeminiOAuthClient) Refresh(_ context.Context, cred *oauthpkg.Credential) (*oauthpkg.Credential, error) {
	atomic.AddInt32(&c.refreshCalled, 1)
	if c.refreshErr != nil {
		return nil, c.refreshErr
	}
	if c.refreshCred != nil {
		return c.refreshCred.Clone(), nil
	}
	return cred.Clone(), nil
}
func TestCreateProxyRequest_AntigravityOAuthUsesAntigravityEndpointAndHeaders(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "antigravity-sean-example-com-project-1",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "project-1",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGoogle, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "antigravity-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderAntigravity,
			OAuthRef:      "antigravity-sean-example-com-project-1",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"responseModalities":["IMAGE","TEXT"]}}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-3.1-flash-image:generateContent", bytes.NewReader(body))
	original.Header.Set("x-goog-api-key", "client-key")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGoogle,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiGenerateContent,
		UpstreamPath:   "/v1beta/models/gemini-3.1-flash-image:generateContent",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1beta/models/gemini-3.1-flash-image:generateContent", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Method; got != http.MethodPost {
		t.Fatalf("method = %q", got)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := proxyReq.Header.Get("User-Agent"); got != antigravityOAuthUserAgent() {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := proxyReq.Header.Get("x-goog-api-key"); got != "" {
		t.Fatalf("x-goog-api-key = %q, want empty", got)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["model"]; got != "gemini-3.1-flash-image" {
		t.Fatalf("model = %v", got)
	}
	if got := root["project"]; got != "project-1" {
		t.Fatalf("project = %v", got)
	}
	request, ok := root["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %#v, want object", root["request"])
	}
	if _, ok := request["generationConfig"]; !ok {
		t.Fatalf("request.generationConfig missing: %#v", request)
	}
}

func TestCreateProxyRequest_AntigravityOAuthModelsUsesFetchAvailableModels(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "antigravity-sean-example-com-project-1",
		Provider:    config.OAuthProviderAntigravity,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		Metadata: map[string]string{
			"project_id": "project-1",
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientGoogle, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "antigravity-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderAntigravity,
			OAuthRef:      "antigravity-sean-example-com-project-1",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	original := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1beta/models", nil)
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientGoogle,
		Family:         ProtocolFamilyGemini,
		Capability:     CapabilityGeminiModels,
		UpstreamPath:   "/v1beta/models",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1beta/models", nil)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Method; got != http.MethodPost {
		t.Fatalf("method = %q", got)
	}
	root := decodeRequestBodyMap(t, proxyReq)
	if got := root["project"]; got != "project-1" {
		t.Fatalf("project = %v", got)
	}
}
