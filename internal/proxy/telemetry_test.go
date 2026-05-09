package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

func TestRecordCompletedUsageCountsOnlySuccessfulGenerationRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		clientType  ClientType
		capability  RequestCapability
		method      string
		statusCode  int
		wantRequest int64
		wantSuccess int64
	}{
		{
			name:        "openai responses success counts",
			clientType:  ClientOpenAI,
			capability:  CapabilityOpenAIResponses,
			method:      http.MethodPost,
			statusCode:  http.StatusOK,
			wantRequest: 1,
			wantSuccess: 1,
		},
		{
			name:        "openai responses failure skips success count",
			clientType:  ClientOpenAI,
			capability:  CapabilityOpenAIResponses,
			method:      http.MethodPost,
			statusCode:  http.StatusBadRequest,
			wantRequest: 1,
			wantSuccess: 0,
		},
		{
			name:        "openai models success skips success count",
			clientType:  ClientOpenAI,
			capability:  CapabilityOpenAIModels,
			method:      http.MethodGet,
			statusCode:  http.StatusOK,
			wantRequest: 1,
			wantSuccess: 0,
		},
		{
			name:        "claude messages success counts",
			clientType:  ClientClaude,
			capability:  CapabilityClaudeMessages,
			method:      http.MethodPost,
			statusCode:  http.StatusOK,
			wantRequest: 1,
			wantSuccess: 1,
		},
		{
			name:        "gemini generate content success counts",
			clientType:  ClientGemini,
			capability:  CapabilityGeminiGenerateContent,
			method:      http.MethodPost,
			statusCode:  http.StatusOK,
			wantRequest: 1,
			wantSuccess: 1,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store, err := telemetry.NewStore("")
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			cp := newClientProxy(tc.clientType, config.ClientModeAuto, "", []config.Provider{
				{Name: "p1", BaseURL: "https://example.com", APIKey: "provider-key", Priority: 1},
			}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, store)

			req := httptest.NewRequest(tc.method, "http://proxy/test", nil)
			req = withRequestContext(req, RequestContext{
				ClientType: tc.clientType,
				Capability: tc.capability,
			})

			cp.recordCompletedUsage(req, "p1", tc.statusCode, telemetry.UsageSnapshot{
				UsageDelta: telemetry.UsageDelta{
					InputTokens:  3,
					OutputTokens: 4,
				},
			}, time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))

			got, ok := store.ProviderSnapshot(string(tc.clientType), "p1")
			if !ok {
				t.Fatalf("ProviderSnapshot missing")
			}
			if got.RequestCount != tc.wantRequest || got.SuccessCount != tc.wantSuccess {
				t.Fatalf("snapshot = %#v", got)
			}
		})
	}
}

func TestRecordCompletedUsagePersistsTrackedCost(t *testing.T) {
	t.Parallel()

	store, err := telemetry.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "https://example.com", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, store)

	req := httptest.NewRequest(http.MethodPost, "http://proxy/test", nil)
	req = withRequestContext(req, RequestContext{
		ClientType: ClientOpenAI,
		Capability: CapabilityOpenAIResponses,
	})

	cp.recordCompletedUsage(req, "p1", http.StatusOK, telemetry.UsageSnapshot{
		UsageDelta: telemetry.UsageDelta{
			InputTokens:  3,
			OutputTokens: 4,
		},
		CostMicros: 123_456,
		HasCost:    true,
	}, time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))

	got, ok := store.ProviderSnapshot(string(ClientOpenAI), "p1")
	if !ok {
		t.Fatalf("ProviderSnapshot missing")
	}
	if !got.HasCost || got.TotalCostMicros != 123_456 {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestForwardWithFailover_OpenAIPlainTextStreamRecordsUsage(t *testing.T) {
	t.Parallel()

	store, err := telemetry.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "provider-key", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{}, store)
	streamBody := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1","usage":null}}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":23,"output_tokens":71,"output_tokens_details":{"reasoning_tokens":63},"total_tokens":94}}}`,
		"",
	}, "\n")
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Content-Type", "text/plain; charset=utf-8")
		return newResponse(http.StatusOK, h, streamBody), nil
	})

	reqBody := []byte(`{"model":"gpt-5.4","stream":true,"input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader(reqBody))
	req = withRequestContext(req, requestContextForClientPath(ClientOpenAI, "/v1/responses", true))
	extractor := usageExtractorFromRequestWithContentType(req, "text/event-stream; charset=utf-8")
	if extractor == nil {
		t.Fatalf("expected usage extractor")
	}
	extractor.Append([]byte(streamBody))
	if usage, ok := extractor.Finalize(); !ok || usage.InputTokens != 23 || usage.OutputTokens != 71 || usage.ReasoningTokens != 63 {
		t.Fatalf("direct extractor = %#v ok=%v", usage, ok)
	}

	rr := httptest.NewRecorder()
	cp.forwardWithFailover(rr, req, "/v1/responses")

	if got := rr.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
	if got := rr.Result().Header.Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("content-type = %q", got)
	}

	got, ok := store.ProviderSnapshot(string(ClientOpenAI), "p1")
	if !ok {
		t.Fatalf("ProviderSnapshot missing")
	}
	if got.RequestCount != 1 || got.SuccessCount != 1 {
		t.Fatalf("counts = %#v", got)
	}
	if got.InputTokens != 23 || got.OutputTokens != 71 || got.TotalTokens != 94 {
		t.Fatalf("tokens = %#v", got)
	}
	if got.ReasoningTokens != 63 {
		t.Fatalf("reasoning_tokens = %d", got.ReasoningTokens)
	}
}
