package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const stickyPreviewLimit = 24

type stickyKeyLevel string

const (
	stickyKeyLevelL1 stickyKeyLevel = "L1"
	stickyKeyLevelL2 stickyKeyLevel = "L2"
	stickyKeyLevelL3 stickyKeyLevel = "L3"
)

type stickyKey struct {
	Level   stickyKeyLevel
	Key     string
	Source  string
	Preview string
}

type stickyBinding struct {
	ProviderIndex int
	KeyIndex      int
	BoundAt       time.Time
	LastSeenAt    time.Time
	Level         stickyKeyLevel
	Source        string
}

type stickyLookupEntry struct {
	ProviderIndex int
	KeyIndex      int
	LastSeenAt    time.Time
	Source        string
}

func stickyScopeKey(scope routingScope, key string) string {
	return fmt.Sprintf("%s\x00%s", scope, strings.TrimSpace(key))
}

func extractRequestStickyKeyFromRoot(requestCtx RequestContext, root map[string]any) stickyKey {
	if requestCtx.Capability == CapabilityOpenAIResponses {
		if v := strings.TrimSpace(stringField(root, "previous_response_id")); v != "" {
			return stickyKey{Level: stickyKeyLevelL1, Key: v, Source: "previous_response_id"}
		}
		if v := strings.TrimSpace(stringField(root, "prompt_cache_key")); v != "" {
			return stickyKey{Level: stickyKeyLevelL2, Key: v, Source: "prompt_cache_key"}
		}
	}

	humanMessages := extractHumanMessages(requestCtx, root)
	if len(humanMessages) < 2 {
		return stickyKey{}
	}
	model := stickyModelName(requestCtx, root)
	text := humanMessages[len(humanMessages)-2]
	return stickyKey{
		Level:   stickyKeyLevelL3,
		Key:     buildDynamicFeatureKey(requestCtx, model, text),
		Source:  "dynamic_human_feature",
		Preview: previewStickyFeature(text),
	}
}

func extractResponseLearningStickyKeyFromRoot(requestCtx RequestContext, root map[string]any) stickyKey {
	humanMessages := extractHumanMessages(requestCtx, root)
	if len(humanMessages) == 0 {
		return stickyKey{}
	}
	model := stickyModelName(requestCtx, root)
	text := humanMessages[len(humanMessages)-1]
	return stickyKey{
		Level:   stickyKeyLevelL3,
		Key:     buildDynamicFeatureKey(requestCtx, model, text),
		Source:  "dynamic_human_feature",
		Preview: previewStickyFeature(text),
	}
}

