package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

func TestCreateProxyRequest_ClaudeOAuthUsesBearerAuth(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("claude-sonnet-4-5"),
				Claude: &config.ClaudeOverrides{
					ThinkingBudgetTokens: claudeTestIntPtr(2048),
				},
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"claude-3-7-sonnet","messages":[]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages?beta=true", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("x-api-key", "client-key")
	original.Header.Set("anthropic-version", "2023-06-01")
	original.Header.Set("anthropic-beta", "custom-beta-2026-06-30")
	original.Header.Set("X-App-Name", "stale-client")
	original.Header.Set("X-App-Ver", "0.1.0")
	original.Header.Set("X-Client-App", "stale-client")
	original.Header.Set("Cookie", "secret=1")
	original.Header.Set("Proxy-Authorization", "Basic dGVzdA==")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.URL.String(); got != "https://api.anthropic.com/v1/messages?beta=true" {
		t.Fatalf("url = %q", got)
	}
	if got := proxyReq.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := proxyReq.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Fatalf("anthropic-version = %q", got)
	}
	if got := proxyReq.Header.Get("User-Agent"); got != claudeOAuthUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, claudeOAuthUserAgent)
	}
	if got := proxyReq.Header.Get("X-App"); got != "cli" {
		t.Fatalf("X-App = %q, want cli", got)
	}
	if got := proxyReq.Header.Get("X-App-Name"); got != "" {
		t.Fatalf("X-App-Name = %q, want empty for synthetic non-CLI request", got)
	}
	if got := proxyReq.Header.Get("X-App-Ver"); got != "" {
		t.Fatalf("X-App-Ver = %q, want empty for synthetic non-CLI request", got)
	}
	if got := proxyReq.Header.Get("X-Client-App"); got != "" {
		t.Fatalf("X-Client-App = %q, want empty for synthetic non-CLI request", got)
	}
	if got := proxyReq.Header.Get("X-Claude-Code-Session-Id"); strings.TrimSpace(got) == "" {
		t.Fatalf("X-Claude-Code-Session-Id = %q, want non-empty", got)
	}
	if got := proxyReq.Header.Get("Anthropic-Dangerous-Direct-Browser-Access"); got != "true" {
		t.Fatalf("Anthropic-Dangerous-Direct-Browser-Access = %q, want true", got)
	}
	if got := proxyReq.Header.Get("X-Stainless-Runtime"); got != claudeOAuthStainlessRuntime {
		t.Fatalf("X-Stainless-Runtime = %q, want %q", got, claudeOAuthStainlessRuntime)
	}
	if got := proxyReq.Header.Get("X-Stainless-Package-Version"); got != claudeOAuthStainlessPackageVersion {
		t.Fatalf("X-Stainless-Package-Version = %q, want %q", got, claudeOAuthStainlessPackageVersion)
	}
	betas := proxyReq.Header.Get("Anthropic-Beta")
	for _, token := range []string{"oauth-2025-04-20", "claude-code-20250219", "interleaved-thinking-2025-05-14", "prompt-caching-scope-2026-01-05"} {
		if !strings.Contains(strings.ToLower(betas), strings.ToLower(token)) {
			t.Fatalf("Anthropic-Beta = %q, want token %q", betas, token)
		}
	}
	for _, token := range []string{"thinking-token-count-2026-05-13"} {
		if !strings.Contains(strings.ToLower(betas), strings.ToLower(token)) {
			t.Fatalf("Anthropic-Beta = %q, want token %q", betas, token)
		}
	}
	for _, token := range []string{"custom-beta-2026-06-30", "redact-thinking-2026-02-12", "extended-cache-ttl-2025-04-11", "advisor-tool-2026-03-01", "advanced-tool-use-2025-11-20", "effort-2025-11-24"} {
		if !strings.Contains(strings.ToLower(betas), strings.ToLower(token)) {
			t.Fatalf("Anthropic-Beta = %q, want token %q", betas, token)
		}
	}
	if got := proxyReq.Header.Get("Cookie"); got != "" {
		t.Fatalf("Cookie = %q, want empty", got)
	}
	if got := proxyReq.Header.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("Proxy-Authorization = %q, want empty", got)
	}

	var root map[string]any
	if err := json.NewDecoder(proxyReq.Body).Decode(&root); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	if got := root["model"]; got != "claude-sonnet-4-5" {
		t.Fatalf("model = %v", got)
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatalf("system = %#v, want injected billing attribution block", root["system"])
	}
	systemBlock, ok := system[0].(map[string]any)
	if !ok {
		t.Fatalf("system[0] = %#v", system[0])
	}
	systemText, ok := systemBlock["text"].(string)
	if !ok {
		t.Fatalf("system[0].text = %#v", systemBlock["text"])
	}
	if !strings.Contains(systemText, "x-anthropic-billing-header:") {
		t.Fatalf("system[0].text = %q, want billing header", systemText)
	}
	if !strings.Contains(systemText, "cc_version="+claudeOAuthBillingVersion(root["messages"])) {
		t.Fatalf("system[0].text = %q, want current app version", systemText)
	}
	cch := claudeOAuthTestCCH(systemText)
	if cch == "" || cch == "00000" {
		t.Fatalf("system billing cch = %q, want signed non-zero cch", cch)
	}
	thinking, ok := root["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %#v", root["thinking"])
	}
	if got := thinking["budget_tokens"]; got != float64(2048) {
		t.Fatalf("thinking.budget_tokens = %v", got)
	}
}

