package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
)

const (
	defaultCodexOAuthBaseURL = "https://chatgpt.com/backend-api/codex"
	codexOAuthVersion        = "0.142.4"
	codexOAuthUserAgent      = "codex_cli_rs/0.142.4 (Mac OS 26.5.1; arm64) iTerm.app/3.6.11"
	codexOAuthOriginator     = "codex_cli_rs"
)

var codexOAuthAllowedHeaders = map[string]bool{
	"accept":                                 true,
	"accept-language":                        true,
	"chatgpt-account-id":                     true,
	"content-type":                           true,
	"conversation-id":                        true,
	"conversation_id":                        true,
	"openai-beta":                            true,
	"user-agent":                             true,
	"originator":                             true,
	"session-id":                             true,
	"session_id":                             true,
	"thread-id":                              true,
	"thread_id":                              true,
	"version":                                true,
	"x-codex-beta-features":                  true,
	"x-codex-installation-id":                true,
	"x-codex-window-id":                      true,
	"x-client-request-id":                    true,
	"x-oai-attestation":                      true,
	"x-openai-fedramp":                       true,
	"x-openai-internal-codex-residency":      true,
	"x-openai-internal-codex-responses-lite": true,
	"x-openai-memgen-request":                true,
	"x-responsesapi-include-timing-metrics":  true,
}

type codexOAuthRequestContext struct {
	sessionID      string
	threadID       string
	installationID string
	turnMetadata   string
}

type codexOAuthTurnMetadataContextKey struct{}

func providerSupportsCapability(provider config.Provider, capability RequestCapability) bool {
	if !provider.UsesOAuth() {
		return true
	}

	switch provider.NormalizedOAuthProvider() {
	case config.OAuthProviderCodex:
		return capability == CapabilityOpenAIResponses
	case config.OAuthProviderClaude:
		return capability == CapabilityClaudeMessages || capability == CapabilityClaudeCountTokens
	case config.OAuthProviderGemini:
		return capability == CapabilityGeminiGenerateContent ||
			capability == CapabilityGeminiStreamGenerate ||
			capability == CapabilityGeminiCountTokens
	default:
		return false
	}
}

func supportedCapabilitySummary(provider config.Provider) string {
	if !provider.UsesOAuth() {
		return "all configured request types"
	}

	switch provider.NormalizedOAuthProvider() {
	case config.OAuthProviderCodex:
		return "OpenAI Responses requests"
	case config.OAuthProviderClaude:
		return "Claude messages and count_tokens requests"
	case config.OAuthProviderGemini:
		return "Gemini generateContent, streamGenerateContent, and countTokens requests"
	default:
		return "its configured request types"
	}
}

func (cp *ClientProxy) createOAuthProxyRequestWithPayloadForProvider(original *http.Request, provider config.Provider, providerIndex int, path string, payload *requestPayload) (*http.Request, error) {
	if cp == nil || cp.oauth == nil {
		return nil, fmt.Errorf("oauth service is unavailable")
	}
	if payload == nil {
		payload = newRequestPayload(nil)
	}

	switch provider.NormalizedOAuthProvider() {
	case config.OAuthProviderCodex:
		return cp.createCodexOAuthRequestWithPayloadForProvider(original, provider, providerIndex, path, payload)
	case config.OAuthProviderClaude:
		return cp.createClaudeOAuthRequestWithPayloadForProvider(original, provider, providerIndex, path, payload)
	case config.OAuthProviderGemini:
		return cp.createGeminiOAuthRequestWithPayloadForProvider(original, provider, providerIndex, path, payload)
	default:
		return nil, fmt.Errorf("unsupported oauth provider %q", provider.NormalizedOAuthProvider())
	}
}