func buildDynamicFeatureKey(requestCtx RequestContext, model string, text string) string {
	normalized := normalizeStickyText(text)
	if normalized == "" {
		return ""
	}
	model = strings.TrimSpace(strings.ToLower(model))
	payload := strings.Join([]string{
		string(requestCtx.ClientType),
		string(requestCtx.Capability),
		model,
		normalized,
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func previewStickyFeature(text string) string {
	normalized := normalizeStickyText(text)
	if len(normalized) > stickyPreviewLimit {
		return normalized[:stickyPreviewLimit]
	}
	return normalized
}

func normalizeStickyText(text string) string {
	text = strings.TrimSpace(strings.ToLower(text))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func decodeStickyRoot(body []byte) map[string]any {
	if len(body) == 0 {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	return root
}

func stickyModelName(requestCtx RequestContext, root map[string]any) string {
	if v := strings.TrimSpace(stringField(root, "model")); v != "" {
		return v
	}
	path := strings.TrimSpace(requestCtx.UpstreamPath)
	if path == "" {
		return ""
	}
	if idx := strings.Index(path, "/models/"); idx >= 0 {
		modelPath := path[idx+len("/models/"):]
		if cut := strings.IndexAny(modelPath, ":/"); cut >= 0 {
			return strings.TrimSpace(modelPath[:cut])
		}
		return strings.TrimSpace(modelPath)
	}
	return ""
}

func extractHumanMessages(requestCtx RequestContext, root map[string]any) []string {
	switch requestCtx.Family {
	case ProtocolFamilyGemini:
		return extractGeminiHumanMessages(root)
	default:
		return extractMessagesAPIHumanMessages(root)
	}
}

func extractMessagesAPIHumanMessages(root map[string]any) []string {
	msgs, _ := root["messages"].([]any)
	out := make([]string, 0, len(msgs))
	for _, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(stringField(msg, "role")), "user") {
			continue
		}
		if text := contentText(msg["content"]); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func extractGeminiHumanMessages(root map[string]any) []string {
	contents, _ := root["contents"].([]any)
	out := make([]string, 0, len(contents))
	for _, raw := range contents {
		content, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(stringField(content, "role")), "user") {
			continue
		}
		parts, _ := content["parts"].([]any)
		var texts []string
		for _, partRaw := range parts {
			part, ok := partRaw.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(stringField(part, "text")); text != "" {
				texts = append(texts, text)
			}
		}
		if len(texts) > 0 {
			out = append(out, strings.Join(texts, " "))
		}
	}
	return out
}

func contentText(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		var texts []string
		for _, item := range typed {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(stringField(obj, "text")); text != "" {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, " ")
	default:
		return ""
	}
}

func stringField(root map[string]any, key string) string {
	if root == nil {
		return ""
	}
	value, ok := root[key]
	if !ok {
		return ""
	}
	str, _ := value.(string)
	return str
}

func (cp *ClientProxy) resolveStickyProvider(scope routingScope, key stickyKey, now time.Time) (int, int, bool) {
	if cp == nil || strings.TrimSpace(key.Key) == "" {
		return 0, 0, false
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.pruneStickyStateLocked(now)

	switch key.Level {
	case stickyKeyLevelL1:
		if entry, ok := cp.stickyBindings[stickyScopeKey(scope, key.Key)]; ok {
			entry.LastSeenAt = now
			cp.stickyBindings[stickyScopeKey(scope, key.Key)] = entry
			return entry.ProviderIndex, entry.KeyIndex, true
		}
		if entry, ok := cp.responseLookup[key.Key]; ok {
			entry.LastSeenAt = now
			cp.responseLookup[key.Key] = entry
			return entry.ProviderIndex, entry.KeyIndex, true
		}
	case stickyKeyLevelL2, stickyKeyLevelL3:
		if entry, ok := cp.dynamicFeatureBindings[stickyScopeKey(scope, key.Key)]; ok {
			entry.LastSeenAt = now
			cp.dynamicFeatureBindings[stickyScopeKey(scope, key.Key)] = entry
			return entry.ProviderIndex, entry.KeyIndex, true
		}
	}

	return 0, 0, false
}

func (cp *ClientProxy) learnStickySuccessWithPayload(scope routingScope, requestCtx RequestContext, requestKey stickyKey, payload *requestPayload, responseBody []byte, providerIndex int, keyIndex int, now time.Time) {
	if cp == nil || providerIndex < 0 {
		return
	}

	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.pruneStickyStateLocked(now)

	switch requestKey.Level {
	case stickyKeyLevelL1:
		cp.stickyBindings[stickyScopeKey(scope, requestKey.Key)] = stickyBinding{
			ProviderIndex: providerIndex,
			KeyIndex:      keyIndex,
			BoundAt:       now,
			LastSeenAt:    now,
			Level:         requestKey.Level,
			Source:        requestKey.Source,
		}
	case stickyKeyLevelL2, stickyKeyLevelL3:
		cp.dynamicFeatureBindings[stickyScopeKey(scope, requestKey.Key)] = stickyLookupEntry{
			ProviderIndex: providerIndex,
			KeyIndex:      keyIndex,
			LastSeenAt:    now,
			Source:        requestKey.Source,
		}
		cp.enforceDynamicFeatureCapacityLocked()
	}

	// Even when the request is anchored by an explicit L1 key, learn the current
	// human-message feature as an L3 hint so a later stateless follow-up can
	// still prefer the same provider.
	if learned := payload.responseLearningStickyKey(requestCtx); learned.Level == stickyKeyLevelL3 && learned.Key != "" {
		cp.dynamicFeatureBindings[stickyScopeKey(scope, learned.Key)] = stickyLookupEntry{
			ProviderIndex: providerIndex,
			KeyIndex:      keyIndex,
			LastSeenAt:    now,
			Source:        learned.Source,
		}
		cp.enforceDynamicFeatureCapacityLocked()
	}

	if responseID := extractResponseLookupID(requestCtx, responseBody); responseID != "" {
		cp.responseLookup[responseID] = stickyLookupEntry{
			ProviderIndex: providerIndex,
			KeyIndex:      keyIndex,
			LastSeenAt:    now,
			Source:        "response_id",
		}
	}
}

func extractResponseLookupID(requestCtx RequestContext, responseBody []byte) string {
	if requestCtx.Capability != CapabilityOpenAIResponses || len(responseBody) == 0 {
		return ""
	}
	root := decodeStickyRoot(responseBody)
	return strings.TrimSpace(stringField(root, "id"))
}

func (cp *ClientProxy) pruneStickyStateLocked(now time.Time) {
	for key, entry := range cp.stickyBindings {
		if cp.routing.explicitTTL > 0 && now.Sub(entry.LastSeenAt) > cp.routing.explicitTTL {
			delete(cp.stickyBindings, key)
		}
	}
	for key, entry := range cp.responseLookup {
		if cp.routing.responseLookupTTL > 0 && now.Sub(entry.LastSeenAt) > cp.routing.responseLookupTTL {
			delete(cp.responseLookup, key)
		}
	}
	for key, entry := range cp.dynamicFeatureBindings {
		ttl := cp.routing.dynamicFeatureTTL
		if entry.Source == "prompt_cache_key" {
			ttl = cp.routing.cacheHintTTL
		}
		if ttl > 0 && now.Sub(entry.LastSeenAt) > ttl {
			delete(cp.dynamicFeatureBindings, key)
		}
	}
	cp.enforceDynamicFeatureCapacityLocked()
}

func (cp *ClientProxy) enforceDynamicFeatureCapacityLocked() {
	if cp == nil || cp.routing.dynamicFeatureCapacity <= 0 {
		return
	}

	overflow := len(cp.dynamicFeatureBindings) - cp.routing.dynamicFeatureCapacity
	if overflow <= 0 {
		return
	}

	type candidate struct {
		key string
		at  time.Time
	}
	candidates := make([]candidate, 0, len(cp.dynamicFeatureBindings))
	for key, entry := range cp.dynamicFeatureBindings {
		candidates = append(candidates, candidate{key: key, at: entry.LastSeenAt})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].key < candidates[j].key
		}
		return candidates[i].at.Before(candidates[j].at)
	})
	for i := 0; i < overflow && i < len(candidates); i++ {
		delete(cp.dynamicFeatureBindings, candidates[i].key)
	}
}
