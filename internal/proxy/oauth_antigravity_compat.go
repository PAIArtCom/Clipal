package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

const (
	antigravityOAuthFallbackVersion = "2.13.0"
	antigravityOAuthInfoPlist       = "/Applications/Antigravity.app/Contents/Info.plist"
	antigravityOAuthGeneratePath    = "/v1internal:generateContent"
	antigravityOAuthStreamPath      = "/v1internal:streamGenerateContent"
	antigravityOAuthCountTokensPath = "/v1internal:countTokens"
	antigravityOAuthModelsPath      = "/v1internal:fetchAvailableModels"
)

var (
	antigravityOAuthVersionOnce sync.Once
	antigravityOAuthVersion     string
	antigravityOAuthPlistRE     = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)
)

func buildAntigravityOAuthRequest(capability RequestCapability, path string, body []byte, projectID string) (string, []byte, error) {
	if capability == CapabilityGeminiModels {
		return antigravityOAuthModelsPath, buildAntigravityOAuthModelsEnvelope(projectID), nil
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil, fmt.Errorf("antigravity oauth request body is required")
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", nil, fmt.Errorf("antigravity oauth request body must be valid json: %w", err)
	}
	return buildAntigravityOAuthRequestFromRoot(capability, path, root, projectID)
}

func buildAntigravityOAuthRequestFromRoot(capability RequestCapability, path string, root map[string]any, projectID string) (string, []byte, error) {
	if capability == CapabilityGeminiModels {
		return antigravityOAuthModelsPath, buildAntigravityOAuthModelsEnvelope(projectID), nil
	}
	if root == nil {
		return "", nil, fmt.Errorf("antigravity oauth request body must be a json object")
	}

	modelName, err := geminiModelFromPath(path)
	if err != nil {
		return "", nil, fmt.Errorf("antigravity oauth does not support path %q", normalizeUpstreamPath(path))
	}

	var rewrittenBody any
	switch capability {
	case CapabilityGeminiGenerateContent, CapabilityGeminiStreamGenerate:
		if strings.TrimSpace(projectID) == "" {
			return "", nil, fmt.Errorf("antigravity oauth credential is missing project_id metadata")
		}
		rewrittenBody = buildAntigravityOAuthGenerateEnvelope(root, modelName, projectID)
	case CapabilityGeminiCountTokens:
		rewrittenBody = buildAntigravityOAuthCountTokensEnvelope(root, modelName)
	default:
		return "", nil, fmt.Errorf("antigravity oauth does not support %s requests", capability)
	}

	rewritten, err := json.Marshal(rewrittenBody)
	if err != nil {
		return "", nil, fmt.Errorf("marshal antigravity oauth request: %w", err)
	}

	switch capability {
	case CapabilityGeminiGenerateContent:
		return antigravityOAuthGeneratePath, rewritten, nil
	case CapabilityGeminiStreamGenerate:
		return antigravityOAuthStreamPath, rewritten, nil
	case CapabilityGeminiCountTokens:
		return antigravityOAuthCountTokensPath, rewritten, nil
	default:
		return "", nil, fmt.Errorf("antigravity oauth does not support %s requests", capability)
	}
}

func buildAntigravityOAuthGenerateEnvelope(root map[string]any, modelName string, projectID string) map[string]any {
	envelope := map[string]any{
		"model":   strings.TrimSpace(modelName),
		"project": strings.TrimSpace(projectID),
		"request": cloneAntigravityOAuthRequestPayload(root),
	}
	if requestID, ok := antigravityOAuthStringField(root, "requestId", "request_id"); ok {
		envelope["requestId"] = requestID
	}
	if userPromptID, ok := antigravityOAuthStringField(root, "userPromptId", "user_prompt_id"); ok {
		envelope["userPromptId"] = userPromptID
	}
	if requestType, ok := antigravityOAuthField(root, "requestType", "request_type"); ok {
		envelope["requestType"] = requestType
	}
	if enabledCreditTypes, ok := antigravityOAuthField(root, "enabledCreditTypes", "enabled_credit_types"); ok {
		envelope["enabledCreditTypes"] = enabledCreditTypes
	}
	return envelope
}