func (cp *ClientProxy) createCodexOAuthRequestWithPayloadForProvider(original *http.Request, provider config.Provider, providerIndex int, path string, payload *requestPayload) (*http.Request, error) {
	if original == nil {
		return nil, fmt.Errorf("original request is nil")
	}

	requestCtx, ok := requestContextFromRequest(original)
	if !ok {
		requestCtx = requestContextForClientPath(cp.clientType, path, false)
	}
	if requestCtx.Capability != CapabilityOpenAIResponses {
		return nil, fmt.Errorf("codex oauth only supports OpenAI responses requests")
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

	targetPath, stream, requestBody, err := payload.codexOAuthRequest(original, requestCtx, provider, path)
	if err != nil {
		return nil, err
	}
	codexCtx := codexOAuthRequestContextForRequest(original, targetPath)
	ensureCodexOAuthTurnMetadata(original, codexCtx, targetPath)
	requestBody, err = applyCodexOAuthBodyContext(requestBody, stream, codexCtx)
	if err != nil {
		return nil, err
	}
	targetURL, err := buildTargetURL(defaultCodexOAuthBaseURL, targetPath, original.URL.RawQuery)
	if err != nil {
		return nil, err
	}

	proxyReq, err := http.NewRequestWithContext(original.Context(), original.Method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}
	copyCodexOAuthHeaders(proxyReq.Header, original.Header)
	removeStaleCodexOAuthBetaHeader(proxyReq.Header)

	addForwardedHeaders(proxyReq, original)
	clearAuthCarriers(proxyReq)
	proxyReq.Header.Set("Authorization", "Bearer "+accessToken)
	applyCodexOAuthHeaders(proxyReq, cred, stream, codexCtx, targetPath)
	proxyReq.ContentLength = int64(len(requestBody))
	proxyReq.Header.Del("Content-Length")
	return proxyReq, nil
}

func buildCodexOAuthRequest(path string, body []byte) (string, bool, []byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", false, nil, fmt.Errorf("responses request body is required")
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", false, nil, fmt.Errorf("responses request body must be valid json: %w", err)
	}
	return buildCodexOAuthRequestFromRoot(path, root, body)
}

func buildCodexOAuthRequestFromRoot(path string, root map[string]any, body []byte) (string, bool, []byte, error) {
	path = normalizeUpstreamPath(path)

	switch {
	case matchesExactPath(path, "/v1/responses/compact"):
		_, rewritten, err := normalizeCodexOAuthResponsesRoot(root, true)
		if err != nil {
			return "", false, nil, err
		}
		return "/responses/compact", false, rewritten, nil
	case matchesExactPath(path, "/v1/responses"):
		stream, rewritten, err := normalizeCodexOAuthResponsesRoot(root, false)
		if err != nil {
			return "", false, nil, err
		}
		if !stream {
			rewritten, err = forceCodexOAuthStreamingBody(rewritten)
			if err != nil {
				return "", false, nil, err
			}
		}
		return "/responses", true, rewritten, nil
	case pathMatchesPrefix(path, "/v1/responses/"):
		return strings.TrimPrefix(path, "/v1"), false, body, nil
	default:
		return "", false, nil, fmt.Errorf("codex oauth does not support path %q", path)
	}
}

func forceCodexOAuthStreamingBody(body []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("responses request body must be valid json: %w", err)
	}
	root["stream"] = true
	root["store"] = false
	rewritten, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal codex oauth request: %w", err)
	}
	return rewritten, nil
}

func normalizeCodexOAuthResponsesRoot(root map[string]any, forceCompact bool) (bool, []byte, error) {
	if root == nil {
		return false, nil, fmt.Errorf("responses request body must be a json object")
	}
	stream, _ := root["stream"].(bool)
	if forceCompact {
		stream = false
	}
	if stream {
		root["stream"] = true
		root["store"] = false
	} else {
		delete(root, "stream")
		delete(root, "store")
	}
	if forceCompact {
		delete(root, "include")
		delete(root, "tool_choice")
		delete(root, "client_metadata")
	}

	delete(root, "prompt_cache_retention")
	delete(root, "safety_identifier")
	delete(root, "stream_options")
	delete(root, "max_output_tokens")
	delete(root, "max_completion_tokens")
	delete(root, "temperature")
	delete(root, "top_p")
	delete(root, "frequency_penalty")
	delete(root, "presence_penalty")
	normalizeCodexOAuthLegacyFunctionFields(root)
	normalizeCodexOAuthInput(root)
	if instructions, ok := root["instructions"]; !ok || instructions == nil {
		root["instructions"] = ""
	}
	if !forceCompact {
		ensureCodexOAuthResponseDefaults(root)
	}

	rewritten, err := json.Marshal(root)
	if err != nil {
		return false, nil, fmt.Errorf("marshal codex oauth request: %w", err)
	}
	return stream, rewritten, nil
}

