package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestForwardWithFailover_PreviousResponseBusyOverflowRebindsSession(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.applyRoutingRuntimeSettings(routingRuntimeSettings{
		explicitTTL:            30 * time.Minute,
		cacheHintTTL:           10 * time.Minute,
		dynamicFeatureTTL:      10 * time.Minute,
		responseLookupTTL:      15 * time.Minute,
		dynamicFeatureCapacity: 1024,
		busyRetryDelays:        []time.Duration{10 * time.Millisecond, 20 * time.Millisecond},
		busyProbeMaxInFlight:   1,
		shortRetryAfterMax:     3 * time.Second,
		maxInlineWait:          50 * time.Millisecond,
	})

	var p1ChainCalls int32
	var p2ChainCalls int32
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		payload := string(body)
		switch r.URL.Host {
		case "p1":
			if strings.Contains(payload, `"previous_response_id":"resp_1"`) {
				atomic.AddInt32(&p1ChainCalls, 1)
				h := make(http.Header)
				h.Set("Content-Type", "application/json")
				h.Set("Retry-After", "1")
				return newResponse(http.StatusTooManyRequests, h, `{"error":{"message":"too many concurrent requests","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`), nil
			}
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusOK, h, `{"id":"resp_1","status":"completed"}`), nil
		case "p2":
			if strings.Contains(payload, `"previous_response_id":"resp_1"`) {
				atomic.AddInt32(&p2ChainCalls, 1)
			}
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return newResponse(http.StatusOK, h, `{"id":"resp_2","status":"completed"}`), nil
		default:
			return nil, io.ErrUnexpectedEOF
		}
	})

	firstRR := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-4.1","input":"hello"}`)))
	firstReq = withRequestContext(firstReq, requestContextForClientPath(ClientOpenAI, "/v1/responses", false))
	cp.forwardWithFailover(firstRR, firstReq, "/v1/responses")
	if firstRR.Result().StatusCode != http.StatusOK {
		t.Fatalf("first status: got %d want %d body=%s", firstRR.Result().StatusCode, http.StatusOK, firstRR.Body.String())
	}

	secondRR := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-4.1","previous_response_id":"resp_1","input":"next"}`)))
	secondReq = withRequestContext(secondReq, requestContextForClientPath(ClientOpenAI, "/v1/responses", false))
	cp.forwardWithFailover(secondRR, secondReq, "/v1/responses")
	if secondRR.Result().StatusCode != http.StatusOK {
		t.Fatalf("second status: got %d want %d body=%s", secondRR.Result().StatusCode, http.StatusOK, secondRR.Body.String())
	}
	if got := atomic.LoadInt32(&p1ChainCalls); got != 2 {
		t.Fatalf("p1 chain calls: got %d want 2", got)
	}
	if got := atomic.LoadInt32(&p2ChainCalls); got != 1 {
		t.Fatalf("p2 chain calls after overflow: got %d want 1", got)
	}

	thirdRR := httptest.NewRecorder()
	thirdReq := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/responses", bytes.NewReader([]byte(`{"model":"gpt-4.1","previous_response_id":"resp_1","input":"third"}`)))
	thirdReq = withRequestContext(thirdReq, requestContextForClientPath(ClientOpenAI, "/v1/responses", false))
	cp.forwardWithFailover(thirdRR, thirdReq, "/v1/responses")
	if thirdRR.Result().StatusCode != http.StatusOK {
		t.Fatalf("third status: got %d want %d body=%s", thirdRR.Result().StatusCode, http.StatusOK, thirdRR.Body.String())
	}
	if got := atomic.LoadInt32(&p1ChainCalls); got != 2 {
		t.Fatalf("p1 chain calls after rebind: got %d want 2", got)
	}
	if got := atomic.LoadInt32(&p2ChainCalls); got != 2 {
		t.Fatalf("p2 chain calls after rebind: got %d want 2", got)
	}
}

func TestForwardWithFailover_FirstTurnLearningSeedsSecondTurnL3Lookup(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	var p1Calls int32
	var p2Calls int32
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			atomic.AddInt32(&p1Calls, 1)
		case "p2":
			atomic.AddInt32(&p2Calls, 1)
		default:
			return nil, io.ErrUnexpectedEOF
		}
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		return newResponse(http.StatusOK, h, `{"id":"chatcmpl_1"}`), nil
	})

	cp.setCurrentIndexForScope(1, routingScopeDefault)
	firstRR := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"How do I deploy to Cloudflare?"}]}`)))
	firstReq = withRequestContext(firstReq, requestContextForClientPath(ClientOpenAI, "/v1/chat/completions", false))
	cp.forwardWithFailover(firstRR, firstReq, "/v1/chat/completions")
	if firstRR.Result().StatusCode != http.StatusOK {
		t.Fatalf("first status: got %d want %d body=%s", firstRR.Result().StatusCode, http.StatusOK, firstRR.Body.String())
	}
	if got := atomic.LoadInt32(&p2Calls); got != 1 {
		t.Fatalf("first turn p2 calls: got %d want 1", got)
	}

	cp.setCurrentIndexForScope(0, routingScopeDefault)
	secondRR := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"How do I deploy to Cloudflare?"},{"role":"assistant","content":"Use Wrangler."},{"role":"user","content":"What about environment variables?"}]}`)))
	secondReq = withRequestContext(secondReq, requestContextForClientPath(ClientOpenAI, "/v1/chat/completions", false))
	cp.forwardWithFailover(secondRR, secondReq, "/v1/chat/completions")
	if secondRR.Result().StatusCode != http.StatusOK {
		t.Fatalf("second status: got %d want %d body=%s", secondRR.Result().StatusCode, http.StatusOK, secondRR.Body.String())
	}
	if got := atomic.LoadInt32(&p1Calls); got != 0 {
		t.Fatalf("second turn p1 calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&p2Calls); got != 2 {
		t.Fatalf("second turn p2 calls: got %d want 2", got)
	}
}
