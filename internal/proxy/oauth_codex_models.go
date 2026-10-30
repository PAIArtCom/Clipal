package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// codexOAuthPublicModelFields is the public capability subset of the Codex
// ModelInfo schema. Keep this projection explicit: ModelInfo also contains
// model prompts, approval policies, entitlement details, and internal control
// flags that must not be exposed through the OpenAI-compatible endpoint.
var codexOAuthPublicModelFields = []string{
	"slug",
	"display_name",
	"description",
	"default_reasoning_level",
	"supported_reasoning_levels",
	"shell_type",
	"visibility",
	"supported_in_api",
	"priority",
	"additional_speed_tiers",
	"service_tiers",
	"default_service_tier",
	"availability_nux",
	"upgrade",
	"include_skills_usage_instructions",
	"include_plugin_usage_instructions",
	"include_apps_usage_instructions",
	"supports_reasoning_summary_parameter",
	"default_reasoning_summary",
	"support_verbosity",
	"default_verbosity",
	"apply_patch_tool_type",
	"web_search_tool_type",
	"truncation_policy",
	"supports_image_detail_original",
	"context_window",
	"max_context_window",
	"auto_compact_token_limit",
	"effective_context_window_percent",
	"experimental_supported_tools",
	"input_modalities",
	"supports_search_tool",
	"supports_experimental_context",
	"use_responses_lite",
	"supports_reasoning_effort_updates",
	"model_specialty",
	"tool_mode",
	"multi_agent_version",
	"multi_agent_reasoning_effort",
}

type codexOAuthDownstreamClient int

const (
	codexOAuthDownstreamOpenAI codexOAuthDownstreamClient = iota
	codexOAuthDownstreamCLI
)

// prepareCodexOAuthResponse adapts Codex-specific upstream payloads to the
// public OpenAI wire format. Codex OAuth accounts only expose model metadata
// through /backend-api/codex/models, which uses the Codex catalog schema
// instead of the OpenAI {"object":"list","data":[...]} envelope.
//
// Native Codex clients speak that schema themselves (GET <base_url>/models
// returns {"models":[...]}), so their responses are passed through untouched.
// OpenAI-compatible clients receive the standard list envelope, with the
// public Codex ModelInfo capability fields retained.
func prepareCodexOAuthResponse(downstreamReq *http.Request, upstreamResp *http.Response) (*http.Response, error) {
	requestCtx, ok := requestContextFromRequest(downstreamReq)
	if !ok || requestCtx.Capability != CapabilityOpenAIModels {
		return upstreamResp, nil
	}
	if codexOAuthDownstreamClientForRequest(downstreamReq) == codexOAuthDownstreamCLI {
		return upstreamResp, nil
	}
	if upstreamResp == nil || upstreamResp.StatusCode < http.StatusOK || upstreamResp.StatusCode >= http.StatusMultipleChoices {
		return upstreamResp, nil
	}
	if !codexOAuthResponseIsJSON(upstreamResp) {
		return upstreamResp, nil
	}
	return rewriteCodexOAuthModelsResponse(requestCtx, upstreamResp)
}

// codexOAuthDownstreamClientForRequest classifies the client that will
// receive the response. The request sent upstream has Codex CLI identity
// headers added by Clipal, so response adaptation must never inspect that
// rewritten request to choose the downstream schema.
func codexOAuthDownstreamClientForRequest(downstreamReq *http.Request) codexOAuthDownstreamClient {
	if isCodexNativeClient(downstreamReq) {
		return codexOAuthDownstreamCLI
	}
	return codexOAuthDownstreamOpenAI
}

// isCodexNativeClient reports whether the downstream request comes from a
// first-party Codex client. Those clients parse the Codex catalog schema
// ({"models":[...]}) and must not receive the OpenAI list envelope.
func isCodexNativeClient(req *http.Request) bool {
	if req == nil {
		return false
	}
	if isCodexNativeModelsRequest(req) {
		return true
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

// isCodexNativeModelsRequest recognizes the endpoint shape used by the
// official Codex ModelsClient. The path is intentionally checked on the
// downstream request because the router stores only the canonical /v1/models
// path in RequestContext, while the original URL still tells us whether the
// client requested /models?client_version=....
func isCodexNativeModelsRequest(req *http.Request) bool {
	if req == nil || req.URL == nil || req.Method != http.MethodGet {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(req.URL.Path), "/")
	if path == "" || !strings.HasSuffix(path, "/models") {
		return false
	}
	if strings.HasSuffix(path, "/v1/models") || strings.HasSuffix(path, "/v1beta/models") {
		return false
	}
	return strings.TrimSpace(req.URL.Query().Get("client_version")) != ""
}

// applyCodexNativeModelsHeaders asks compatible API-key gateways such as
// Orbit for the official Codex catalog when the downstream client requested
// the native /models endpoint. Authentication remains owned by the provider;
// these headers only select the catalog representation.
func applyCodexNativeModelsHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Originator", codexOAuthOriginator)
	req.Header.Set("User-Agent", codexOAuthUserAgent)
	req.Header.Set("Version", codexOAuthVersion)
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
		// Preserve the public capability metadata from the official ModelInfo
		// schema, including service tiers and reasoning options, without exposing
		// prompts, approval policies, entitlements, or internal control fields.
		model := make(map[string]any, len(codexOAuthPublicModelFields)+4)
		for _, key := range codexOAuthPublicModelFields {
			if value, ok := raw[key]; ok {
				model[key] = value
			}
		}
		model["id"] = id
		model["object"] = "model"
		model["created"] = 0
		model["owned_by"] = "codex"
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
