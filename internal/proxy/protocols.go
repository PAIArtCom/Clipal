package proxy

import (
	"context"
	"net/http"
	"strings"
)

type ProtocolFamily string

const (
	ProtocolFamilyClaude ProtocolFamily = "claude"
	ProtocolFamilyOpenAI ProtocolFamily = "openai"
	ProtocolFamilyGemini ProtocolFamily = "gemini"
)

type RequestCapability string

const (
	CapabilityClaudeCompatible         RequestCapability = "claude_compatible"
	CapabilityClaudeMessages           RequestCapability = "claude_messages"
	CapabilityClaudeCountTokens        RequestCapability = "claude_count_tokens"
	CapabilityOpenAICompatible         RequestCapability = "openai_compatible"
	CapabilityOpenAIChatCompletions    RequestCapability = "openai_chat_completions"
	CapabilityOpenAICompletions        RequestCapability = "openai_completions"
	CapabilityOpenAIResponses          RequestCapability = "openai_responses"
	CapabilityOpenAIEmbeddings         RequestCapability = "openai_embeddings"
	CapabilityOpenAIModerations        RequestCapability = "openai_moderations"
	CapabilityOpenAIAudio              RequestCapability = "openai_audio"
	CapabilityOpenAIImages             RequestCapability = "openai_images"
	CapabilityOpenAIFiles              RequestCapability = "openai_files"
	CapabilityOpenAIUploads            RequestCapability = "openai_uploads"
	CapabilityOpenAIModels             RequestCapability = "openai_models"
	CapabilityOpenAIFineTuning         RequestCapability = "openai_fine_tuning"
	CapabilityOpenAIBatches            RequestCapability = "openai_batches"
	CapabilityOpenAIVectorStores       RequestCapability = "openai_vector_stores"
	CapabilityOpenAIAssistants         RequestCapability = "openai_assistants"
	CapabilityOpenAIThreads            RequestCapability = "openai_threads"
	CapabilityOpenAIRealtime           RequestCapability = "openai_realtime"
	CapabilityGeminiCompatible         RequestCapability = "gemini_compatible"
	CapabilityGeminiGenerateContent    RequestCapability = "gemini_generate_content"
	CapabilityGeminiStreamGenerate     RequestCapability = "gemini_stream_generate_content"
	CapabilityGeminiCountTokens        RequestCapability = "gemini_count_tokens"
	CapabilityGeminiEmbedContent       RequestCapability = "gemini_embed_content"
	CapabilityGeminiBatchEmbedContents RequestCapability = "gemini_batch_embed_contents"
	CapabilityGeminiPredict            RequestCapability = "gemini_predict"
	CapabilityGeminiPredictLongRunning RequestCapability = "gemini_predict_long_running"
	CapabilityGeminiModels             RequestCapability = "gemini_models"
	CapabilityGeminiFiles              RequestCapability = "gemini_files"
	CapabilityGeminiUploadFiles        RequestCapability = "gemini_upload_files"
	CapabilityGeminiCachedContents     RequestCapability = "gemini_cached_contents"
	CapabilityGeminiTunedModels        RequestCapability = "gemini_tuned_models"
	CapabilityGeminiInteractions       RequestCapability = "gemini_interactions"
	CapabilityGeminiBatches            RequestCapability = "gemini_batches"
	CapabilityGeminiOperations         RequestCapability = "gemini_operations"
	CapabilityGeminiFileSearchStores   RequestCapability = "gemini_file_search_stores"
	CapabilityGeminiGeneratedFiles     RequestCapability = "gemini_generated_files"
	CapabilityGeminiCorpora            RequestCapability = "gemini_corpora"
	CapabilityGeminiAuthTokens         RequestCapability = "gemini_auth_tokens"
	CapabilityGeminiAgents             RequestCapability = "gemini_agents"
	CapabilityGeminiWebhooks           RequestCapability = "gemini_webhooks"
)

type RequestContext struct {
	ClientType     ClientType
	Family         ProtocolFamily
	Capability     RequestCapability
	UpstreamPath   string
	UnifiedIngress bool
}

type requestContextKey struct{}

type routingScope string

const (
	routingScopeDefault           routingScope = "default"
	routingScopeClaudeCountTokens routingScope = "claude_count_tokens"
	routingScopeOpenAIResponses   routingScope = "openai_responses"
	routingScopeGeminiStream      routingScope = "gemini_stream_generate_content"
)