func applyCodexOAuthHeaders(proxyReq *http.Request, cred *oauthpkg.Credential, stream bool, codexCtx codexOAuthRequestContext, targetPath string) {
	if proxyReq == nil {
		return
	}

	proxyReq.Header.Set("Content-Type", "application/json")
	if stream {
		proxyReq.Header.Set("Accept", "text/event-stream")
	} else {
		proxyReq.Header.Set("Accept", "application/json")
	}
	proxyReq.Header.Set("Connection", "Keep-Alive")
	proxyReq.Header.Set("User-Agent", codexOAuthUserAgent)
	proxyReq.Header.Set("Originator", codexOAuthOriginator)
	proxyReq.Header.Set("Version", codexOAuthVersion)
	if sanitizeCodexOAuthIdentifier(proxyReq.Header.Get("Session-Id")) == "" && codexCtx.sessionID != "" {
		proxyReq.Header.Set("Session-Id", codexCtx.sessionID)
	}
	proxyReq.Header.Del("Session_id")
	proxyReq.Header.Del("Session_Id")
	if sanitizeCodexOAuthIdentifier(proxyReq.Header.Get("Thread-Id")) == "" && codexCtx.threadID != "" {
		proxyReq.Header.Set("Thread-Id", codexCtx.threadID)
	}
	proxyReq.Header.Del("Thread_id")
	proxyReq.Header.Del("Thread_Id")
	if sanitizeCodexOAuthIdentifier(proxyReq.Header.Get("X-Client-Request-Id")) == "" && codexCtx.threadID != "" {
		proxyReq.Header.Set("X-Client-Request-Id", codexCtx.threadID)
	}
	if sanitizeCodexOAuthIdentifier(proxyReq.Header.Get("X-Codex-Installation-Id")) == "" && codexCtx.installationID != "" {
		proxyReq.Header.Set("X-Codex-Installation-Id", codexCtx.installationID)
	}
	if sanitizeCodexOAuthIdentifier(proxyReq.Header.Get("X-Codex-Window-Id")) == "" && codexCtx.threadID != "" {
		proxyReq.Header.Set("X-Codex-Window-Id", codexCtx.threadID+":0")
	}
	proxyReq.Header.Del("X-Codex-Parent-Thread-Id")
	if (targetPath == "/responses/compact" || strings.TrimSpace(proxyReq.Header.Get("X-Codex-Turn-Metadata")) == "") && codexCtx.sessionID != "" && codexCtx.threadID != "" {
		metadata := strings.TrimSpace(codexCtx.turnMetadata)
		if metadata == "" {
			metadata = codexOAuthTurnMetadata(codexCtx, targetPath)
		}
		if metadata != "" {
			proxyReq.Header.Set("X-Codex-Turn-Metadata", metadata)
		}
	}

	if cred != nil && strings.TrimSpace(cred.AccountID) != "" {
		proxyReq.Header.Set("ChatGPT-Account-ID", strings.TrimSpace(cred.AccountID))
	} else {
		proxyReq.Header.Del("Chatgpt-Account-Id")
	}
	if cred != nil && strings.EqualFold(strings.TrimSpace(cred.Metadata["chatgpt_account_is_fedramp"]), "true") {
		proxyReq.Header.Set("X-OpenAI-Fedramp", "true")
	} else {
		proxyReq.Header.Del("X-OpenAI-Fedramp")
	}
}

func newCodexOAuthRequestContext(headers http.Header) codexOAuthRequestContext {
	sessionID := firstSafeCodexOAuthHeader(headers, "Session-Id", "Session_id")
	if sessionID == "" {
		sessionID = newCodexUUID()
	}
	threadID := firstSafeCodexOAuthHeader(headers, "Thread-Id", "Thread_id")
	if threadID == "" {
		threadID = newCodexUUID()
	}
	installationID := firstSafeCodexOAuthHeader(headers, "X-Codex-Installation-Id")
	if installationID == "" {
		installationID = newCodexUUID()
	}
	return codexOAuthRequestContext{
		sessionID:      sessionID,
		threadID:       threadID,
		installationID: installationID,
	}
}