func TestCreateProxyRequest_ClaudeOAuthRegeneratesOfficialLookingClaudeCodeHeaders(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"claude-sonnet-4-5","messages":[]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("User-Agent", "claude-code/2.2.0 (external, cli)")
	original.Header.Set("X-App", "cli")
	original.Header.Set("X-App-Name", "claude-code")
	original.Header.Set("X-App-Ver", "2.2.0")
	original.Header.Set("X-Client-App", "claude-code")
	original.Header.Set("X-Claude-Code-Session-Id", "session-from-claude-code")
	original.Header.Set("Anthropic-Beta", "attacker-beta")
	original.Header.Set("X-Stainless-Runtime", "attacker-runtime")
	original.Header.Set("X-Stainless-Package-Version", "attacker-package")
	original.Header.Set("X-Stainless-Helper-Method", "attacker-helper")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	for key, want := range map[string]string{
		"User-Agent": claudeOAuthUserAgent,
		"X-App":      "cli",
	} {
		if got := proxyReq.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if got := proxyReq.Header.Get("X-Claude-Code-Session-Id"); got != "session-from-claude-code" {
		t.Fatalf("X-Claude-Code-Session-Id = %q, want inherited client session", got)
	}
	for _, key := range []string{"X-App-Name", "X-App-Ver", "X-Client-App"} {
		if got := proxyReq.Header.Get(key); got != "" {
			t.Fatalf("%s = %q, want empty for Claude %s", key, got, claudeOAuthAppVersion)
		}
	}
	if got := proxyReq.Header.Get("Anthropic-Beta"); strings.Contains(got, "attacker-beta") {
		t.Fatalf("Anthropic-Beta preserved attacker beta: %q", got)
	}
	if got := proxyReq.Header.Get("X-Stainless-Runtime"); got != claudeOAuthStainlessRuntime {
		t.Fatalf("X-Stainless-Runtime = %q, want %q", got, claudeOAuthStainlessRuntime)
	}
	if got := proxyReq.Header.Get("X-Stainless-Package-Version"); got != claudeOAuthStainlessPackageVersion {
		t.Fatalf("X-Stainless-Package-Version = %q, want %q", got, claudeOAuthStainlessPackageVersion)
	}
	if got := proxyReq.Header.Get("X-Stainless-Helper-Method"); got != "" {
		t.Fatalf("X-Stainless-Helper-Method = %q, want empty for non-streaming request", got)
	}
	assertClaudeOAuthSDKEnvelopeBody(t, proxyReq)
}

func TestCreateProxyRequest_ClaudeOAuthInheritsClientSessionContext(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"conversation_id":"conversation-secret@example.com",
		"metadata":{
			"session_id":"metadata-session-secret",
			"user_id":"{\"device_id\":\"device-123\",\"account_uuid\":\"acct-123\",\"session_id\":\"user-id-session-secret\"}"
		},
		"messages":[{"role":"user","content":"hello"}]
	}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("X-Claude-Code-Session-Id", "header-session-secret")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	sessionID := proxyReq.Header.Get("X-Claude-Code-Session-Id")
	if sessionID != "header-session-secret" {
		t.Fatalf("X-Claude-Code-Session-Id = %q, want inherited header session", sessionID)
	}

	root := decodeRequestBodyMap(t, proxyReq)
	metadata, _ := root["metadata"].(map[string]any)
	userID := stringValue(metadata["user_id"])
	if !strings.Contains(userID, sessionID) {
		t.Fatalf("metadata.user_id = %q, want inherited session %q", userID, sessionID)
	}
	if _, ok := metadata["session_id"]; ok {
		t.Fatalf("metadata.session_id should be removed: %#v", metadata)
	}
}

func TestCreateProxyRequest_ClaudeOAuthPreservesExplicitSDKControls(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"name":"CustomTool","description":"custom","input_schema":{"type":"object"}}],
		"thinking":{"type":"enabled","budget_tokens":1024},
		"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"none"}]},
		"output_config":{"effort":"low"}
	}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("User-Agent"); got != claudeOAuthUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, claudeOAuthUserAgent)
	}
	root := decodeRequestBodyMap(t, proxyReq)
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want client tool", root["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "CustomTool" {
		t.Fatalf("tools[0] = %#v, want CustomTool", tools[0])
	}
	thinking, _ := root["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(1024) {
		t.Fatalf("thinking = %#v, want explicit thinking", root["thinking"])
	}
	contextManagement, _ := root["context_management"].(map[string]any)
	edits, _ := contextManagement["edits"].([]any)
	edit, _ := edits[0].(map[string]any)
	if edit["keep"] != "none" {
		t.Fatalf("context_management = %#v, want explicit keep none", root["context_management"])
	}
	outputConfig, _ := root["output_config"].(map[string]any)
	if outputConfig["effort"] != "low" {
		t.Fatalf("output_config = %#v, want explicit effort low", root["output_config"])
	}
}

