package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func prepareAntigravityOAuthResponse(original *http.Request, resp *http.Response) (*http.Response, error) {
	requestCtx, ok := requestContextFromRequest(original)
	if !ok {
		return resp, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, nil
	}

	switch requestCtx.Capability {
	case CapabilityGeminiGenerateContent:
		if !geminiOAuthResponseShouldRewrite(resp, "application/json") {
			return resp, nil
		}
		return rewriteGeminiOAuthJSONResponse(resp)
	case CapabilityGeminiStreamGenerate:
		if !geminiOAuthResponseShouldRewrite(resp, "text/event-stream") {
			return resp, nil
		}
		return rewriteGeminiOAuthStreamResponse(resp), nil
	case CapabilityGeminiModels:
		if !geminiOAuthResponseShouldRewrite(resp, "application/json") {
			return resp, nil
		}
		return rewriteAntigravityOAuthModelsResponse(resp)
	default:
		return resp, nil
	}
}

func rewriteAntigravityOAuthModelsResponse(resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	rewritten, ok := normalizeAntigravityOAuthModelsPayload(body)
	if !ok {
		setGeminiOAuthResponseBody(resp, body)
		return resp, nil
	}
	setGeminiOAuthResponseBody(resp, rewritten)
	return resp, nil
}

func normalizeAntigravityOAuthModelsPayload(body []byte) ([]byte, bool) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false
	}
	if _, ok := root["error"]; ok {
		return body, false
	}
	modelsValue, ok := root["models"]
	if !ok {
		return body, false
	}
	models := make([]any, 0)
	switch typed := modelsValue.(type) {
	case []any:
		for _, item := range typed {
			if model, ok := normalizeAntigravityOAuthModel("", item); ok {
				models = append(models, model)
			}
		}
	case map[string]any:
		for id, item := range typed {
			if model, ok := normalizeAntigravityOAuthModel(id, item); ok {
				models = append(models, model)
			}
		}
	default:
		return body, false
	}
	out := map[string]any{"models": models}
	rewritten, err := json.Marshal(out)
	if err != nil {
		return body, false
	}
	return rewritten, true
}

func normalizeAntigravityOAuthModel(id string, raw any) (map[string]any, bool) {
	id = strings.TrimSpace(id)
	model := map[string]any{}
	if typed, ok := raw.(map[string]any); ok && typed != nil {
		for k, v := range typed {
			model[k] = v
		}
		if value, ok := geminiOAuthStringValue(typed["name"]); ok && id == "" {
			id = strings.TrimPrefix(value, "models/")
		}
		if value, ok := geminiOAuthStringValue(typed["id"]); ok && id == "" {
			id = value
		}
	}
	if id == "" {
		return nil, false
	}
	name := "models/" + strings.TrimPrefix(id, "models/")
	model["name"] = name
	model["version"] = id
	if displayName, ok := geminiOAuthStringValue(model["displayName"]); ok {
		model["displayName"] = displayName
	} else {
		model["displayName"] = id
	}
	if description, ok := geminiOAuthStringValue(model["description"]); !ok || description == "" {
		model["description"] = model["displayName"]
	}
	if _, ok := model["supportedGenerationMethods"]; !ok {
		model["supportedGenerationMethods"] = []any{"generateContent", "streamGenerateContent", "countTokens"}
	}
	return model, true
}
