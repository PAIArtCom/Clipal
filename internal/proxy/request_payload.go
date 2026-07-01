package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

type requestPayload struct {
	body             []byte
	rootParsed       bool
	root             map[string]any
	overrideCache    map[string][]byte
	codexCache       map[string]codexOAuthPreparedRequest
	geminiCache      map[string]geminiOAuthPreparedRequest
	antigravityCache map[string]antigravityOAuthPreparedRequest
}

type codexOAuthPreparedRequest struct {
	targetPath string
	stream     bool
	body       []byte
	err        error
}

type geminiOAuthPreparedRequest struct {
	targetPath string
	modelName  string
	body       []byte
	err        error
}

type antigravityOAuthPreparedRequest struct {
	targetPath string
	body       []byte
	err        error
}

func newRequestPayload(body []byte) *requestPayload {
	return &requestPayload{body: body}
}

func (p *requestPayload) Body() []byte {
	if p == nil {
		return nil
	}
	return p.body
}

func (p *requestPayload) jsonRoot() map[string]any {
	if p == nil || len(p.body) == 0 {
		return nil
	}
	if p.rootParsed {
		return p.root
	}
	p.rootParsed = true
	var root map[string]any
	if err := json.Unmarshal(p.body, &root); err != nil {
		return nil
	}
	p.root = root
	return p.root
}

func (p *requestPayload) requestStickyKey(requestCtx RequestContext) stickyKey {
	return extractRequestStickyKeyFromRoot(requestCtx, p.jsonRoot())
}

func (p *requestPayload) responseLearningStickyKey(requestCtx RequestContext) stickyKey {
	return extractResponseLearningStickyKeyFromRoot(requestCtx, p.jsonRoot())
}

func (p *requestPayload) providerBody(original *http.Request, requestCtx RequestContext, provider config.Provider) []byte {
	if p == nil {
		return nil
	}
	if len(p.body) == 0 || !hasProviderRequestOverrides(provider) || !isJSONRequest(original) {
		return p.body
	}

	key := providerOverrideCacheKey(requestCtx, provider)
	if p.overrideCache != nil {
		if cached, ok := p.overrideCache[key]; ok {
			return cached
		}
	}

	root := p.jsonRoot()
	if root == nil {
		return p.body
	}
	rewrittenRoot := cloneJSONRootForRequestOverrides(root)
	if !applyProviderRequestOverridesToRoot(rewrittenRoot, requestCtx, provider) {
		return p.body
	}

	rewritten, err := json.Marshal(rewrittenRoot)
	if err != nil {
		return p.body
	}
	if p.overrideCache == nil {
		p.overrideCache = make(map[string][]byte)
	}
	p.overrideCache[key] = rewritten
	return rewritten
}

func (p *requestPayload) providerRoot(original *http.Request, requestCtx RequestContext, provider config.Provider) (map[string]any, bool) {
	if p == nil || len(p.body) == 0 || !isJSONRequest(original) {
		return nil, false
	}
	root := p.jsonRoot()
	if root == nil {
		return nil, false
	}
	rewrittenRoot := cloneJSONRootForRequestOverrides(root)
	_ = applyProviderRequestOverridesToRoot(rewrittenRoot, requestCtx, provider)
	return rewrittenRoot, true
}

func (p *requestPayload) codexOAuthRequest(original *http.Request, requestCtx RequestContext, provider config.Provider, path string) (string, bool, []byte, error) {
	if p == nil {
		return buildCodexOAuthRequest(path, nil)
	}
	key := strings.Join([]string{"codex", normalizeUpstreamPath(path), providerOverrideCacheKey(requestCtx, provider)}, "\x00")
	if p.codexCache != nil {
		if cached, ok := p.codexCache[key]; ok {
			return cached.targetPath, cached.stream, cached.body, cached.err
		}
	}

	body := p.providerBody(original, requestCtx, provider)
	targetPath, stream, requestBody, err := buildCodexOAuthRequest(path, body)
	if root, ok := p.providerRoot(original, requestCtx, provider); ok {
		targetPath, stream, requestBody, err = buildCodexOAuthRequestFromRoot(path, root, body)
	}
	if p.codexCache == nil {
		p.codexCache = make(map[string]codexOAuthPreparedRequest)
	}
	p.codexCache[key] = codexOAuthPreparedRequest{
		targetPath: targetPath,
		stream:     stream,
		body:       requestBody,
		err:        err,
	}
	return targetPath, stream, requestBody, err
}

