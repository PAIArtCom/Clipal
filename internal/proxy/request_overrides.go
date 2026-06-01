package proxy

import (
	"mime"
	"net/http"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

func hasProviderRequestOverrides(provider config.Provider) bool {
	return provider.ModelOverride() != "" ||
		provider.OpenAIReasoningEffort() != "" ||
		provider.ClaudeEffort() != "" ||
		provider.ClaudeThinkingBudgetTokens() > 0
}

func isJSONRequest(req *http.Request) bool {
	if req == nil {
		return false
	}
	mediaType := strings.TrimSpace(req.Header.Get("Content-Type"))
	if mediaType == "" {
		return false
	}
	parsed, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		parsed = mediaType
	}
	parsed = strings.ToLower(strings.TrimSpace(parsed))
	return parsed == "application/json" || strings.HasSuffix(parsed, "+json")
}

func applyProviderRequestOverridesToRoot(root map[string]any, requestCtx RequestContext, provider config.Provider) bool {
	switch requestCtx.Family {
	case ProtocolFamilyOpenAI:
		return applyOpenAIProviderRequestOverrides(root, requestCtx, provider)
	case ProtocolFamilyClaude:
		return applyClaudeProviderRequestOverrides(root, requestCtx, provider)
	default:
		return false
	}
}

func applyOpenAIProviderRequestOverrides(root map[string]any, requestCtx RequestContext, provider config.Provider) bool {
	changed := false
	model := provider.ModelOverride()
	if model != "" && isOpenAIGenerationCapability(requestCtx.Capability) {
		root["model"] = model
		changed = true
	}

	reasoningEffort := provider.OpenAIReasoningEffort()
	if reasoningEffort == "" {
		return changed
	}

	switch requestCtx.Capability {
	case CapabilityOpenAIResponses:
		reasoning, _ := root["reasoning"].(map[string]any)
		if reasoning == nil {
			reasoning = make(map[string]any)
		}
		reasoning["effort"] = reasoningEffort
		root["reasoning"] = reasoning
		return true
	default:
		if _, ok := root["reasoning_effort"]; ok {
			root["reasoning_effort"] = reasoningEffort
			return true
		}
	}

	return changed
}

func applyClaudeProviderRequestOverrides(root map[string]any, requestCtx RequestContext, provider config.Provider) bool {
	if requestCtx.Capability != CapabilityClaudeMessages && requestCtx.Capability != CapabilityClaudeCountTokens {
		return false
	}

	changed := false
	model := provider.ModelOverride()
	if model != "" {
		root["model"] = model
		changed = true
	}

	if effort := provider.ClaudeEffort(); effort != "" {
		outputConfig, _ := root["output_config"].(map[string]any)
		if outputConfig == nil {
			outputConfig = make(map[string]any)
		}
		outputConfig["effort"] = effort
		root["output_config"] = outputConfig
		root["thinking"] = map[string]any{
			"type": "adaptive",
		}
		return true
	}

	if thinkingBudgetTokens := provider.ClaudeThinkingBudgetTokens(); thinkingBudgetTokens > 0 && !claudeModelUsesOutputConfigEffort(stringValue(root["model"])) {
		root["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": thinkingBudgetTokens,
		}
		changed = true
	}

	return changed
}

func claudeModelUsesOutputConfigEffort(model string) bool {
	normalized := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(normalized, "claude-opus-4-7") ||
		strings.Contains(normalized, "claude-opus-4-8")
}

func isOpenAIGenerationCapability(capability RequestCapability) bool {
	switch capability {
	case CapabilityOpenAIResponses, CapabilityOpenAIChatCompletions, CapabilityOpenAICompletions:
		return true
	default:
		return false
	}
}
