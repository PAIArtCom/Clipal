package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestProviderTestRecoversOnlyAfterCompletedGeneration(t *testing.T) {
	var reply atomic.Int32
	var generateCalls atomic.Int32
	var injectFailure atomic.Bool
	var beforeReply func()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer key-two" {
			t.Error("both steps must use the selected key")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method + " " + req.URL.Path {
		case "GET /v1/models":
			fmt.Fprint(w, `{"data":[{"id":"other"},{"id":"chosen"},{"id":"chosen"}]}`)
		case "POST /v1/chat/completions":
			generateCalls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["model"] != "chosen" {
				t.Errorf("test did not use the chosen model: %v, %v", body, err)
			}
			if injectFailure.Load() {
				beforeReply()
			}
			switch reply.Load() {
			case 0:
				w.WriteHeader(http.StatusForbidden)
			case 1:
				fmt.Fprint(w, `{"choices":[{"finish_reason":null}]}`)
			default:
				fmt.Fprint(w, `{"choices":[{"finish_reason":"stop"}]}`)
			}
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	breaker := circuitBreakerConfig{enabled: true, failureThreshold: 1, successThreshold: 2, openTimeout: time.Hour, halfOpenMaxInFlight: 1}
	overrideModel := "override"
	cp := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "target", BaseURL: upstream.URL, APIKeys: []string{"key-one", "key-two"}, Overrides: &config.ProviderOverrides{Model: &overrideModel}},
		{Name: "backup", BaseURL: upstream.URL, APIKey: "backup"},
	}, time.Hour, time.Second, time.Second, breaker, config.GlobalUpstreamProxyModeDirect, "")
	t.Cleanup(cp.Close)
	router := &Router{proxies: map[ClientType]*ClientProxy{ClientOpenAI: cp}}
	ctx := t.Context()
	now := time.Now()
	cp.deactivateKeyFor(0, 0, "auth", 401, "", time.Hour)
	cp.deactivateKeyFor(0, 1, "auth", 401, "", time.Hour)
	cp.deactivateFor(0, "auth", 401, "", time.Hour)
	cp.deactivateFor(1, "billing", 402, "", time.Hour)
	cp.recordCircuitFailure(now, 0, false, "server")
	cp.markProviderBusy(0, "overloaded", 1, now, time.Hour)
	binding := stickyBinding{ProviderIndex: 1, KeyIndex: 0, BoundAt: now, LastSeenAt: now}
	cp.stickyBindings["existing-session"] = binding
	cp.currentIndex, cp.responsesIndex = 1, 1
	cp.lastRequest = RequestOutcomeEvent{At: now, Provider: "backup"}

	catalog, err := router.FetchProviderTestModels(ctx, ClientOpenAI, "target", 2)
	if err != nil || catalog.Key != 2 || !slices.Equal(catalog.Models, []string{"chosen", "other"}) {
		t.Fatalf("catalog = %+v, %v", catalog, err)
	}
	if generateCalls.Load() != 0 || !cp.isDeactivated(0) {
		t.Fatal("model discovery must wait for model selection and leave cooldown in place")
	}
	if _, err := router.RunProviderTest(ctx, ClientOpenAI, "target", catalog.ID, "unlisted", ""); !errors.Is(err, ErrProviderTestModel) {
		t.Fatalf("unlisted model error = %v", err)
	}
	for _, expected := range []string{"HTTP 403", "did not return a completed generation"} {
		_, err := router.RunProviderTest(ctx, ClientOpenAI, "target", catalog.ID, "chosen", "chat")
		if err == nil || !strings.Contains(err.Error(), expected) || !cp.isDeactivated(0) {
			t.Fatalf("failed generation must preserve cooldown, error = %v", err)
		}
		var failure *ProviderTestFailure
		if expected == "HTTP 403" && (!errors.As(err, &failure) || failure.UpstreamStatus != 403 || failure.Reason != "auth") {
			t.Fatalf("expected structured authentication failure, got %#v", err)
		}
		reply.Add(1)
	}
	beforeReply = func() {
		if _, err := router.FetchProviderTestModels(ctx, ClientOpenAI, "target", 2); !errors.Is(err, ErrProviderTestBusy) {
			t.Errorf("concurrent test error = %v", err)
		}
		cp.deactivateKeyFor(0, 1, "auth", 401, "second real failure", time.Hour)
	}
	injectFailure.Store(true)
	result, err := router.RunProviderTest(ctx, ClientOpenAI, "target", catalog.ID, "chosen", "chat")
	if err != nil || result.Recovered || result.Restored || !cp.isDeactivated(0) {
		t.Fatalf("a concurrent failure must win over the test, got %+v, %v", result, err)
	}
	injectFailure.Store(false)
	catalog, err = router.FetchProviderTestModels(ctx, ClientOpenAI, "target", 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err = router.RunProviderTest(ctx, ClientOpenAI, "target", catalog.ID, "chosen", "chat")
	if err != nil || !result.Recovered || !result.Restored || cp.isDeactivated(0) || !cp.isDeactivated(1) || !cp.isKeyDeactivated(0, 0) || cp.isKeyDeactivated(0, 1) {
		t.Fatalf("only the tested provider and key should recover, got %+v, %v", result, err)
	}
	if state, _ := cp.breakers[0].snapshot(time.Now()); state != circuitClosed || !cp.providerBusySnapshot(0).Until.IsZero() {
		t.Fatal("provider breaker and busy backoff did not recover")
	}
	if cp.currentIndex != 1 || cp.responsesIndex != 1 || cp.stickyBindings["existing-session"] != binding || cp.lastRequest.Provider != "backup" {
		t.Fatal("provider test modified the business routing or request history")
	}
}