func TestCreateProxyRequest_ClaudeOAuthSDKHeadersAndStreamingHelper(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"claude-sonnet-4-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}
	if got := proxyReq.Header.Get("Accept"); got != claudeOAuthAccept {
		t.Fatalf("Accept = %q, want %q", got, claudeOAuthAccept)
	}
	if got := proxyReq.Header.Get("Accept-Encoding"); got != claudeOAuthAcceptEncoding {
		t.Fatalf("Accept-Encoding = %q, want %q", got, claudeOAuthAcceptEncoding)
	}
	if got := proxyReq.Header.Get("X-Stainless-Helper-Method"); got != claudeOAuthStainlessHelperMethod {
		t.Fatalf("X-Stainless-Helper-Method = %q, want %q", got, claudeOAuthStainlessHelperMethod)
	}

	official := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	official.Header.Set("Content-Type", "application/json")
	official.Header.Set("User-Agent", "claude-cli/2.1.196 (external, sdk-ts, agent-sdk/0.3.185)")
	official = withRequestContext(official, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})
	officialReq, err := cp.createProxyRequest(official, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest official: %v", err)
	}
	if got := officialReq.Header.Get("User-Agent"); got != claudeOAuthUserAgent {
		t.Fatalf("official User-Agent = %q", got)
	}
	assertClaudeOAuthSDKEnvelopeBody(t, officialReq)

	customEntrypoint := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	customEntrypoint.Header.Set("Content-Type", "application/json")
	customEntrypoint.Header.Set("User-Agent", "claude-cli/2.1.196 (external, sdk-secret)")
	customEntrypoint = withRequestContext(customEntrypoint, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})
	customEntrypointReq, err := cp.createProxyRequest(customEntrypoint, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest custom entrypoint: %v", err)
	}
	if got := customEntrypointReq.Header.Get("User-Agent"); got != claudeOAuthUserAgent {
		t.Fatalf("custom entrypoint User-Agent = %q, want synthesized %q", got, claudeOAuthUserAgent)
	}

	sdkCLI := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	sdkCLI.Header.Set("Content-Type", "application/json")
	sdkCLI.Header.Set("User-Agent", "claude-cli/2.1.196 (external, sdk-cli)")
	sdkCLI = withRequestContext(sdkCLI, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})
	sdkCLIReq, err := cp.createProxyRequest(sdkCLI, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest sdk-cli: %v", err)
	}
	assertClaudeOAuthSDKEnvelopeBody(t, sdkCLIReq)
}

func assertClaudeOAuthSDKEnvelopeBody(t *testing.T, req *http.Request) {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, string(body))
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want SDK empty tools", root["tools"])
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want SDK billing plus prompt", root["system"])
	}
	for _, item := range system {
		block, _ := item.(map[string]any)
		text := stringValue(block["text"])
		if strings.Contains(text, claudeOAuthSystemCorePrompt) || strings.Contains(text, claudeOAuthSessionGuidancePrompt) {
			t.Fatalf("SDK system included Claude Code prompt block: %#v", system)
		}
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", root["messages"])
	}
	firstMessage, _ := messages[0].(map[string]any)
	content, ok := firstMessage["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("messages[0].content = %#v, want SDK content without Claude Code prefix", firstMessage["content"])
	}
}

func TestCreateProxyRequest_ClaudeOAuthPreservesStringSystemInSDKEnvelope(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{"model":"claude-sonnet-4-5","system":"Keep it short.","messages":[]}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original.Header.Set("User-Agent", "claude-code/2.1.196 (external, cli)")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	var root map[string]any
	if err := json.NewDecoder(proxyReq.Body).Decode(&root); err != nil {
		t.Fatalf("Decode body: %v", err)
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want billing plus client system", root["system"])
	}
	attribution, ok := system[0].(map[string]any)
	if !ok || !isClaudeOAuthBillingHeaderText(stringValue(attribution["text"])) {
		t.Fatalf("system[0] = %#v, want billing attribution", system[0])
	}
	clientSystem, ok := system[1].(map[string]any)
	if !ok || clientSystem["text"] != "Keep it short." {
		t.Fatalf("system[1] = %#v, want client system preserved", system[1])
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages = %#v", root["messages"])
	}
	firstMessage, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("messages[0] = %#v", messages[0])
	}
	content, ok := firstMessage["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("messages[0].content = %#v", firstMessage["content"])
	}
	for _, item := range content {
		block, _ := item.(map[string]any)
		if strings.Contains(stringValue(block["text"]), "Keep it short.") {
			t.Fatalf("messages[0].content = %#v, client system should not be moved into messages", content)
		}
	}
}

