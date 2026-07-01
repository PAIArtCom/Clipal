package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func newUnifiedIngressTestRouter() *Router {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			ListenAddr:            "127.0.0.1",
			Port:                  3333,
			LogLevel:              config.LogLevelDebug,
			ReactivateAfter:       "1h",
			UpstreamIdleTimeout:   "0",
			ResponseHeaderTimeout: "2m",
			MaxRequestBody:        32 * 1024 * 1024,
		},
		Claude: config.ClientConfig{
			Mode: config.ClientModeAuto,
			Providers: []config.Provider{
				{Name: "claude", BaseURL: "http://claude", APIKey: "k-claude", Priority: 1},
			},
		},
		OpenAI: config.ClientConfig{
			Mode: config.ClientModeAuto,
			Providers: []config.Provider{
				{Name: "codex", BaseURL: "http://codex", APIKey: "k-codex", Priority: 1},
			},
		},
		Gemini: config.ClientConfig{
			Mode: config.ClientModeAuto,
			Providers: []config.Provider{
				{Name: "gemini", BaseURL: "http://gemini", APIKey: "k-gemini", Priority: 1},
			},
		},
	}

	return NewRouter(cfg)
}

func installMarkerTransport(cp *ClientProxy, host string, body string, calls *int32) {
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != host {
			return nil, io.ErrUnexpectedEOF
		}
		atomic.AddInt32(calls, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		}, nil
	})
}

func installPathAssertingTransport(cp *ClientProxy, host string, wantPath string, body string, calls *int32) {
	cp.httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != host {
			return nil, io.ErrUnexpectedEOF
		}
		if r.URL.Path != wantPath {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader([]byte("unexpected path: " + r.URL.Path))),
			}, nil
		}
		atomic.AddInt32(calls, 1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		}, nil
	})
}

func TestClipalClaudeRequestUsesClaudePool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/messages", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "claude-ok" {
		t.Fatalf("body: got %q want %q", got, "claude-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 1 {
		t.Fatalf("claude calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 0 {
		t.Fatalf("gemini calls: got %d want 0", got)
	}
}

func TestClipalResponsesRequestUsesCodexPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 0 {
		t.Fatalf("gemini calls: got %d want 0", got)
	}
}