func codexOAuthRequestContextForRequest(req *http.Request, targetPath string) codexOAuthRequestContext {
	if req == nil {
		codexCtx := newCodexOAuthRequestContext(nil)
		codexCtx.turnMetadata = codexOAuthTurnMetadata(codexCtx, targetPath)
		return codexCtx
	}
	codexCtx := newCodexOAuthRequestContext(req.Header)
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if sanitizeCodexOAuthIdentifier(req.Header.Get("Session-Id")) == "" && codexCtx.sessionID != "" {
		req.Header.Set("Session-Id", codexCtx.sessionID)
	}
	if sanitizeCodexOAuthIdentifier(req.Header.Get("Thread-Id")) == "" && codexCtx.threadID != "" {
		req.Header.Set("Thread-Id", codexCtx.threadID)
	}
	if sanitizeCodexOAuthIdentifier(req.Header.Get("X-Codex-Installation-Id")) == "" && codexCtx.installationID != "" {
		req.Header.Set("X-Codex-Installation-Id", codexCtx.installationID)
	}
	if existing, _ := req.Context().Value(codexOAuthTurnMetadataContextKey{}).(string); strings.TrimSpace(existing) != "" {
		codexCtx.turnMetadata = existing
	} else {
		codexCtx.turnMetadata = codexOAuthTurnMetadata(codexCtx, targetPath)
		if codexCtx.turnMetadata != "" {
			*req = *req.WithContext(context.WithValue(req.Context(), codexOAuthTurnMetadataContextKey{}, codexCtx.turnMetadata))
		}
	}
	if codexCtx.turnMetadata != "" {
		req.Header.Set("X-Codex-Turn-Metadata", codexCtx.turnMetadata)
	}
	return codexCtx
}

func ensureCodexOAuthTurnMetadata(req *http.Request, codexCtx codexOAuthRequestContext, targetPath string) {
	if req == nil || codexCtx.sessionID == "" || codexCtx.threadID == "" {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	metadata := strings.TrimSpace(codexCtx.turnMetadata)
	if metadata == "" {
		metadata = codexOAuthTurnMetadata(codexCtx, targetPath)
	}
	if metadata != "" {
		req.Header.Set("X-Codex-Turn-Metadata", metadata)
	}
}

func firstSafeCodexOAuthHeader(headers http.Header, keys ...string) string {
	for _, key := range keys {
		value := sanitizeCodexOAuthIdentifier(headers.Get(key))
		if value != "" {
			return value
		}
	}
	return ""
}

func sanitizeCodexOAuthIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"@", "sk-", "secret", "token", "provider", "customer", "raw_ref", "raw-ref", ".env"} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == ':' || r == '.':
		default:
			return ""
		}
	}
	return value
}

func newCodexUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func codexOAuthTurnMetadata(codexCtx codexOAuthRequestContext, targetPath string) string {
	windowID := codexCtx.threadID + ":0"
	metadata := map[string]any{
		"session_id":              codexCtx.sessionID,
		"thread_id":               codexCtx.threadID,
		"thread_source":           "user",
		"turn_id":                 newCodexUUID(),
		"sandbox":                 "none",
		"turn_started_at_unix_ms": time.Now().UnixMilli(),
		"request_kind":            "turn",
		"window_id":               windowID,
	}
	if targetPath == "/responses/compact" {
		metadata["request_kind"] = "compaction"
		metadata["compaction"] = map[string]any{
			"trigger":        "manual",
			"reason":         "user_requested",
			"implementation": "responses_compaction_v2",
			"phase":          "standalone_turn",
			"strategy":       "memento",
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func applyCodexOAuthBodyContext(body []byte, stream bool, codexCtx codexOAuthRequestContext) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 || codexCtx.threadID == "" {
		return body, nil
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("responses request body must be valid json: %w", err)
	}
	if strings.TrimSpace(asString(root["prompt_cache_key"])) == "" {
		root["prompt_cache_key"] = codexCtx.threadID
	}
	if stream && codexCtx.installationID != "" {
		metadata, _ := root["client_metadata"].(map[string]any)
		if metadata == nil {
			metadata = make(map[string]any, 1)
		}
		if strings.TrimSpace(asString(metadata["x-codex-installation-id"])) == "" {
			metadata["x-codex-installation-id"] = codexCtx.installationID
		}
		root["client_metadata"] = metadata
	}
	if _, ok := root["reasoning"].(map[string]any); stream && ok {
		ensureCodexOAuthInclude(root, "reasoning.encrypted_content")
	}

	rewritten, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal codex oauth request: %w", err)
	}
	return rewritten, nil
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}

func ensureCodexOAuthInclude(root map[string]any, value string) {
	if root == nil || value == "" {
		return
	}
	include, _ := root["include"].([]any)
	for _, item := range include {
		if strings.TrimSpace(asString(item)) == value {
			return
		}
	}
	root["include"] = append(include, value)
}

func copyCodexOAuthHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) || !codexOAuthAllowedHeaders[strings.ToLower(strings.TrimSpace(key))] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func removeStaleCodexOAuthBetaHeader(headers http.Header) {
	if headers == nil {
		return
	}
	values := headers.Values("OpenAI-Beta")
	if len(values) == 0 {
		return
	}
	filtered := values[:0]
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), "responses=experimental") {
			continue
		}
		filtered = append(filtered, value)
	}
	headers.Del("OpenAI-Beta")
	for _, value := range filtered {
		headers.Add("OpenAI-Beta", value)
	}
}

func normalizeCodexOAuthLegacyFunctionFields(root map[string]any) {
	if root == nil {
		return
	}
	if functionsRaw, ok := root["functions"]; ok {
		if functions, ok := functionsRaw.([]any); ok {
			existing, _ := root["tools"].([]any)
			tools := make([]any, 0, len(existing)+len(functions))
			tools = append(tools, existing...)
			for _, function := range functions {
				tools = append(tools, map[string]any{
					"type":     "function",
					"function": function,
				})
			}
			root["tools"] = tools
		}
		delete(root, "functions")
	}
	if functionCall, ok := root["function_call"]; ok {
		switch v := functionCall.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				root["tool_choice"] = trimmed
			}
		case map[string]any:
			if name, ok := v["name"].(string); ok && strings.TrimSpace(name) != "" {
				root["tool_choice"] = map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": strings.TrimSpace(name),
					},
				}
			}
		}
		delete(root, "function_call")
	}
}

func normalizeCodexOAuthInput(root map[string]any) {
	if root == nil {
		return
	}
	switch input := root["input"].(type) {
	case string:
		trimmed := strings.TrimSpace(input)
		if trimmed == "" {
			root["input"] = []any{}
		} else {
			root["input"] = []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{
							"type": "input_text",
							"text": input,
						},
					},
				},
			}
		}
	case []any:
		for _, item := range input {
			msg, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(asString(msg["type"])), "message") {
				delete(msg, "type")
			}
		}
		extractCodexOAuthSystemInstructions(root, input)
	}
}

func ensureCodexOAuthResponseDefaults(root map[string]any) {
	if root == nil {
		return
	}
	if _, ok := root["tools"]; !ok {
		root["tools"] = []any{}
	}
	if _, ok := root["tool_choice"]; !ok {
		root["tool_choice"] = "auto"
	}
	if _, ok := root["parallel_tool_calls"]; !ok {
		root["parallel_tool_calls"] = false
	}
	if codexOAuthIncludeMissingOrEmpty(root["include"]) {
		if _, hasReasoning := root["reasoning"]; hasReasoning {
			root["include"] = []any{"reasoning.encrypted_content"}
		} else {
			root["include"] = []any{}
		}
	}
}

func codexOAuthIncludeMissingOrEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch include := value.(type) {
	case []any:
		return len(include) == 0
	case []string:
		return len(include) == 0
	default:
		return false
	}
}

