package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

const (
	defaultGeminiOAuthBaseURL  = "https://cloudcode-pa.googleapis.com"
	geminiOAuthVersion         = "0.45.0-nightly.20260521.g854f811be"
	geminiOAuthProjectMetadata = "project_id"
	geminiOAuthGeneratePath    = "/v1internal:generateContent"
	geminiOAuthStreamPath      = "/v1internal:streamGenerateContent"
	geminiOAuthCountTokensPath = "/v1internal:countTokens"
)

func (cp *ClientProxy) createGeminiOAuthRequestWithPayloadForProvider(original *http.Request, provider config.Provider, providerIndex int, path string, payload *requestPayload) (*http.Request, error) {
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

	targetPath, modelName, rewrittenBody, err := payload.geminiOAuthRequest(original, requestCtx, provider, path, projectID)
	if err != nil {
		return nil, err
	}

	targetURL, err := buildTargetURL(defaultGeminiOAuthBaseURL, targetPath, "")
	if err != nil {
		return nil, err
	}

	proxyReq, err := http.NewRequestWithContext(original.Context(), original.Method, targetURL, bytes.NewReader(rewrittenBody))
	if err != nil {
		return nil, err
	}
	copyHeaderAllowingApplicationAuth(proxyReq.Header, original.Header)
	addForwardedHeaders(proxyReq, original)
	clearAuthCarriers(proxyReq)
	applyGeminiOAuthHeaders(proxyReq, modelName, requestCtx.Capability, accessToken)
	proxyReq.ContentLength = int64(len(rewrittenBody))
	proxyReq.Header.Del("Content-Length")
	return proxyReq, nil
}

func buildGeminiOAuthRequest(capability RequestCapability, path string, body []byte, projectID string) (string, string, []byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", "", nil, fmt.Errorf("gemini oauth request body is required")
	}

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", "", nil, fmt.Errorf("gemini oauth request body must be valid json: %w", err)
	}
	return buildGeminiOAuthRequestFromRoot(capability, path, root, projectID)
}

func buildGeminiOAuthRequestFromRoot(capability RequestCapability, path string, root map[string]any, projectID string) (string, string, []byte, error) {
	if root == nil {
		return "", "", nil, fmt.Errorf("gemini oauth request body must be a json object")
	}
	modelName, err := geminiModelFromPath(path)
	if err != nil {
		return "", "", nil, err
	}

	var rewrittenBody any
	switch capability {
	case CapabilityGeminiGenerateContent:
		if strings.TrimSpace(projectID) == "" {
			return "", "", nil, fmt.Errorf("gemini oauth credential is missing project_id metadata")
		}
		rewrittenBody = buildGeminiOAuthGenerateEnvelope(root, modelName, projectID)
	case CapabilityGeminiStreamGenerate:
		if strings.TrimSpace(projectID) == "" {
			return "", "", nil, fmt.Errorf("gemini oauth credential is missing project_id metadata")
		}
		rewrittenBody = buildGeminiOAuthGenerateEnvelope(root, modelName, projectID)
	case CapabilityGeminiCountTokens:
		rewrittenBody = buildGeminiOAuthCountTokensEnvelope(root, modelName)
	default:
		return "", "", nil, fmt.Errorf("gemini oauth does not support %s requests", capability)
	}

	rewritten, err := json.Marshal(rewrittenBody)
	if err != nil {
		return "", "", nil, fmt.Errorf("marshal gemini oauth request: %w", err)
	}

	switch capability {
	case CapabilityGeminiGenerateContent:
		return geminiOAuthGeneratePath, modelName, rewritten, nil
	case CapabilityGeminiStreamGenerate:
		return geminiOAuthStreamPath, modelName, rewritten, nil
	case CapabilityGeminiCountTokens:
		return geminiOAuthCountTokensPath, modelName, rewritten, nil
	default:
		return "", "", nil, fmt.Errorf("gemini oauth does not support %s requests", capability)
	}
}

func buildGeminiOAuthGenerateEnvelope(root map[string]any, modelName string, projectID string) map[string]any {
	envelope := map[string]any{
		"model":   strings.TrimSpace(modelName),
		"project": strings.TrimSpace(projectID),
		"request": cloneGeminiOAuthRequestPayload(root),
	}
	if userPromptID, ok := geminiOAuthStringField(root, "user_prompt_id"); ok {
		envelope["user_prompt_id"] = userPromptID
	}
	if enabledCreditTypes, ok := root["enabled_credit_types"]; ok {
		envelope["enabled_credit_types"] = enabledCreditTypes
	}
	return envelope
}

func cloneGeminiOAuthRequestPayload(root map[string]any) map[string]any {
	request := make(map[string]any, len(root))
	for key, value := range root {
		request[key] = value
	}
	delete(request, "model")
	delete(request, "project")
	delete(request, "user_prompt_id")
	delete(request, "enabled_credit_types")
	return request
}

func buildGeminiOAuthCountTokensEnvelope(root map[string]any, modelName string) map[string]any {
	request := make(map[string]any, 2)
	request["model"] = fmt.Sprintf("models/%s", strings.TrimSpace(modelName))
	if contents, ok := root["contents"]; ok {
		request["contents"] = contents
	}
	return map[string]any{
		"request": request,
	}
}

func geminiOAuthStringField(root map[string]any, key string) (string, bool) {
	if root == nil {
		return "", false
	}
	value, ok := root[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return text, true
}

func geminiModelFromPath(path string) (string, error) {
	path = normalizeUpstreamPath(path)
	modelPrefix := ""
	switch {
	case strings.HasPrefix(path, "/v1beta/models/"):
		modelPrefix = "/v1beta/models/"
	case strings.HasPrefix(path, "/v1/models/"):
		modelPrefix = "/v1/models/"
	default:
		return "", fmt.Errorf("gemini oauth does not support path %q", path)
	}

	rest := strings.TrimPrefix(path, modelPrefix)
	modelName, _, ok := strings.Cut(rest, ":")
	modelName = strings.TrimSpace(strings.TrimSuffix(modelName, "/"))
	if !ok || modelName == "" {
		return "", fmt.Errorf("gemini oauth does not support path %q", path)
	}
	return modelName, nil
}

func applyGeminiOAuthHeaders(proxyReq *http.Request, modelName string, capability RequestCapability, accessToken string) {
	if proxyReq == nil {
		return
	}
	proxyReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	proxyReq.Header.Set("User-Agent", geminiOAuthUserAgent(modelName))
	proxyReq.Header.Del("X-Goog-Api-Client")
	proxyReq.Header.Del("Client-Metadata")
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

func geminiOAuthUserAgent(modelName string) string {
	if strings.TrimSpace(modelName) == "" {
		modelName = "unknown"
	}
	return fmt.Sprintf("GeminiCLI/%s/%s (%s; %s; terminal)", geminiOAuthVersion, modelName, geminiOAuthOS(), geminiOAuthArch())
}

func geminiOAuthOS() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func geminiOAuthArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	default:
		return runtime.GOARCH
	}
}
