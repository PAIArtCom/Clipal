package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// prepareCodexOAuthResponse adapts Codex-specific upstream payloads to the
// public OpenAI wire format. Codex OAuth accounts only expose model metadata
// through /backend-api/codex/models, which uses the Codex catalog schema
// instead of the OpenAI {"object":"list","data":[...]} envelope.
//
// Native Codex clients speak that schema themselves (GET <base_url>/models
// returns {"models":[...]}), so their responses are passed through untouched.
func prepareCodexOAuthResponse(original *http.Request, resp *http.Response) (*http.Response, error) {
	requestCtx, ok := requestContextFromRequest(original)
	if !ok || requestCtx.Capability != CapabilityOpenAIModels {
		return resp, nil
	}
	if isCodexNativeClient(original) {
		return resp, nil
	}
	if resp == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, nil
	}
	if !codexOAuthResponseIsJSON(resp) {
		return resp, nil
	}
	return rewriteCodexOAuthModelsResponse(requestCtx, resp)
}

// isCodexNativeClient reports whether the request comes from a first-party
// Codex client. Those clients parse the Codex catalog schema
// ({"models":[...]}) and must not receive the OpenAI list envelope.
func isCodexNativeClient(req *http.Request) bool {
	if req == nil {
		return false
	}
	if isCodexOriginator(req.Header.Get("Originator")) {
		return true
	}
	userAgent := strings.ToLower(strings.TrimSpace(req.Header.Get("User-Agent")))
	for _, prefix := range []string{"codex_cli_rs/", "codex_vscode/", "codex-tui/"} {
		if strings.HasPrefix(userAgent, prefix) {
			return true
		}
	}
	return false
}

func isCodexOriginator(value string) bool {
	originator := strings.ToLower(strings.TrimSpace(value))
	if originator == "" {
		return false
	}
	switch originator {
	case "codex_cli_rs", "codex_vscode", "codex-tui":
		return true
	}
	return strings.HasPrefix(originator, "codex_") ||
		strings.HasPrefix(originator, "codex-") ||
		strings.HasPrefix(originator, "codex ")
}

func codexOAuthResponseIsJSON(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	return contentType == "" || strings.Contains(contentType, "application/json")
}

func rewriteCodexOAuthModelsResponse(requestCtx RequestContext, resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil {
		return resp, nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}

	models, ok := codexOAuthModelsFromPayload(body)
	if !ok {
		setCodexOAuthResponseBody(resp, body)
		return resp, nil
	}

	if requested := requestedCodexOAuthModelID(requestCtx); requested != "" {
		for _, item := range models {
			model, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := model["id"].(string); id == requested {
				setCodexOAuthJSONResponse(resp, http.StatusOK, model)
				return resp, nil
			}
		}
		setCodexOAuthJSONResponse(resp, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"message": "The model '" + requested + "' does not exist",
				"type":    "invalid_request_error",
				"param":   nil,
				"code":    "model_not_found",
			},
		})
		return resp, nil
	}

	setCodexOAuthJSONResponse(resp, http.StatusOK, map[string]any{
		"object": "list",
		"data":   models,
	})
	return resp, nil
}

func codexOAuthModelsFromPayload(body []byte) ([]any, bool) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, false
	}
	rawModels, ok := root["models"].([]any)
	if !ok {
		return nil, false
	}

	models := make([]any, 0, len(rawModels))
	for _, item := range rawModels {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := codexOAuthStringField(raw, "slug")
		if id == "" {
			id = codexOAuthStringField(raw, "id")
		}
		if id == "" {
			continue
		}
		model := map[string]any{
			"id":       id,
			"object":   "model",
			"created":  0,
			"owned_by": "codex",
		}
		for _, key := range []string{"display_name", "description", "context_window", "max_context_window"} {
			if value, ok := raw[key]; ok && value != nil {
				model[key] = value
			}
		}
		models = append(models, model)
	}
	return models, true
}

func codexOAuthStringField(root map[string]any, key string) string {
	value, _ := root[key].(string)
	return strings.TrimSpace(value)
}

func requestedCodexOAuthModelID(requestCtx RequestContext) string {
	path := normalizeUpstreamPath(requestCtx.UpstreamPath)
	const prefix = "/v1/models/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func setCodexOAuthJSONResponse(resp *http.Response, status int, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	resp.StatusCode = status
	resp.Status = strconv.Itoa(status) + " " + http.StatusText(status)
	setCodexOAuthResponseBody(resp, data)
	return true
}

func setCodexOAuthResponseBody(resp *http.Response, body []byte) {
	if resp == nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Del("Content-Encoding")
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
}
