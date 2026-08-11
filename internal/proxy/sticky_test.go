package proxy

import (
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestExtractRequestStickyKey_OpenAIResponsesL1AndL2(t *testing.T) {
	t.Parallel()

	requestCtx := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIResponses,
		UpstreamPath: "/v1/responses",
	}

	l1 := extractRequestStickyKey(requestCtx, []byte(`{"previous_response_id":"resp_123","prompt_cache_key":"cache_ignored"}`))
	if l1.Level != stickyKeyLevelL1 {
		t.Fatalf("level: got %q want %q", l1.Level, stickyKeyLevelL1)
	}
	if l1.Key != "resp_123" {
		t.Fatalf("key: got %q want %q", l1.Key, "resp_123")
	}

	l2 := extractRequestStickyKey(requestCtx, []byte(`{"prompt_cache_key":"cache_123"}`))
	if l2.Level != stickyKeyLevelL2 {
		t.Fatalf("level: got %q want %q", l2.Level, stickyKeyLevelL2)
	}
	if l2.Key != "cache_123" {
		t.Fatalf("key: got %q want %q", l2.Key, "cache_123")
	}
}

func TestExtractRequestStickyKey_L3UsesSecondToLastHumanMessage(t *testing.T) {
	t.Parallel()

	openAI := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIChatCompletions,
		UpstreamPath: "/v1/chat/completions",
	}
	key := extractRequestStickyKey(openAI, []byte(`{"model":"gpt-4.1","messages":[{"role":"system","content":"sys"},{"role":"user","content":"Alpha first"},{"role":"assistant","content":"reply"},{"role":"user","content":"Beta second"}]}`))
	if key.Level != stickyKeyLevelL3 {
		t.Fatalf("level: got %q want %q", key.Level, stickyKeyLevelL3)
	}
	if got, want := key.Preview, "alpha first"; got != want {
		t.Fatalf("preview: got %q want %q", got, want)
	}
	if got, want := key.Key, buildDynamicFeatureKey(openAI, "gpt-4.1", "Alpha first"); got != want {
		t.Fatalf("key: got %q want %q", got, want)
	}

	anthropic := RequestContext{
		ClientType:   ClientClaude,
		Family:       ProtocolFamilyClaude,
		Capability:   CapabilityClaudeMessages,
		UpstreamPath: "/v1/messages",
	}
	key = extractRequestStickyKey(anthropic, []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"Gamma one"}]},{"role":"assistant","content":[{"type":"text","text":"reply"}]},{"role":"user","content":[{"type":"text","text":"Delta two"}]}]}`))
	if got, want := key.Preview, "gamma one"; got != want {
		t.Fatalf("anthropic preview: got %q want %q", got, want)
	}

	gemini := RequestContext{
		ClientType:   ClientGemini,
		Family:       ProtocolFamilyGemini,
		Capability:   CapabilityGeminiGenerateContent,
		UpstreamPath: "/v1beta/models/gemini-2.5-pro:generateContent",
	}
	key = extractRequestStickyKey(gemini, []byte(`{"contents":[{"role":"user","parts":[{"text":"First prompt"}]},{"role":"model","parts":[{"text":"reply"}]},{"role":"user","parts":[{"text":"Second prompt"}]}]}`))
	if got, want := key.Preview, "first prompt"; got != want {
		t.Fatalf("gemini preview: got %q want %q", got, want)
	}
}

func TestExtractRequestStickyKey_SingleHumanMessageDoesNotCreateL3(t *testing.T) {
	t.Parallel()

	requestCtx := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIChatCompletions,
		UpstreamPath: "/v1/chat/completions",
	}

	key := extractRequestStickyKey(requestCtx, []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"Only once"}]}`))
	if key.Level != "" {
		t.Fatalf("level: got %q want empty", key.Level)
	}
}

func TestExtractResponseLearningStickyKey_UsesLastHumanMessageIncludingFirstTurn(t *testing.T) {
	t.Parallel()

	requestCtx := RequestContext{
		ClientType:   ClientOpenAI,
		Family:       ProtocolFamilyOpenAI,
		Capability:   CapabilityOpenAIChatCompletions,
		UpstreamPath: "/v1/chat/completions",
	}

	firstTurn := extractResponseLearningStickyKey(requestCtx, []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"First turn"}]}`))
	if firstTurn.Level != stickyKeyLevelL3 {
		t.Fatalf("firstTurn level: got %q want %q", firstTurn.Level, stickyKeyLevelL3)
	}
	if got, want := firstTurn.Preview, "first turn"; got != want {
		t.Fatalf("firstTurn preview: got %q want %q", got, want)
	}

	multiTurn := extractResponseLearningStickyKey(requestCtx, []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"Alpha first"},{"role":"assistant","content":"reply"},{"role":"user","content":"Beta second"}]}`))
	if got, want := multiTurn.Preview, "beta second"; got != want {
		t.Fatalf("multiTurn preview: got %q want %q", got, want)
	}
	if got, want := multiTurn.Key, buildDynamicFeatureKey(requestCtx, "gpt-4.1", "Beta second"); got != want {
		t.Fatalf("multiTurn key: got %q want %q", got, want)
	}
}

func TestPreviewStickyFeature_TruncatesToTwentyFourChars(t *testing.T) {
	t.Parallel()

	got := previewStickyFeature("  This is a very long human message that should be truncated  ")
	if want := "this is a very long huma"; got != want {
		t.Fatalf("preview: got %q want %q", got, want)
	}
}

func TestEnforceDynamicFeatureCapacityLocked_EvictsLeastRecentlySeenEntries(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.routing.dynamicFeatureCapacity = 2

	now := time.Now()
	cp.dynamicFeatureBindings = map[string]stickyLookupEntry{
		"k-oldest": {ProviderIndex: 0, LastSeenAt: now.Add(-3 * time.Minute), Source: "dynamic_human_feature"},
		"k-middle": {ProviderIndex: 0, LastSeenAt: now.Add(-2 * time.Minute), Source: "dynamic_human_feature"},
		"k-newest": {ProviderIndex: 0, LastSeenAt: now.Add(-1 * time.Minute), Source: "dynamic_human_feature"},
	}

	cp.enforceDynamicFeatureCapacityLocked()

	if len(cp.dynamicFeatureBindings) != 2 {
		t.Fatalf("len(dynamicFeatureBindings): got %d want 2", len(cp.dynamicFeatureBindings))
	}
	if _, ok := cp.dynamicFeatureBindings["k-oldest"]; ok {
		t.Fatalf("expected oldest entry to be evicted")
	}
	if _, ok := cp.dynamicFeatureBindings["k-middle"]; !ok {
		t.Fatalf("expected middle entry to remain")
	}
	if _, ok := cp.dynamicFeatureBindings["k-newest"]; !ok {
		t.Fatalf("expected newest entry to remain")
	}
}