func TestCreateProxyRequest_ClaudeOAuthResignsBillingHeaderAfterOverrides(t *testing.T) {
	dir := t.TempDir()
	svc := oauthpkg.NewService(dir)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
			Overrides: &config.ProviderOverrides{
				Model: strPtr("claude-sonnet-4-5"),
			},
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc

	body := []byte(`{
		"model":"claude-3-7-sonnet",
		"messages":[{"role":"user","content":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81.a1b; cc_entrypoint=user; cch=abcde;"}]}],
		"metadata":{"note":"x-anthropic-billing-header: cc_version=2.1.81.a1b; cc_entrypoint=metadata; cch=fedcb;"},
		"system":[
			{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81.a1b; cc_entrypoint=cli; cch=00000;"},
			{"type":"text","text":"You are a Claude agent, built on Anthropic's Claude Agent SDK."}
		]
	}`)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader(body))
	original.Header.Set("Content-Type", "application/json")
	original = withRequestContext(original, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	proxyReq, err := cp.createProxyRequest(original, cp.providers[0], "", "/v1/messages", body)
	if err != nil {
		t.Fatalf("createProxyRequest: %v", err)
	}

	rewrittenBody, err := io.ReadAll(proxyReq.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if strings.Contains(string(rewrittenBody), "cch=00000;") {
		t.Fatalf("body = %s, want signed non-zero cch", string(rewrittenBody))
	}
	if got := signClaudeOAuthMessageBody(rewrittenBody); string(got) != string(rewrittenBody) {
		t.Fatalf("body was not emitted in normalized billing form")
	}

	var root map[string]any
	if err := json.Unmarshal(rewrittenBody, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := root["model"]; got != "claude-sonnet-4-5" {
		t.Fatalf("model = %v, want claude-sonnet-4-5", got)
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) == 0 {
		t.Fatalf("system = %#v", root["system"])
	}
	systemBlock, ok := system[0].(map[string]any)
	if !ok {
		t.Fatalf("system[0] = %#v", system[0])
	}
	systemText, ok := systemBlock["text"].(string)
	if !ok {
		t.Fatalf("system[0].text = %#v", systemBlock["text"])
	}
	if cch := claudeOAuthTestCCH(systemText); cch == "" || cch == "00000" {
		t.Fatalf("system billing header was not signed: %q", systemText)
	}

	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages = %#v", root["messages"])
	}
	message, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("messages[0] = %#v", messages[0])
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("messages[0].content = %#v", message["content"])
	}
	foundOriginalMessageText := false
	for _, item := range content {
		messageBlock, _ := item.(map[string]any)
		if messageBlock != nil && messageBlock["text"] == "x-anthropic-billing-header: cc_version=2.1.81.a1b; cc_entrypoint=user; cch=abcde;" {
			foundOriginalMessageText = true
			break
		}
	}
	if !foundOriginalMessageText {
		t.Fatalf("messages[0].content = %#v, want original billing-like message text preserved", content)
	}

	metadata, ok := root["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", root["metadata"])
	}
	if got := metadata["note"]; got != "x-anthropic-billing-header: cc_version=2.1.81.a1b; cc_entrypoint=metadata; cch=fedcb;" {
		t.Fatalf("metadata.note = %#v", got)
	}
}