var versionedPathRoots = []string{
	"/upload/v1beta",
	"/upload/v1",
	"/v1beta",
	"/v1",
}

func detectClipalRequestContext(path string) (RequestContext, bool) {
	path = canonicalizeClipalPath(path)

	switch capability := detectClaudeCapability(path); capability {
	case "":
	default:
		return RequestContext{
			ClientType:     ClientClaude,
			Family:         ProtocolFamilyClaude,
			Capability:     capability,
			UpstreamPath:   path,
			UnifiedIngress: true,
		}, true
	}

	switch capability := detectGeminiCapability(path); capability {
	case "":
	default:
		return RequestContext{
			ClientType:     ClientGemini,
			Family:         ProtocolFamilyGemini,
			Capability:     capability,
			UpstreamPath:   path,
			UnifiedIngress: true,
		}, true
	}

	switch capability := detectOpenAICapability(path); capability {
	case "":
	default:
		return RequestContext{
			ClientType:     ClientOpenAI,
			Family:         ProtocolFamilyOpenAI,
			Capability:     capability,
			UpstreamPath:   path,
			UnifiedIngress: true,
		}, true
	}

	return RequestContext{}, false
}

func requestContextForClientPath(clientType ClientType, path string, unified bool) RequestContext {
	path = normalizeUpstreamPath(path)
	requestCtx := RequestContext{
		ClientType:     clientType,
		UpstreamPath:   path,
		UnifiedIngress: unified,
	}

	switch clientType {
	case ClientClaude:
		requestCtx.Family = ProtocolFamilyClaude
		requestCtx.Capability = capabilityOrDefault(detectClaudeCapability(path), CapabilityClaudeCompatible)
	case ClientOpenAI:
		requestCtx.Family = ProtocolFamilyOpenAI
		requestCtx.Capability = capabilityOrDefault(detectOpenAICapability(path), CapabilityOpenAICompatible)
	case ClientGemini:
		requestCtx.Family = ProtocolFamilyGemini
		requestCtx.Capability = capabilityOrDefault(detectGeminiCapability(path), CapabilityGeminiCompatible)
	}

	return requestCtx
}

func withRequestContext(req *http.Request, requestCtx RequestContext) *http.Request {
	if req == nil {
		return nil
	}
	ctx := context.WithValue(req.Context(), requestContextKey{}, requestCtx)
	return req.WithContext(ctx)
}

func requestContextFromRequest(req *http.Request) (RequestContext, bool) {
	if req == nil {
		return RequestContext{}, false
	}
	requestCtx, ok := req.Context().Value(requestContextKey{}).(RequestContext)
	if !ok {
		return RequestContext{}, false
	}
	return requestCtx, true
}

func routingScopeForRequest(req *http.Request) routingScope {
	requestCtx, ok := requestContextFromRequest(req)
	if !ok {
		return routingScopeDefault
	}
	return routingScopeForCapability(requestCtx.Capability)
}

func routingScopeForCapability(capability RequestCapability) routingScope {
	switch capability {
	case CapabilityClaudeCountTokens:
		return routingScopeClaudeCountTokens
	case CapabilityOpenAIResponses:
		return routingScopeOpenAIResponses
	case CapabilityGeminiStreamGenerate:
		return routingScopeGeminiStream
	default:
		return routingScopeDefault
	}
}

func normalizeUpstreamPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func canonicalizeClipalPath(path string) string {
	path = collapseNestedVersionedPath(normalizeUpstreamPath(path))

	if isVersionedClipalPath(path) {
		return path
	}

	if canonical, ok := canonicalizeBareClaudePath(path); ok {
		return canonical
	}
	if canonical, ok := canonicalizeBareGeminiPath(path); ok {
		return canonical
	}
	if canonical, ok := canonicalizeBareOpenAIPath(path); ok {
		return canonical
	}

	return path
}

func collapseNestedVersionedPath(path string) string {
	path = normalizeUpstreamPath(path)
	for {
		collapsed := collapseOneNestedVersionedPath(path)
		if collapsed == path {
			return path
		}
		path = collapsed
	}
}

func collapseOneNestedVersionedPath(path string) string {
	for _, root := range versionedPathRoots {
		if !pathMatchesPrefix(path, root) {
			continue
		}

		rest := strings.TrimPrefix(path, root)
		if strings.TrimSpace(rest) == "" {
			continue
		}

		rest = normalizeUpstreamPath(rest)
		if isVersionedClipalPath(rest) {
			return rest
		}
	}

	return path
}