func TestClipalBareResponsesPathCanonicalizesToV1(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var codexCalls int32
	installPathAssertingTransport(router.proxies[ClientOpenAI], "codex", "/v1/responses", "codex-ok", &codexCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/responses", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
}

func TestClipalDuplicateV1ResponsesPathCanonicalizesToSingleV1(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var codexCalls int32
	installPathAssertingTransport(router.proxies[ClientOpenAI], "codex", "/v1/responses", "codex-ok", &codexCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
}

func TestClipalBareResponsesResourcePathCanonicalizesToV1(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var codexCalls int32
	installPathAssertingTransport(router.proxies[ClientOpenAI], "codex", "/v1/responses/resp_123/cancel", "codex-ok", &codexCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/responses/resp_123/cancel", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
}

func TestClipalBareClaudeMessagesPathCanonicalizesToV1(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls int32
	installPathAssertingTransport(router.proxies[ClientClaude], "claude", "/v1/messages", "claude-ok", &claudeCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/messages", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "claude-ok" {
		t.Fatalf("body: got %q want %q", got, "claude-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 1 {
		t.Fatalf("claude calls: got %d want 1", got)
	}
}

func TestClipalBareGeminiMethodPathCanonicalizesToV1Beta(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var geminiCalls int32
	installPathAssertingTransport(
		router.proxies[ClientGemini],
		"gemini",
		"/v1beta/models/gemini-2.5-pro:generateContent",
		"gemini-ok",
		&geminiCalls,
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"http://proxy/clipal/models/gemini-2.5-pro:generateContent",
		bytes.NewReader([]byte(`{"contents":[]}`)),
	)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalBareModelsPathDefaultsToOpenAICompatibility(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var codexCalls int32
	installPathAssertingTransport(router.proxies[ClientOpenAI], "codex", "/v1/models", "codex-ok", &codexCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/models", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
}

func TestClipalBareGeminiModelMetadataPathUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var geminiCalls int32
	installPathAssertingTransport(router.proxies[ClientGemini], "gemini", "/v1/models/gemini-2.5-pro", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/models/gemini-2.5-pro", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiInteractionsPathUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var geminiCalls int32
	installPathAssertingTransport(router.proxies[ClientGemini], "gemini", "/v1beta/interactions", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/interactions", bytes.NewReader([]byte(`{"input":[]}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalVertexGeminiPathUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	vertexPath := "/v1/projects/proj/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent"
	var geminiCalls int32
	installPathAssertingTransport(router.proxies[ClientGemini], "gemini", vertexPath, "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal"+vertexPath, bytes.NewReader([]byte(`{"contents":[]}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalBareGeminiPredictLongRunningPathUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var geminiCalls int32
	installPathAssertingTransport(
		router.proxies[ClientGemini],
		"gemini",
		"/v1beta/models/veo-3.1-lite-generate-preview:predictLongRunning",
		"gemini-ok",
		&geminiCalls,
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"http://proxy/clipal/models/veo-3.1-lite-generate-preview:predictLongRunning",
		bytes.NewReader([]byte(`{"instances":[]}`)),
	)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalResponsesRequestRecordsCapabilityInRuntime(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var codexCalls int32
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}

	snap := router.RuntimeSnapshot()
	lastRequest := snap.Clients[ClientOpenAI].LastRequest
	if lastRequest == nil {
		t.Fatalf("expected last request to be recorded")
	}

	field := reflect.ValueOf(*lastRequest).FieldByName("Capability")
	if !field.IsValid() {
		t.Fatalf("expected RequestOutcomeEvent to expose Capability")
	}
	if got := field.String(); got != "openai_responses" {
		t.Fatalf("capability: got %q want %q", got, "openai_responses")
	}
}

func TestUnknownEndpointSuggestsClipalIngress(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/unknown", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d want %d", rr.Result().StatusCode, http.StatusNotFound)
	}
	if got := rr.Body.String(); !bytes.Contains([]byte(got), []byte("/clipal")) {
		t.Fatalf("body: got %q want to mention /clipal", got)
	}
}

func TestClipalResponsesRoutingDoesNotShiftChatCursor(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()
	router.proxies[ClientOpenAI] = newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	var p1ResponsesCalls, p1ChatCalls, p2ResponsesCalls, p2ChatCalls int32
	router.proxies[ClientOpenAI].httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			switch r.URL.Path {
			case "/v1/responses":
				atomic.AddInt32(&p1ResponsesCalls, 1)
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("down"))),
				}, nil
			case "/v1/chat/completions":
				atomic.AddInt32(&p1ChatCalls, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("chat-p1"))),
				}, nil
			}
		case "p2":
			switch r.URL.Path {
			case "/v1/responses":
				atomic.AddInt32(&p2ResponsesCalls, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("responses-p2"))),
				}, nil
			case "/v1/chat/completions":
				atomic.AddInt32(&p2ChatCalls, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("chat-p2"))),
				}, nil
			}
		}
		return nil, io.ErrUnexpectedEOF
	})

	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr1, req1)
	if rr1.Result().StatusCode != http.StatusOK {
		t.Fatalf("responses status: got %d want %d body=%s", rr1.Result().StatusCode, http.StatusOK, rr1.Body.String())
	}
	if got := rr1.Body.String(); got != "responses-p2" {
		t.Fatalf("responses body: got %q want %q", got, "responses-p2")
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/chat/completions", bytes.NewReader([]byte(`{"x":2}`)))
	router.handleRequest(rr2, req2)
	if rr2.Result().StatusCode != http.StatusOK {
		t.Fatalf("chat status: got %d want %d body=%s", rr2.Result().StatusCode, http.StatusOK, rr2.Body.String())
	}
	if got := rr2.Body.String(); got != "chat-p1" {
		t.Fatalf("chat body: got %q want %q", got, "chat-p1")
	}
	if got := atomic.LoadInt32(&p1ResponsesCalls); got != 1 {
		t.Fatalf("p1 responses calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&p2ResponsesCalls); got != 1 {
		t.Fatalf("p2 responses calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&p1ChatCalls); got != 1 {
		t.Fatalf("p1 chat calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&p2ChatCalls); got != 0 {
		t.Fatalf("p2 chat calls: got %d want 0", got)
	}
}

func TestClipalImagesRequestUsesCodexPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/images/edits", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 0 {
		t.Fatalf("gemini calls: got %d want 0", got)
	}
}

func TestClipalAudioRequestUsesCodexPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/audio/transcriptions", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 0 {
		t.Fatalf("gemini calls: got %d want 0", got)
	}
}

func TestClipalFilesRequestUsesCodexPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1/files", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 0 {
		t.Fatalf("gemini calls: got %d want 0", got)
	}
}

func TestClipalGeminiRequestUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiV1RequestUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/models/gemini-2.5-pro:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiGenerateDoesNotShiftStreamCursor(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()
	router.proxies[ClientGemini] = newClientProxy(ClientGemini, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
		{Name: "p2", BaseURL: "http://p2", APIKey: "k2", Priority: 2},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	var p1GenerateCalls, p1StreamCalls, p2GenerateCalls, p2StreamCalls int32
	router.proxies[ClientGemini].httpClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "p1":
			switch r.URL.Path {
			case "/v1beta/models/gemini-2.5-pro:generateContent":
				atomic.AddInt32(&p1GenerateCalls, 1)
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("down"))),
				}, nil
			case "/v1beta/models/gemini-2.5-pro:streamGenerateContent":
				atomic.AddInt32(&p1StreamCalls, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("stream-p1"))),
				}, nil
			}
		case "p2":
			switch r.URL.Path {
			case "/v1beta/models/gemini-2.5-pro:generateContent":
				atomic.AddInt32(&p2GenerateCalls, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("generate-p2"))),
				}, nil
			case "/v1beta/models/gemini-2.5-pro:streamGenerateContent":
				atomic.AddInt32(&p2StreamCalls, 1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(bytes.NewReader([]byte("stream-p2"))),
				}, nil
			}
		}
		return nil, io.ErrUnexpectedEOF
	})

	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	router.handleRequest(rr1, req1)
	if rr1.Result().StatusCode != http.StatusOK {
		t.Fatalf("generate status: got %d want %d body=%s", rr1.Result().StatusCode, http.StatusOK, rr1.Body.String())
	}
	if got := rr1.Body.String(); got != "generate-p2" {
		t.Fatalf("generate body: got %q want %q", got, "generate-p2")
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1beta/models/gemini-2.5-pro:streamGenerateContent", bytes.NewReader([]byte(`{"contents":[]}`)))
	router.handleRequest(rr2, req2)
	if rr2.Result().StatusCode != http.StatusOK {
		t.Fatalf("stream status: got %d want %d body=%s", rr2.Result().StatusCode, http.StatusOK, rr2.Body.String())
	}
	if got := rr2.Body.String(); got != "stream-p1" {
		t.Fatalf("stream body: got %q want %q", got, "stream-p1")
	}
	if got := atomic.LoadInt32(&p1GenerateCalls); got != 1 {
		t.Fatalf("p1 generate calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&p2GenerateCalls); got != 1 {
		t.Fatalf("p2 generate calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&p1StreamCalls); got != 1 {
		t.Fatalf("p1 stream calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&p2StreamCalls); got != 0 {
		t.Fatalf("p2 stream calls: got %d want 0", got)
	}
}

func TestClipalModelsRequestUsesCodexPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1/models", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 0 {
		t.Fatalf("gemini calls: got %d want 0", got)
	}
}

func TestClipalGeminiModelsListUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1beta/models", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiModelGetUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1/models/gemini-2.5-flash-image", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiFilesRequestUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1beta/files", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiUploadFilesRequestUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/upload/v1beta/files", bytes.NewReader([]byte("file-bytes")))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiCachedContentsRequestUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1beta/cachedContents", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiTunedModelsRequestUsesGeminiPool(t *testing.T) {
	t.Parallel()

	router := newUnifiedIngressTestRouter()

	var claudeCalls, codexCalls, geminiCalls int32
	installMarkerTransport(router.proxies[ClientClaude], "claude", "claude-ok", &claudeCalls)
	installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)
	installMarkerTransport(router.proxies[ClientGemini], "gemini", "gemini-ok", &geminiCalls)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://proxy/clipal/v1beta/tunedModels", nil)
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "gemini-ok" {
		t.Fatalf("body: got %q want %q", got, "gemini-ok")
	}
	if got := atomic.LoadInt32(&claudeCalls); got != 0 {
		t.Fatalf("claude calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 0 {
		t.Fatalf("codex calls: got %d want 0", got)
	}
	if got := atomic.LoadInt32(&geminiCalls); got != 1 {
		t.Fatalf("gemini calls: got %d want 1", got)
	}
}

func TestClipalGeminiPlatformRESTPathsUseGeminiPool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		urlPath  string
		wantPath string
	}{
		{
			name:     "files register bare collection method",
			method:   http.MethodPost,
			urlPath:  "/clipal/files:register",
			wantPath: "/v1beta/files:register",
		},
		{
			name:     "batches bare resource method",
			method:   http.MethodPost,
			urlPath:  "/clipal/batches/batch-1:cancel",
			wantPath: "/v1beta/batches/batch-1:cancel",
		},
		{
			name:     "batch cancel",
			method:   http.MethodPost,
			urlPath:  "/clipal/v1beta/batches/batch-1:cancel",
			wantPath: "/v1beta/batches/batch-1:cancel",
		},
		{
			name:     "operations bare",
			method:   http.MethodGet,
			urlPath:  "/clipal/operations/op-1",
			wantPath: "/v1beta/operations/op-1",
		},
		{
			name:     "model scoped operation",
			method:   http.MethodGet,
			urlPath:  "/clipal/v1beta/models/veo-3.1-lite-generate-preview/operations/op-1",
			wantPath: "/v1beta/models/veo-3.1-lite-generate-preview/operations/op-1",
		},
		{
			name:     "vertex operation item",
			method:   http.MethodGet,
			urlPath:  "/clipal/v1/projects/proj/locations/us-central1/operations/op-1",
			wantPath: "/v1/projects/proj/locations/us-central1/operations/op-1",
		},
		{
			name:     "vertex operations collection",
			method:   http.MethodGet,
			urlPath:  "/clipal/v1/projects/proj/locations/us-central1/operations",
			wantPath: "/v1/projects/proj/locations/us-central1/operations",
		},
		{
			name:     "file search stores bare",
			method:   http.MethodGet,
			urlPath:  "/clipal/fileSearchStores/store-1/documents/doc-1",
			wantPath: "/v1beta/fileSearchStores/store-1/documents/doc-1",
		},
		{
			name:     "generated files bare",
			method:   http.MethodGet,
			urlPath:  "/clipal/generatedFiles/file-1",
			wantPath: "/v1beta/generatedFiles/file-1",
		},
		{
			name:     "corpora bare",
			method:   http.MethodGet,
			urlPath:  "/clipal/corpora/corpus-1/documents/doc-1",
			wantPath: "/v1beta/corpora/corpus-1/documents/doc-1",
		},
		{
			name:     "auth tokens bare",
			method:   http.MethodPost,
			urlPath:  "/clipal/authTokens",
			wantPath: "/v1beta/authTokens",
		},
		{
			name:     "agents bare",
			method:   http.MethodGet,
			urlPath:  "/clipal/agents/agent-1",
			wantPath: "/v1beta/agents/agent-1",
		},
		{
			name:     "webhooks bare",
			method:   http.MethodPost,
			urlPath:  "/clipal/webhooks/hook-1",
			wantPath: "/v1beta/webhooks/hook-1",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := newUnifiedIngressTestRouter()
			var geminiCalls int32
			installPathAssertingTransport(router.proxies[ClientGemini], "gemini", tt.wantPath, "gemini-ok", &geminiCalls)

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "http://proxy"+tt.urlPath, bytes.NewReader([]byte(`{"x":1}`)))
			router.handleRequest(rr, req)

			if rr.Result().StatusCode != http.StatusOK {
				t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
			}
			if got := rr.Body.String(); got != "gemini-ok" {
				t.Fatalf("body: got %q want %q", got, "gemini-ok")
			}
			if got := atomic.LoadInt32(&geminiCalls); got != 1 {
				t.Fatalf("gemini calls: got %d want 1", got)
			}
		})
	}
}

func TestClipalAmbiguousOpenAIV1PathsStayOpenAICompatible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "bare files collection", path: "/clipal/files"},
		{name: "bare files item", path: "/clipal/files/file-1"},
		{name: "bare batches collection", path: "/clipal/batches"},
		{name: "bare batches item", path: "/clipal/batches/batch-1"},
		{name: "versioned files", path: "/clipal/v1/files"},
		{name: "versioned batches", path: "/clipal/v1/batches"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := newUnifiedIngressTestRouter()
			var codexCalls int32
			installMarkerTransport(router.proxies[ClientOpenAI], "codex", "codex-ok", &codexCalls)

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "http://proxy"+tt.path, nil)
			router.handleRequest(rr, req)

			if rr.Result().StatusCode != http.StatusOK {
				t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
			}
			if got := rr.Body.String(); got != "codex-ok" {
				t.Fatalf("body: got %q want %q", got, "codex-ok")
			}
			if got := atomic.LoadInt32(&codexCalls); got != 1 {
				t.Fatalf("codex calls: got %d want 1", got)
			}
		})
	}
}

func TestCompatibilityAliasUnknownSubpathStillForwards(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "codex", BaseURL: "http://codex", APIKey: "k-codex", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	var codexCalls int32
	installMarkerTransport(cp, "codex", "codex-ok", &codexCalls)

	router := &Router{
		cfg: &config.Config{
			Global: config.GlobalConfig{
				ListenAddr:      "127.0.0.1",
				Port:            3333,
				LogLevel:        config.LogLevelDebug,
				ReactivateAfter: "1h",
				MaxRequestBody:  32 * 1024 * 1024,
			},
		},
		proxies: map[ClientType]*ClientProxy{
			ClientOpenAI: cp,
		},
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/codex/v1/images", bytes.NewReader([]byte(`{"x":1}`)))
	router.handleRequest(rr, req)

	if rr.Result().StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d body=%s", rr.Result().StatusCode, http.StatusOK, rr.Body.String())
	}
	if got := rr.Body.String(); got != "codex-ok" {
		t.Fatalf("body: got %q want %q", got, "codex-ok")
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex calls: got %d want 1", got)
	}
}