func TestNormalizeClaudeOAuthRequestSynthesizesAgentSDKEnvelopeForOfficialLookingUA(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"temperature":0.2}`)
	proxyReq := httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", bytes.NewReader(body))
	original.Header.Set("User-Agent", "claude-code/2.1.196 (external, cli)")
	requestCtx := RequestContext{
		ClientType:   ClientClaude,
		Family:       ProtocolFamilyClaude,
		Capability:   CapabilityClaudeMessages,
		UpstreamPath: "/v1/messages",
	}

	rewritten := normalizeClaudeOAuthRequest(body, proxyReq, original, requestCtx)

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if _, ok := root["temperature"]; ok {
		t.Fatalf("temperature should be removed: %#v", root["temperature"])
	}
	if got := root["max_tokens"]; got != float64(claudeOAuthDefaultMaxTokens) {
		t.Fatalf("max_tokens = %v", got)
	}
	metadata, ok := root["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", root["metadata"])
	}
	var userID map[string]any
	if err := json.Unmarshal([]byte(stringValue(metadata["user_id"])), &userID); err != nil {
		t.Fatalf("metadata.user_id is not JSON: %v", err)
	}
	if stringValue(userID["device_id"]) == "" || stringValue(userID["session_id"]) == "" {
		t.Fatalf("metadata.user_id = %#v, want device_id and session_id", userID)
	}
	if got := proxyReq.Header.Get("X-Claude-Code-Session-Id"); got != stringValue(userID["session_id"]) {
		t.Fatalf("X-Claude-Code-Session-Id = %q, want metadata session %q", got, userID["session_id"])
	}
	if _, ok := metadata["session_id"]; ok {
		t.Fatalf("metadata.session_id should be removed: %#v", metadata)
	}
	thinking, ok := root["thinking"].(map[string]any)
	if !ok || thinking["type"] != "adaptive" {
		t.Fatalf("thinking = %#v, want adaptive", root["thinking"])
	}
	outputConfig, ok := root["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != "high" {
		t.Fatalf("output_config = %#v, want effort high", root["output_config"])
	}
	contextManagement, ok := root["context_management"].(map[string]any)
	if !ok {
		t.Fatalf("context_management = %#v", root["context_management"])
	}
	edits, ok := contextManagement["edits"].([]any)
	if !ok || len(edits) == 0 {
		t.Fatalf("context_management.edits = %#v", contextManagement["edits"])
	}
	edit, ok := edits[0].(map[string]any)
	if !ok || edit["type"] != "clear_thinking_20251015" || edit["keep"] != "all" {
		t.Fatalf("context_management.edits[0] = %#v", edits[0])
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want SDK empty tools", root["tools"])
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want billing plus SDK prompt", root["system"])
	}
	systemBlock, _ := system[0].(map[string]any)
	if !isClaudeOAuthBillingHeaderText(stringValue(systemBlock["text"])) {
		t.Fatalf("system[0] = %#v, want billing header", system[0])
	}
	if cch := claudeOAuthTestCCH(stringValue(systemBlock["text"])); cch == "" || cch == "00000" {
		t.Fatalf("system billing cch = %q, want signed non-zero cch", cch)
	}
	if !strings.Contains(stringValue(systemBlock["text"]), "cc_version="+claudeOAuthBillingVersion(root["messages"])+";") {
		t.Fatalf("system[0].text = %q, want fingerprint based on original user prompt", systemBlock["text"])
	}
	sdkPrompt, _ := system[1].(map[string]any)
	if got := stringValue(sdkPrompt["text"]); got != claudeOAuthSystemPrompt {
		t.Fatalf("system[1].text = %q, want SDK prompt", got)
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("messages = %#v", root["messages"])
	}
	firstMessage, _ := messages[0].(map[string]any)
	content, ok := firstMessage["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("messages[0].content = %#v", firstMessage["content"])
	}
	finalBlock, ok := claudeOAuthLastTextBlock(content)
	if !ok || finalBlock["text"] != "hello" {
		t.Fatalf("final user block = %#v", finalBlock)
	}
	betas := proxyReq.Header.Get("Anthropic-Beta")
	for _, token := range []string{"context-management-2025-06-27", "effort-2025-11-24", "interleaved-thinking-2025-05-14", "advanced-tool-use-2025-11-20"} {
		if !strings.Contains(betas, token) {
			t.Fatalf("Anthropic-Beta = %q, want %q", betas, token)
		}
	}
}

func TestNormalizeClaudeOAuthRequestDefaultsToSDKEnvelope(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"temperature":0.2}`)
	proxyReq := httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", bytes.NewReader(body))

	rewritten := normalizeClaudeOAuthRequest(body, proxyReq, original, RequestContext{Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages})

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want SDK empty tools", root["tools"])
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want billing plus SDK prompt", root["system"])
	}
	sdkPrompt, _ := system[1].(map[string]any)
	if got := stringValue(sdkPrompt["text"]); got != claudeOAuthSystemPrompt {
		t.Fatalf("system[1].text = %q, want SDK prompt", got)
	}
	for _, item := range system {
		block, _ := item.(map[string]any)
		text := stringValue(block["text"])
		if strings.Contains(text, claudeOAuthSystemCorePrompt) || strings.Contains(text, claudeOAuthSessionGuidancePrompt) {
			t.Fatalf("SDK system included Claude Code prompt block: %#v", system)
		}
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", root["messages"])
	}
	firstMessage, _ := messages[0].(map[string]any)
	content, ok := firstMessage["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("messages[0].content = %#v, want only client content", firstMessage["content"])
	}
	block, _ := content[0].(map[string]any)
	if got := stringValue(block["text"]); got != "hello" {
		t.Fatalf("messages[0].content[0].text = %q", got)
	}
}

func TestNormalizeClaudeOAuthRequestSDKCustomSystemReplacesDefaultPrompt(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","system":"Keep it short.","messages":[{"role":"user","content":"hello"}]}`)
	rewritten := normalizeClaudeOAuthRequest(body, httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil), nil, RequestContext{Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages})

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) != 2 {
		t.Fatalf("system = %#v, want billing plus custom system", root["system"])
	}
	custom, _ := system[1].(map[string]any)
	if got := stringValue(custom["text"]); got != "Keep it short." {
		t.Fatalf("system[1].text = %q, want custom system", got)
	}
	for _, item := range system {
		block, _ := item.(map[string]any)
		if stringValue(block["text"]) == claudeOAuthSystemPrompt {
			t.Fatalf("system = %#v, SDK prompt should be replaced by custom system", system)
		}
	}
}