func isVersionedClipalPath(path string) bool {
	return pathMatchesPrefix(path, "/v1") ||
		pathMatchesPrefix(path, "/v1beta") ||
		pathMatchesPrefix(path, "/upload/v1") ||
		pathMatchesPrefix(path, "/upload/v1beta")
}

func canonicalizeBareClaudePath(path string) (string, bool) {
	switch {
	case matchesExactPath(path, "/messages"):
		return "/v1/messages", true
	case path == "/messages/count_tokens" || path == "/messages/count_tokens/":
		return "/v1" + path, true
	default:
		return "", false
	}
}

func canonicalizeBareGeminiPath(path string) (string, bool) {
	switch {
	case isGeminiBareMethodPath(path, ":generateContent"),
		isGeminiBareMethodPath(path, ":streamGenerateContent"),
		isGeminiBareMethodPath(path, ":countTokens"),
		isGeminiBareMethodPath(path, ":embedContent"),
		isGeminiBareMethodPath(path, ":batchEmbedContents"),
		isGeminiBareMethodPath(path, ":predict"),
		isGeminiBareMethodPath(path, ":predictLongRunning"),
		isGeminiBareResourceMethodPath(path, "/files"),
		isGeminiBareResourcePath(path, "/interactions"),
		isGeminiBareResourcePath(path, "/cachedContents"),
		isGeminiBareResourcePath(path, "/tunedModels"),
		isGeminiBareResourceMethodPath(path, "/batches"),
		isGeminiBareResourcePath(path, "/operations"),
		isGeminiBareResourcePath(path, "/fileSearchStores"),
		isGeminiBareResourcePath(path, "/generatedFiles"),
		isGeminiBareResourcePath(path, "/corpora"),
		isGeminiBareResourcePath(path, "/authTokens"),
		isGeminiBareResourcePath(path, "/agents"),
		isGeminiBareResourcePath(path, "/webhooks"):
		return "/v1beta" + path, true
	default:
		return "", false
	}
}

func isGeminiBareMethodPath(path string, method string) bool {
	return isGeminiMethodPathWithPrefix(path, "/models/", method)
}

func isGeminiBareResourcePath(path string, resource string) bool {
	return pathMatchesPrefix(path, resource) || strings.HasPrefix(path, resource+":")
}

func isGeminiBareResourceMethodPath(path string, resource string) bool {
	return strings.HasPrefix(path, resource+":") ||
		strings.HasPrefix(path, resource+"/") && strings.Contains(strings.TrimPrefix(path, resource+"/"), ":")
}

func canonicalizeBareOpenAIPath(path string) (string, bool) {
	switch {
	case matchesExactPath(path, "/chat/completions"),
		matchesExactPath(path, "/completions"),
		matchesExactPath(path, "/embeddings"),
		matchesExactPath(path, "/moderations"),
		pathMatchesPrefix(path, "/responses"),
		pathMatchesPrefix(path, "/audio"),
		pathMatchesPrefix(path, "/images"),
		pathMatchesPrefix(path, "/files"),
		pathMatchesPrefix(path, "/uploads"),
		pathMatchesPrefix(path, "/models"),
		pathMatchesPrefix(path, "/fine_tuning"),
		pathMatchesPrefix(path, "/batches"),
		pathMatchesPrefix(path, "/vector_stores"),
		pathMatchesPrefix(path, "/assistants"),
		pathMatchesPrefix(path, "/threads"),
		pathMatchesPrefix(path, "/realtime"):
		return "/v1" + path, true
	default:
		return "", false
	}
}

func matchesExactPath(path string, want string) bool {
	return path == want || path == want+"/"
}

func capabilityOrDefault(capability RequestCapability, fallback RequestCapability) RequestCapability {
	if capability != "" {
		return capability
	}
	return fallback
}

func detectClaudeCapability(path string) RequestCapability {
	switch {
	case isClaudeCountTokensPath(path):
		return CapabilityClaudeCountTokens
	case matchesExactPath(path, "/v1/messages"):
		return CapabilityClaudeMessages
	default:
		return ""
	}
}