func TestProviderTestOpenAIAPIUsesResponsesByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.Method + " " + req.URL.Path {
		case "GET /v1/models":
			fmt.Fprint(w, `{"data":[{"id":"chosen"}]}`)
		case "POST /v1/responses":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["model"] != "chosen" || body["input"] != "Reply with OK." {
				t.Errorf("unexpected Responses probe payload: %v, %v", body, err)
			}
			fmt.Fprint(w, `{"status":"completed"}`)
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	cp := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "target", BaseURL: upstream.URL, APIKey: "test-key"},
	}, time.Hour, time.Second, time.Second, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeDirect, "")
	t.Cleanup(cp.Close)
	router := &Router{proxies: map[ClientType]*ClientProxy{ClientOpenAI: cp}}
	ctx := t.Context()

	catalog, err := router.FetchProviderTestModels(ctx, ClientOpenAI, "target", 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := router.RunProviderTest(ctx, ClientOpenAI, "target", catalog.ID, "chosen", "")
	if err != nil || !result.Recovered {
		t.Fatalf("default Responses probe = %+v, %v", result, err)
	}
}

func TestProviderTestErrorDetail(t *testing.T) {
	const key = "configured-test-credential"
	for _, tc := range []struct{ name, body, want string }{
		{"message", `{"error":{"message":"Credential configured-test-credential and Bearer token-123 rejected"}}`, "Credential [redacted] and Bearer [redacted] rejected"},
		{"raw body", "<html>Unexpected upstream diagnostic</html>", ""},
		{"long message", `{"error":{"message":"` + strings.Repeat("错", 600) + `"}}`, strings.Repeat("错", 512) + "…"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(upstream.Close)
			cp := newClientProxyWithGlobalProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
				{Name: "target", BaseURL: upstream.URL, APIKey: key},
			}, time.Hour, time.Second, time.Second, circuitBreakerConfig{}, config.GlobalUpstreamProxyModeDirect, "")
			t.Cleanup(cp.Close)
			router := &Router{proxies: map[ClientType]*ClientProxy{ClientOpenAI: cp}}
			_, err := router.FetchProviderTestModels(t.Context(), ClientOpenAI, "target", 0)
			var failure *ProviderTestFailure
			if !errors.As(err, &failure) || failure.Reason != "auth" || failure.Detail != tc.want {
				t.Fatalf("unexpected catalog failure: %#v", err)
			}
		})
	}
}

func TestProviderTestResponsesStreamRequiresSuccessEvent(t *testing.T) {
	req := withRequestContext(httptest.NewRequest(http.MethodPost, "/v1/responses", nil), requestContextForClientPath(ClientOpenAI, "/v1/responses", false))
	for _, scenario := range []struct {
		name  string
		event string
		ok    bool
	}{
		{"completed", "response.completed", true},
		{"failed", "response.failed", false},
		{"truncated", "response.output_text.delta", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: %s\ndata: {\"type\":\"%s\"}\n\n", scenario.event, scenario.event)
			if err := verifyProviderTestStream(ClientOpenAI, req, w.Result()); (err == nil) != scenario.ok {
				t.Fatalf("event %q returned %v", scenario.event, err)
			}
		})
	}
}

func TestProviderTestCodexOAuthUsesNativeCatalogAndResponses(t *testing.T) {
	cp := newCodexModelsTestProxy(t, "")
	t.Cleanup(cp.Close)
	cp.httpClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Authorization") != "Bearer access-1" {
			t.Fatalf("missing the OAuth account authorization on %s", req.URL.Path)
		}
		switch req.URL.Path {
		case "/backend-api/codex/models":
			return newResponse(http.StatusOK, http.Header{"Content-Type": {"application/json"}}, `{"models":[{"slug":"listed-model"}]}`), nil
		case "/backend-api/codex/responses":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["model"] != "listed-model" || body["stream"] != true {
				t.Fatalf("unexpected OAuth probe payload: %v, %v", body, err)
			}
			return newResponse(http.StatusOK, http.Header{"Content-Type": {"text/event-stream"}}, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"), nil
		default:
			t.Fatalf("unexpected Codex endpoint %s", req.URL.Path)
			return nil, nil
		}
	})
	router := &Router{proxies: map[ClientType]*ClientProxy{ClientOpenAI: cp}}
	ctx := t.Context()
	catalog, err := router.FetchProviderTestModels(ctx, ClientOpenAI, "codex-oauth", 0)
	if err != nil || !slices.Equal(catalog.Models, []string{"listed-model"}) || catalog.Key != 0 {
		t.Fatalf("OAuth catalog = %+v, %v", catalog, err)
	}
	result, err := router.RunProviderTest(ctx, ClientOpenAI, "codex-oauth", catalog.ID, "listed-model", "")
	if err != nil || !result.Recovered || result.Restored || !cp.lastAccountAttempt.At.IsZero() {
		t.Fatalf("OAuth test result = %+v, %v, account attempt = %+v", result, err, cp.lastAccountAttempt)
	}
}