func TestNormalizeClaudeOAuthRequestSDKPreservesExplicitControlFields(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"max_tokens":1234,
		"thinking":{"type":"enabled","budget_tokens":999},
		"context_management":{"edits":[{"type":"client_edit","keep":"none"}]},
		"output_config":{"effort":"low"},
		"tools":[{"name":"CustomTool","description":"custom","input_schema":{"type":"object"}}]
	}`)
	rewritten := normalizeClaudeOAuthRequest(body, httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil), nil, RequestContext{Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages})

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := root["max_tokens"]; got != float64(1234) {
		t.Fatalf("max_tokens = %v, want client value", got)
	}
	thinking, ok := root["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(999) {
		t.Fatalf("thinking = %#v, want client value", root["thinking"])
	}
	contextManagement, ok := root["context_management"].(map[string]any)
	if !ok {
		t.Fatalf("context_management = %#v", root["context_management"])
	}
	edits, ok := contextManagement["edits"].([]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("context_management.edits = %#v", contextManagement["edits"])
	}
	edit, _ := edits[0].(map[string]any)
	if edit["type"] != "client_edit" || edit["keep"] != "none" {
		t.Fatalf("context_management.edits[0] = %#v, want client value", edits[0])
	}
	outputConfig, ok := root["output_config"].(map[string]any)
	if !ok || outputConfig["effort"] != "low" {
		t.Fatalf("output_config = %#v, want client value", root["output_config"])
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v, want client tools", root["tools"])
	}
	custom, _ := tools[0].(map[string]any)
	if custom["name"] != "CustomTool" {
		t.Fatalf("tools[0] = %#v, want client tool", tools[0])
	}
}

func TestNormalizeClaudeOAuthRequestRegeneratesOfficialLookingSystemAndPreservesClientTools(t *testing.T) {
	tools := make([]map[string]any, 0, len(claudeOAuthToolNames()))
	for _, name := range claudeOAuthToolNames() {
		tools = append(tools, map[string]any{
			"name":         name,
			"description":  "client " + name,
			"input_schema": map[string]any{"type": "object"},
		})
	}
	bodyRoot := map[string]any{
		"model": "claude-sonnet-4-5",
		"metadata": map[string]any{
			"user_id": `{"device_id":"client-device","account_uuid":"acct","session_id":"client-session"}`,
		},
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": claudeOAuthDeferredToolsPrompt},
				map[string]any{"type": "text", "text": claudeOAuthSkillsPrompt},
				map[string]any{"type": "text", "text": "hello", "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
			},
		}},
		"system": []any{
			map[string]any{"type": "text", "text": "x-anthropic-billing-header: cc_version=9.9.9.abc; cc_entrypoint=evil; cch=00000;"},
			map[string]any{"type": "text", "text": claudeOAuthSystemPrompt},
			map[string]any{"type": "text", "text": "client core", "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h", "scope": "global"}},
			map[string]any{"type": "text", "text": "client runtime", "cache_control": map[string]any{"type": "ephemeral", "ttl": "1h"}},
		},
		"tools": tools,
	}
	body, err := json.Marshal(bodyRoot)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	proxyReq := httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil)
	original := httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", bytes.NewReader(body))
	original.Header.Set("User-Agent", "claude-code/2.1.196 (external, cli)")
	rewritten := normalizeClaudeOAuthRequest(body, proxyReq, original, RequestContext{Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages})

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	system, ok := root["system"].([]any)
	if !ok || len(system) != 3 {
		t.Fatalf("system = %#v", root["system"])
	}
	core, _ := system[1].(map[string]any)
	if core["text"] != "client core" {
		t.Fatalf("system[1] = %#v, want client core preserved", system[1])
	}
	rewrittenTools, ok := root["tools"].([]any)
	if !ok || len(rewrittenTools) != len(claudeOAuthToolNames()) {
		t.Fatalf("tools = %#v", root["tools"])
	}
	bash, _ := rewrittenTools[1].(map[string]any)
	if bash["description"] != "client Bash" {
		t.Fatalf("tools[1] = %#v, want client Bash preserved", rewrittenTools[1])
	}
	systemBlock, _ := system[0].(map[string]any)
	if cch := claudeOAuthTestCCH(stringValue(systemBlock["text"])); cch == "" || cch == "00000" {
		t.Fatalf("system billing header was not signed: %q", systemBlock["text"])
	}
	if got := stringValue(systemBlock["text"]); strings.Contains(got, "9.9.9") || strings.Contains(got, "cc_entrypoint=evil") {
		t.Fatalf("system billing header preserved client-supplied runtime fingerprint: %q", got)
	}
	if got := stringValue(systemBlock["text"]); !strings.Contains(got, "cc_version="+claudeOAuthBillingVersion(root["messages"])+"; cc_entrypoint=sdk-cli;") {
		t.Fatalf("system billing header = %q, want regenerated current fingerprint", got)
	}
}

func TestNormalizeClaudeOAuthRequestPreservesExplicitCustomTools(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"CustomTool","description":"custom","input_schema":{"type":"object"}}]}`)
	rewritten := normalizeClaudeOAuthRequest(body, httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil), nil, RequestContext{Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages})

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", root["tools"])
	}
	custom, _ := tools[0].(map[string]any)
	if custom["name"] != "CustomTool" {
		t.Fatalf("tools[0] = %#v, want CustomTool", tools[0])
	}
}