func detectOpenAICapability(path string) RequestCapability {
	switch {
	case matchesExactPath(path, "/v1/chat/completions"):
		return CapabilityOpenAIChatCompletions
	case matchesExactPath(path, "/v1/completions"):
		return CapabilityOpenAICompletions
	case pathMatchesPrefix(path, "/v1/responses"):
		return CapabilityOpenAIResponses
	case matchesExactPath(path, "/v1/embeddings"):
		return CapabilityOpenAIEmbeddings
	case matchesExactPath(path, "/v1/moderations"):
		return CapabilityOpenAIModerations
	case pathMatchesPrefix(path, "/v1/audio"):
		return CapabilityOpenAIAudio
	case pathMatchesPrefix(path, "/v1/images"):
		return CapabilityOpenAIImages
	case pathMatchesPrefix(path, "/v1/files"):
		return CapabilityOpenAIFiles
	case pathMatchesPrefix(path, "/v1/uploads"):
		return CapabilityOpenAIUploads
	case pathMatchesPrefix(path, "/v1/models"):
		if !isGeminiCompatiblePath(path) {
			return CapabilityOpenAIModels
		}
		return ""
	case pathMatchesPrefix(path, "/v1/fine_tuning"):
		return CapabilityOpenAIFineTuning
	case pathMatchesPrefix(path, "/v1/batches"):
		return CapabilityOpenAIBatches
	case pathMatchesPrefix(path, "/v1/vector_stores"):
		return CapabilityOpenAIVectorStores
	case pathMatchesPrefix(path, "/v1/assistants"):
		return CapabilityOpenAIAssistants
	case pathMatchesPrefix(path, "/v1/threads"):
		return CapabilityOpenAIThreads
	case pathMatchesPrefix(path, "/v1/realtime"):
		return CapabilityOpenAIRealtime
	default:
		return ""
	}
}

func isGeminiCompatiblePath(path string) bool {
	return detectGeminiCapability(path) != ""
}

func detectGeminiCapability(path string) RequestCapability {
	switch {
	case isGeminiMethodPath(path, ":generateContent"):
		return CapabilityGeminiGenerateContent
	case isGeminiMethodPath(path, ":streamGenerateContent"):
		return CapabilityGeminiStreamGenerate
	case isGeminiMethodPath(path, ":countTokens"):
		return CapabilityGeminiCountTokens
	case isGeminiMethodPath(path, ":embedContent"):
		return CapabilityGeminiEmbedContent
	case isGeminiMethodPath(path, ":batchEmbedContents"):
		return CapabilityGeminiBatchEmbedContents
	case isGeminiMethodPath(path, ":predict"):
		return CapabilityGeminiPredict
	case isGeminiMethodPath(path, ":predictLongRunning"):
		return CapabilityGeminiPredictLongRunning
	case isGeminiModelsPath(path):
		return CapabilityGeminiModels
	case isGeminiFilesPath(path):
		return CapabilityGeminiFiles
	case isGeminiUploadFilesPath(path):
		return CapabilityGeminiUploadFiles
	case isGeminiCachedContentsPath(path):
		return CapabilityGeminiCachedContents
	case isGeminiTunedModelsPath(path):
		return CapabilityGeminiTunedModels
	case isGeminiInteractionsPath(path):
		return CapabilityGeminiInteractions
	case isGeminiBatchesPath(path):
		return CapabilityGeminiBatches
	case isGeminiOperationsPath(path):
		return CapabilityGeminiOperations
	case isGeminiFileSearchStoresPath(path):
		return CapabilityGeminiFileSearchStores
	case isGeminiGeneratedFilesPath(path):
		return CapabilityGeminiGeneratedFiles
	case isGeminiCorporaPath(path):
		return CapabilityGeminiCorpora
	case isGeminiAuthTokensPath(path):
		return CapabilityGeminiAuthTokens
	case isGeminiAgentsPath(path):
		return CapabilityGeminiAgents
	case isGeminiWebhooksPath(path):
		return CapabilityGeminiWebhooks
	default:
		return ""
	}
}

func isGeminiMethodPath(path string, method string) bool {
	return isGeminiMethodPathWithPrefix(path, "/v1beta/models/", method) ||
		isGeminiMethodPathWithPrefix(path, "/v1/models/", method) ||
		isVertexGeminiMethodPath(path, method)
}

func isGeminiMethodPathWithPrefix(path string, prefix string, method string) bool {
	return strings.HasPrefix(path, prefix) &&
		(strings.HasSuffix(path, method) || strings.HasSuffix(path, method+"/"))
}