func extractCodexOAuthSystemInstructions(root map[string]any, input []any) {
	if root == nil || len(input) == 0 {
		return
	}

	var systemTexts []string
	filtered := make([]any, 0, len(input))
	for _, item := range input {
		msg, ok := item.(map[string]any)
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		role, _ := msg["role"].(string)
		if !strings.EqualFold(strings.TrimSpace(role), "system") {
			filtered = append(filtered, item)
			continue
		}
		if text := extractCodexOAuthContentText(msg["content"]); text != "" {
			systemTexts = append(systemTexts, text)
		}
	}
	if len(systemTexts) == 0 {
		return
	}

	instructions := strings.Join(systemTexts, "\n\n")
	if existing, ok := root["instructions"].(string); ok && strings.TrimSpace(existing) != "" {
		root["instructions"] = instructions + "\n\n" + strings.TrimSpace(existing)
	} else {
		root["instructions"] = instructions
	}
	root["input"] = filtered
}

func extractCodexOAuthContentText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, part := range v {
			msg, ok := part.(map[string]any)
			if !ok {
				continue
			}
			partType, _ := msg["type"].(string)
			if partType != "" && partType != "text" && partType != "input_text" {
				continue
			}
			text, _ := msg["text"].(string)
			text = strings.TrimSpace(text)
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func (cp *ClientProxy) doProviderRequestWithPayload(original *http.Request, provider config.Provider, providerIndex int, apiKey string, path string, payload *requestPayload) (*http.Response, bool, error) {
	proxyReq, err := cp.createProxyRequestWithPayloadForProvider(original, provider, providerIndex, apiKey, path, payload)
	if err != nil {
		return nil, false, err
	}
	resp, err := cp.doPreparedProviderRequest(proxyReq, providerIndex)
	if err != nil || !provider.UsesOAuth() || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		if err != nil || resp == nil {
			return resp, true, err
		}
		resp, err = prepareOAuthProviderResponse(original, provider, resp)
		return resp, true, err
	}
	if cp == nil || cp.oauth == nil {
		return resp, true, err
	}
	refreshed, refreshErr := cp.oauth.RefreshWithHTTPClient(original.Context(), provider.NormalizedOAuthProvider(), provider.NormalizedOAuthRef(), cp.oauthHTTPClientForProvider(provider, providerIndex))
	if refreshErr != nil || refreshed == nil || strings.TrimSpace(refreshed.AccessToken) == "" {
		return resp, true, err
	}

	_ = resp.Body.Close()
	proxyReq, err = cp.createProxyRequestWithPayloadForProvider(original, provider, providerIndex, apiKey, path, payload)
	if err != nil {
		return nil, false, err
	}
	resp, err = cp.doPreparedProviderRequest(proxyReq, providerIndex)
	if err != nil || resp == nil {
		return resp, true, err
	}
	resp, err = prepareOAuthProviderResponse(original, provider, resp)
	return resp, true, err
}

func (cp *ClientProxy) oauthHTTPClientForProvider(provider config.Provider, providerIndex int) *http.Client {
	if cp == nil || providerIndex < 0 {
		return nil
	}
	if providerIndex < len(cp.providerProxyPolicies) && cp.providerProxyPolicies[providerIndex].mode == upstreamProxyPolicyDirect && provider.NormalizedOAuthProvider() == config.OAuthProviderClaude {
		return nil
	}
	if providerIndex < len(cp.providerProxyPolicies) && cp.providerProxyPolicies[providerIndex].mode == upstreamProxyPolicyEnvironment {
		if OAuthProviderUsesEnvironmentProxy(provider.NormalizedOAuthProvider()) {
			return cp.upstreamHTTPClient(providerIndex)
		}
		return nil
	}
	return cp.upstreamHTTPClient(providerIndex)
}

func (cp *ClientProxy) doPreparedProviderRequest(proxyReq *http.Request, providerIndex int) (*http.Response, error) {
	if proxyReq == nil {
		return nil, fmt.Errorf("proxy request is nil")
	}
	//nolint:gosec // proxyReq.URL is controlled by buildCodexOAuthRequest, not user input
	return cp.upstreamHTTPClient(providerIndex).Do(proxyReq)
}