func TestNormalizeClaudeOAuthRequestPreservesExplicitEmptyTools(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}],"tools":[]}`)
	rewritten := normalizeClaudeOAuthRequest(body, httptest.NewRequest(http.MethodPost, "http://proxy/v1/messages", nil), nil, RequestContext{Family: ProtocolFamilyClaude, Capability: CapabilityClaudeMessages})

	var root map[string]any
	if err := json.Unmarshal(rewritten, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	tools, ok := root["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v, want explicit empty tools preserved", root["tools"])
	}
}

func TestClaudeOAuthBillingVersionUsesOfficialMessageFingerprint(t *testing.T) {
	messages := []any{
		map[string]any{
			"role":    "user",
			"content": []any{map[string]any{"type": "text", "text": "hello"}},
		},
	}
	if got := claudeOAuthBillingVersion(messages); got != "2.1.196.68b" {
		t.Fatalf("billing version = %q, want 2.1.196.68b", got)
	}
}

func TestForwardWithFailover_ClaudeOAuthRefreshesAndRetriesOn401(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var refreshCalls int32
	var upstreamCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		assertClaudeRefreshJSONRequest(t, r, "test-client", "refresh-1")
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600,"account":{"uuid":"acct_123","email_address":"sean@example.com"},"organization":{"uuid":"org_123","name":"Example"}}`)
	}))
	defer tokenServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithProviderClient(&oauthpkg.ClaudeClient{
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			HTTPClient:   tokenServer.Client(),
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/callback",
			Now:          func() time.Time { return now },
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "claude-sean-example-com",
		Provider:     config.OAuthProviderClaude,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(time.Hour),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		call := atomic.AddInt32(&upstreamCalls, 1)
		switch call {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Fatalf("Authorization(first) = %q", got)
			}
			return newResponse(http.StatusUnauthorized, http.Header{"Content-Type": []string{"application/json"}}, `{"error":"expired"}`), nil
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer access-2" {
				t.Fatalf("Authorization(second) = %q", got)
			}
			return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"ok":true}`), nil
		default:
			t.Fatalf("unexpected upstream call %d", call)
			return nil, nil
		}
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"model":"claude-3-7-sonnet","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1/messages")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestForwardCountTokensSingleShot_ClaudeOAuthRefreshesAndRetriesOn401(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 22, 12, 0, 0, 0, time.UTC)
	var refreshCalls int32
	var upstreamCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		assertClaudeRefreshJSONRequest(t, r, "test-client", "refresh-1")
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","expires_in":3600,"account":{"uuid":"acct_123","email_address":"sean@example.com"},"organization":{"uuid":"org_123","name":"Example"}}`)
	}))
	defer tokenServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithProviderClient(&oauthpkg.ClaudeClient{
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			HTTPClient:   tokenServer.Client(),
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/callback",
			Now:          func() time.Time { return now },
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:          "claude-sean-example-com",
		Provider:     config.OAuthProviderClaude,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(time.Hour),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		call := atomic.AddInt32(&upstreamCalls, 1)
		for _, key := range []string{
			"Anthropic-Dangerous-Direct-Browser-Access",
			"X-Stainless-Retry-Count",
			"X-Stainless-Runtime",
			"X-Stainless-Lang",
			"X-Stainless-Timeout",
			"X-Stainless-Package-Version",
			"X-Stainless-Runtime-Version",
			"X-Stainless-Os",
			"X-Stainless-Arch",
			"X-Stainless-Helper-Method",
			"X-App-Name",
			"X-App-Ver",
			"X-Client-App",
		} {
			if got := r.Header.Get(key); got != "" {
				t.Fatalf("%s(%d) = %q, want empty", key, call, got)
			}
		}
		switch call {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
				t.Fatalf("Authorization(first) = %q", got)
			}
			return newResponse(http.StatusUnauthorized, http.Header{"Content-Type": []string{"application/json"}}, `{"error":"expired"}`), nil
		case 2:
			if got := r.Header.Get("Authorization"); got != "Bearer access-2" {
				t.Fatalf("Authorization(second) = %q", got)
			}
			return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"application/json"}}, `{"input_tokens":7}`), nil
		default:
			t.Fatalf("unexpected upstream call %d", call)
			return nil, nil
		}
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"model":"claude-3-7-sonnet","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages/count_tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")
	req.Header.Set("X-Stainless-Retry-Count", "99")
	req.Header.Set("X-Stainless-Runtime", "attacker-runtime")
	req.Header.Set("X-Stainless-Lang", "attacker-lang")
	req.Header.Set("X-Stainless-Timeout", "1")
	req.Header.Set("X-Stainless-Package-Version", "attacker-package")
	req.Header.Set("X-Stainless-Runtime-Version", "attacker-runtime-version")
	req.Header.Set("X-Stainless-Os", "attacker-os")
	req.Header.Set("X-Stainless-Arch", "attacker-arch")
	req.Header.Set("X-Stainless-Helper-Method", "attacker-helper")
	req.Header.Set("X-App-Name", "attacker-app-name")
	req.Header.Set("X-App-Ver", "9.9.9")
	req.Header.Set("X-Client-App", "attacker-client")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeCountTokens,
		UpstreamPath:   "/v1/messages/count_tokens",
		UnifiedIngress: true,
	})

	cp.forwardCountTokensSingleShot(rr, req, "/v1/messages/count_tokens")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"input_tokens":7}` {
		t.Fatalf("body = %q", got)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&upstreamCalls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestForwardWithFailover_ClaudeOAuthUsesUsageResetForCooldown(t *testing.T) {
	dir := t.TempDir()
	resetAt := time.Now().Add(5 * time.Hour).UTC()
	var usageCalls int32
	var claudeCalls int32

	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&usageCalls, 1)
		if got := r.Method; got != http.MethodGet {
			t.Fatalf("usage method = %q, want GET", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("usage authorization = %q", got)
		}
		if got := r.Header.Get("Anthropic-Beta"); got != "oauth-2025-04-20" {
			t.Fatalf("usage beta = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{
			"five_hour": {
				"utilization": 100,
				"resets_at": %q
			},
			"seven_day": {
				"utilization": 100,
				"resets_at": %q
			}
		}`, resetAt.Format(time.RFC3339), resetAt.Add(72*time.Hour).Format(time.RFC3339)))
	}))
	defer usageServer.Close()

	svc := oauthpkg.NewService(dir,
		oauthpkg.WithClaudeClient(&oauthpkg.ClaudeClient{
			UsageURL:     usageServer.URL,
			HTTPClient:   usageServer.Client(),
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/callback",
			Now:          time.Now,
		}),
	)
	if err := svc.Store().Save(&oauthpkg.Credential{
		Ref:         "claude-sean-example-com",
		Provider:    config.OAuthProviderClaude,
		Email:       "sean@example.com",
		AccessToken: "access-1",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cp := newClientProxy(ClientClaude, config.ClientModeAuto, "", []config.Provider{
		{
			Name:          "claude-oauth",
			AuthType:      config.ProviderAuthTypeOAuth,
			OAuthProvider: config.OAuthProviderClaude,
			OAuthRef:      "claude-sean-example-com",
			Priority:      1,
		},
		{Name: "fallback", BaseURL: "http://fallback", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "api.anthropic.com":
			atomic.AddInt32(&claudeCalls, 1)
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			h.Set("Anthropic-RateLimit-Unified-Representative-Claim", "five_hour")
			return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"rate limit"}}`), nil
		case "fallback":
			return newResponse(http.StatusOK, nil, "ok"), nil
		default:
			t.Fatalf("unexpected host %q", r.URL.Host)
			return nil, nil
		}
	})

	rr := httptest.NewRecorder()
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[]}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/claudecode/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withRequestContext(req, RequestContext{
		ClientType:     ClientClaude,
		Family:         ProtocolFamilyClaude,
		Capability:     CapabilityClaudeMessages,
		UpstreamPath:   "/v1/messages",
		UnifiedIngress: true,
	})

	cp.forwardWithFailover(rr, req, "/v1/messages")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d body=%s", got, rr.Body.String())
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 1 {
		t.Fatalf("claude calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&usageCalls); got != 1 {
		t.Fatalf("usage calls = %d, want 1", got)
	}
	if !cp.isDeactivated(0) {
		t.Fatalf("expected claude oauth provider to be in cooldown")
	}
	if remaining := time.Until(cp.deactivationUntil(0)); remaining < 4*time.Hour {
		t.Fatalf("expected cooldown from usage reset, got %s", remaining)
	}
}

func claudeTestIntPtr(v int) *int {
	return &v
}

func claudeOAuthTestCCH(text string) string {
	start := strings.Index(strings.ToLower(text), "cch=")
	if start < 0 {
		return ""
	}
	start += len("cch=")
	end := start
	for end < len(text) && isClaudeOAuthHexDigit(text[end]) {
		end++
	}
	return text[start:end]
}

func assertClaudeRefreshJSONRequest(t *testing.T, r *http.Request, wantClientID string, wantRefreshToken string) {
	t.Helper()
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("Decode refresh request: %v", err)
	}
	if got := req["grant_type"]; got != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", got)
	}
	if got := req["client_id"]; got != wantClientID {
		t.Fatalf("client_id = %q, want %q", got, wantClientID)
	}
	if got := req["refresh_token"]; got != wantRefreshToken {
		t.Fatalf("refresh_token = %q, want %q", got, wantRefreshToken)
	}
	if _, ok := req["scope"]; ok {
		t.Fatalf("scope should be omitted from claude refresh request: %#v", req)
	}
}