func cloneAntigravityOAuthRequestPayload(root map[string]any) map[string]any {
	request := cloneStringAnyMap(root)
	canonicalAntigravityField(request, "systemInstruction", "system_instruction")
	canonicalAntigravityField(request, "generationConfig", "generation_config")
	if generation, ok := request["generationConfig"].(map[string]any); ok {
		generation = cloneStringAnyMap(generation)
		request["generationConfig"] = generation
		canonicalAntigravityField(generation, "thinkingConfig", "thinking_config")
		canonicalAntigravityField(generation, "responseSchema", "responseJsonSchema", "response_json_schema")
		if thinking, ok := generation["thinkingConfig"].(map[string]any); ok {
			thinking = cloneStringAnyMap(thinking)
			generation["thinkingConfig"] = thinking
			canonicalAntigravityField(thinking, "thinkingLevel", "thinking_level")
			canonicalAntigravityField(thinking, "thinkingBudget", "thinking_budget")
			canonicalAntigravityField(thinking, "includeThoughts", "include_thoughts")
			if level, ok := thinking["thinkingLevel"].(string); ok {
				thinking["thinkingLevel"] = strings.ToLower(strings.TrimSpace(level))
			}
		}
	}
	delete(request, "model")
	delete(request, "project")
	delete(request, "request_id")
	delete(request, "requestId")
	delete(request, "user_prompt_id")
	delete(request, "userPromptId")
	delete(request, "request_type")
	delete(request, "requestType")
	delete(request, "enabled_credit_types")
	delete(request, "enabledCreditTypes")
	return request
}

// Canonical spelling wins if both Gemini SDK field spellings are present.
func canonicalAntigravityField(fields map[string]any, canonical string, aliases ...string) {
	for _, alias := range aliases {
		if value, ok := fields[alias]; ok {
			if _, exists := fields[canonical]; !exists {
				fields[canonical] = value
			}
			delete(fields, alias)
		}
	}
}

func antigravityOAuthStringField(root map[string]any, keys ...string) (string, bool) {
	value, ok := antigravityOAuthField(root, keys...)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func antigravityOAuthField(root map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := root[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func buildAntigravityOAuthCountTokensEnvelope(root map[string]any, modelName string) map[string]any {
	request := cloneAntigravityOAuthRequestPayload(root)
	request["model"] = fmt.Sprintf("models/%s", strings.TrimSpace(modelName))
	return map[string]any{
		"request": request,
	}
}

func buildAntigravityOAuthModelsEnvelope(projectID string) []byte {
	root := map[string]any{}
	if projectID = strings.TrimSpace(projectID); projectID != "" {
		root["project"] = projectID
	}
	out, _ := json.Marshal(root)
	return out
}

func applyAntigravityOAuthHeaderDefaults(proxyReq *http.Request, capability RequestCapability, accessToken string) {
	if proxyReq == nil {
		return
	}
	clearAntigravityOAuthUpstreamContextHeaders(proxyReq.Header)
	proxyReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	proxyReq.Header.Set("User-Agent", antigravityOAuthUserAgent())
	proxyReq.Header.Set("Content-Type", "application/json")

	if capability == CapabilityGeminiStreamGenerate {
		proxyReq.Header.Set("Accept", "text/event-stream")
		query := proxyReq.URL.Query()
		query.Set("alt", "sse")
		proxyReq.URL.RawQuery = query.Encode()
		return
	}
	proxyReq.Header.Set("Accept", "application/json")
}

func clearAntigravityOAuthUpstreamContextHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	for key := range headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		switch {
		case lower == "cookie",
			lower == "cookie2",
			lower == "proxy-authorization",
			lower == "x-goog-api-client",
			lower == "client-metadata",
			lower == "x-goog-user-project",
			lower == "x-goog-request-reason",
			lower == "x-goog-request-params",
			lower == "x-goog-authuser",
			lower == "x-cloud-trace-context",
			strings.HasPrefix(lower, "x-antigravity-"),
			strings.HasPrefix(lower, "x-cloud-code-"):
			headers.Del(key)
		}
	}
}

func antigravityOAuthUserAgent() string {
	return fmt.Sprintf("antigravity/cli/%s (aidev_client; os_type=%s; arch=%s)", antigravityOAuthVersionLabel(), geminiOAuthOS(), runtime.GOARCH)
}

func antigravityOAuthVersionLabel() string {
	if value := strings.TrimSpace(os.Getenv("CLIPAL_OAUTH_ANTIGRAVITY_VERSION")); value != "" {
		return value
	}
	antigravityOAuthVersionOnce.Do(func() {
		antigravityOAuthVersion = antigravityOAuthFallbackVersion
		if data, err := os.ReadFile(antigravityOAuthInfoPlist); err == nil {
			if match := antigravityOAuthPlistRE.FindSubmatch(data); len(match) == 2 {
				if value := strings.TrimSpace(string(match[1])); value != "" {
					antigravityOAuthVersion = value
				}
			}
		}
	})
	return antigravityOAuthVersion
}