func (p *requestPayload) geminiOAuthRequest(original *http.Request, requestCtx RequestContext, provider config.Provider, path string, projectID string) (string, string, []byte, error) {
	if p == nil {
		return buildGeminiOAuthRequest(requestCtx.Capability, path, nil, projectID)
	}
	key := strings.Join([]string{
		"gemini",
		string(requestCtx.Capability),
		normalizeUpstreamPath(path),
		strings.TrimSpace(projectID),
		providerOverrideCacheKey(requestCtx, provider),
	}, "\x00")
	if p.geminiCache != nil {
		if cached, ok := p.geminiCache[key]; ok {
			return cached.targetPath, cached.modelName, cached.body, cached.err
		}
	}

	body := p.providerBody(original, requestCtx, provider)
	targetPath, modelName, requestBody, err := buildGeminiOAuthRequest(requestCtx.Capability, path, body, projectID)
	if root, ok := p.providerRoot(original, requestCtx, provider); ok {
		targetPath, modelName, requestBody, err = buildGeminiOAuthRequestFromRoot(requestCtx.Capability, path, root, projectID)
	}
	if p.geminiCache == nil {
		p.geminiCache = make(map[string]geminiOAuthPreparedRequest)
	}
	p.geminiCache[key] = geminiOAuthPreparedRequest{
		targetPath: targetPath,
		modelName:  modelName,
		body:       requestBody,
		err:        err,
	}
	return targetPath, modelName, requestBody, err
}

func (p *requestPayload) antigravityOAuthRequest(original *http.Request, requestCtx RequestContext, provider config.Provider, path string, projectID string) (string, []byte, error) {
	if p == nil {
		return buildAntigravityOAuthRequest(requestCtx.Capability, path, nil, projectID)
	}
	key := strings.Join([]string{
		"antigravity",
		string(requestCtx.Capability),
		normalizeUpstreamPath(path),
		strings.TrimSpace(projectID),
		providerOverrideCacheKey(requestCtx, provider),
	}, "\x00")
	if p.antigravityCache != nil {
		if cached, ok := p.antigravityCache[key]; ok {
			return cached.targetPath, cached.body, cached.err
		}
	}

	body := p.providerBody(original, requestCtx, provider)
	targetPath, requestBody, err := buildAntigravityOAuthRequest(requestCtx.Capability, path, body, projectID)
	if root, ok := p.providerRoot(original, requestCtx, provider); ok {
		targetPath, requestBody, err = buildAntigravityOAuthRequestFromRoot(requestCtx.Capability, path, root, projectID)
	}
	if p.antigravityCache == nil {
		p.antigravityCache = make(map[string]antigravityOAuthPreparedRequest)
	}
	p.antigravityCache[key] = antigravityOAuthPreparedRequest{
		targetPath: targetPath,
		body:       requestBody,
		err:        err,
	}
	return targetPath, requestBody, err
}

func providerOverrideCacheKey(requestCtx RequestContext, provider config.Provider) string {
	return strings.Join([]string{
		string(requestCtx.Family),
		string(requestCtx.Capability),
		provider.Name,
		provider.ModelOverride(),
		provider.OpenAIReasoningEffort(),
		provider.ClaudeEffort(),
		fmt.Sprintf("%d", provider.ClaudeThinkingBudgetTokens()),
	}, "\x00")
}

func cloneJSONRootForRequestOverrides(root map[string]any) map[string]any {
	if root == nil {
		return nil
	}
	out := make(map[string]any, len(root))
	for key, value := range root {
		out[key] = value
	}
	if reasoning, ok := out["reasoning"].(map[string]any); ok {
		out["reasoning"] = cloneStringAnyMap(reasoning)
	}
	if outputConfig, ok := out["output_config"].(map[string]any); ok {
		out["output_config"] = cloneStringAnyMap(outputConfig)
	}
	return out
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