func isGeminiModelsPath(path string) bool {
	return pathMatchesPrefix(path, "/v1beta/models") ||
		isGeminiV1ModelMetadataPath(path) ||
		isVertexGeminiModelsPath(path)
}

func isGeminiFilesPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/files")
}

func isGeminiUploadFilesPath(path string) bool {
	return pathMatchesPrefix(path, "/upload/v1beta/files") || pathMatchesPrefix(path, "/upload/v1/files")
}

func isGeminiCachedContentsPath(path string) bool {
	return pathMatchesPrefix(path, "/v1beta/cachedContents") || pathMatchesPrefix(path, "/v1/cachedContents")
}

func isGeminiTunedModelsPath(path string) bool {
	return pathMatchesPrefix(path, "/v1beta/tunedModels") || pathMatchesPrefix(path, "/v1/tunedModels")
}

func isGeminiInteractionsPath(path string) bool {
	return pathMatchesPrefix(path, "/v1beta/interactions") || pathMatchesPrefix(path, "/v1/interactions")
}

func isGeminiBatchesPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/batches")
}

func isGeminiOperationsPath(path string) bool {
	path = normalizeUpstreamPath(path)
	return pathMatchesPrefix(path, "/v1beta/operations") ||
		pathMatchesPrefix(path, "/v1/operations") ||
		strings.HasPrefix(path, "/v1beta/models/") && strings.Contains(path, "/operations/") ||
		strings.HasPrefix(path, "/v1/models/") && strings.Contains(path, "/operations/") ||
		isVertexGeminiOperationsPath(path)
}

func isGeminiFileSearchStoresPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/fileSearchStores") ||
		isGeminiVersionedResourcePath(path, "/v1/fileSearchStores")
}

func isGeminiGeneratedFilesPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/generatedFiles") ||
		isGeminiVersionedResourcePath(path, "/v1/generatedFiles")
}

func isGeminiCorporaPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/corpora") ||
		isGeminiVersionedResourcePath(path, "/v1/corpora")
}

func isGeminiAuthTokensPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/authTokens") ||
		isGeminiVersionedResourcePath(path, "/v1/authTokens")
}

func isGeminiAgentsPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/agents") ||
		isGeminiVersionedResourcePath(path, "/v1/agents")
}

func isGeminiWebhooksPath(path string) bool {
	return isGeminiVersionedResourcePath(path, "/v1beta/webhooks") ||
		isGeminiVersionedResourcePath(path, "/v1/webhooks")
}

func isGeminiVersionedResourcePath(path string, resource string) bool {
	return pathMatchesPrefix(path, resource) || strings.HasPrefix(path, resource+":")
}

func isGeminiV1ModelMetadataPath(path string) bool {
	if !pathMatchesPrefix(path, "/v1/models") || matchesExactPath(path, "/v1/models") {
		return false
	}

	trimmed := strings.TrimPrefix(path, "/v1/models/")
	if trimmed == "" {
		return false
	}
	modelID := trimmed
	if idx := strings.Index(modelID, "/"); idx >= 0 {
		modelID = modelID[:idx]
	}
	modelID = strings.TrimSpace(modelID)
	return strings.HasPrefix(modelID, "gemini")
}

func isVertexGeminiMethodPath(path string, method string) bool {
	path = normalizeUpstreamPath(path)
	if !strings.HasPrefix(path, "/v1/projects/") && !strings.HasPrefix(path, "/v1beta/projects/") {
		return false
	}
	if !strings.Contains(path, "/publishers/google/models/") {
		return false
	}
	return strings.HasSuffix(path, method) || strings.HasSuffix(path, method+"/")
}

func isVertexGeminiModelsPath(path string) bool {
	path = normalizeUpstreamPath(path)
	if !strings.HasPrefix(path, "/v1/projects/") && !strings.HasPrefix(path, "/v1beta/projects/") {
		return false
	}
	return strings.Contains(path, "/publishers/google/models")
}

func isVertexGeminiOperationsPath(path string) bool {
	path = normalizeUpstreamPath(path)
	if !strings.HasPrefix(path, "/v1/projects/") && !strings.HasPrefix(path, "/v1beta/projects/") {
		return false
	}
	if !strings.Contains(path, "/locations/") {
		return false
	}
	return strings.Contains(path, "/operations/") ||
		strings.HasSuffix(path, "/operations") ||
		strings.Contains(path, "/operations:")
}
