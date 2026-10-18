package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	oauthpkg "github.com/PAIArtCom/Clipal/internal/oauth"
)

func TestResolveAntigravityModelUsesCatalogAndThinking(t *testing.T) {
	budget := func(n float64) *float64 { return &n }
	catalog := map[string]antigravityModel{
		"nebula-flash-low":    {ThinkingBudget: budget(1000), ThinkingLevel: "low"},
		"nebula-flash-medium": {ThinkingBudget: budget(4000), ThinkingLevel: "medium"},
		"nebula-flash-high":   {ThinkingBudget: budget(-1), ThinkingLevel: "high"},
		"nebula-flash-tiered": {ThinkingBudget: budget(-1), ThinkingLevel: "tiered"},
		"nebula-lite":         {},
	}
	for _, tc := range []struct {
		name, model, want string
		thinking          map[string]any
		fail              bool
	}{
		{name: "budget from agy", model: "nebula-flash", thinking: map[string]any{"thinkingBudget": float64(1000)}, want: "nebula-flash-low"},
		{name: "explicit level", model: "nebula-flash", thinking: map[string]any{"thinkingLevel": "MEDIUM"}, want: "nebula-flash-medium"},
		{name: "exact ID wins", model: "nebula-flash-low", thinking: map[string]any{"thinkingBudget": float64(4000)}, want: "nebula-flash-low"},
		{name: "preview alias exists", model: "nebula-lite-preview", want: "nebula-lite"},
		{name: "unknown preview unchanged", model: "unknown-preview", want: "unknown-preview"},
		{name: "unknown model unchanged", model: "unknown", thinking: map[string]any{"thinkingBudget": float64(1000)}, want: "unknown"},
		{name: "no silent default", model: "nebula-flash", want: "nebula-flash"},
		{name: "ambiguous budget", model: "nebula-flash", thinking: map[string]any{"thinkingBudget": float64(-1)}, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAntigravityModel(tc.model, tc.thinking, catalog)
			if (err != nil) != tc.fail || (!tc.fail && got != tc.want) {
				t.Fatalf("got=%q err=%v want=%q", got, err, tc.want)
			}
		})
	}
}

func newAntigravityCatalogProxy(t *testing.T, handler http.HandlerFunc) (*ClientProxy, *oauthpkg.Credential) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := oauthpkg.NewAntigravityClient()
	client.DailyCloudCodeURL = server.URL
	svc := oauthpkg.NewService(t.TempDir(), oauthpkg.WithAntigravityClient(client))
	cred := &oauthpkg.Credential{Ref: "catalog-account", Provider: config.OAuthProviderAntigravity, Email: "catalog@example.com", AccessToken: "test-token", ExpiresAt: time.Now().Add(time.Hour), Metadata: map[string]string{"project_id": "catalog-project"}}
	if err := svc.Store().Save(cred); err != nil {
		t.Fatal(err)
	}
	cp := newClientProxy(ClientGoogle, config.ClientModeAuto, "", []config.Provider{{Name: "catalog", AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: config.OAuthProviderAntigravity, OAuthRef: cred.Ref, Priority: 1}}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.oauth = svc
	t.Cleanup(cp.Close)
	return cp, cred
}

func TestAntigravityCatalogResolvesAllGenerationPathsAndPreservesPayload(t *testing.T) {
	var calls atomic.Int32
	cp, cred := newAntigravityCatalogProxy(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1internal:fetchAvailableModels" {
			t.Errorf("unexpected catalog request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("catalog did not use account credentials")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["project"] != "catalog-project" {
			t.Errorf("catalog project=%v err=%v", body["project"], err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":{"future-flash-low":{"supportsThinking":true,"thinkingBudget":1000},"future-flash-high":{"supportsThinking":true,"thinkingBudget":-1}}}`)
	})
	for _, action := range []string{"generateContent", "streamGenerateContent", "countTokens"} {
		body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"thinkingConfig":{"includeThoughts":true,"thinkingBudget":1000}}}`)
		path := "/v1beta/models/future-flash:" + action
		req := httptest.NewRequest(http.MethodPost, "http://local/clipal"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		prepared, err := cp.createProxyRequest(req, cp.providers[0], "", path, body)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		envelope := decodeRequestBodyMap(t, prepared)
		nested, ok := envelope["request"].(map[string]any)
		if !ok {
			t.Fatalf("missing request object: %#v", envelope)
		}
		if action == "countTokens" {
			if nested["model"] != "models/future-flash-low" {
				t.Fatalf("countTokens model=%v", nested["model"])
			}
		} else if envelope["model"] != "future-flash-low" {
			t.Fatalf("%s model=%v", action, envelope["model"])
		}
		thinking := antigravityThinkingConfig(nested)
		if thinking["thinkingBudget"] != float64(1000) || thinking["includeThoughts"] != true {
			t.Fatalf("thinking changed: %#v", thinking)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("catalog requests=%d want shared cache", calls.Load())
	}
	// Token and transport changes must not reuse account-specific model metadata.
	changed := cred.Clone()
	changed.AccessToken = "rotated-token"
	if _, err := cp.antigravityModelCatalog(context.Background(), changed, 0, cp.oauthHTTPClientForProvider(cp.providers[0], 0)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("token change reused old catalog: %d", calls.Load())
	}
	if _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("transport change reused old catalog: %d", calls.Load())
	}
	cp.mu.Lock()
	for _, call := range cp.antigravityCatalogs {
		call.expiresAt = time.Now().Add(-time.Second)
	}
	cp.mu.Unlock()
	if _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("expired catalog reused: %d", calls.Load())
	}
}

func TestAntigravityCatalogCoalescesFetchAndCancelsWaiter(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cp, cred := newAntigravityCatalogProxy(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"models":{}}`)
	})
	result := make(chan error, 1)
	go func() { _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); result <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cp.antigravityModelCatalog(ctx, cred, 0, nil)
	close(release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err=%v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAntigravityCatalogDoesNotCacheFailures(t *testing.T) {
	var calls atomic.Int32
	cp, cred := newAntigravityCatalogProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"models":{}}`)
	})
	if _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); err == nil {
		t.Fatal("expected discovery failure")
	}
	if _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := cp.antigravityModelCatalog(context.Background(), cred, 0, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 {
		t.Fatalf("unexpected catalog retry/cache behavior: %d calls", calls.Load())
	}
}

func TestAntigravityGeminiFieldNormalizationPreservesSourceAndThinking(t *testing.T) {
	for _, control := range []string{`"thinking_level":" LOW ","thinkingLevel":" HIGH "`, `"thinking_budget":2000,"thinkingBudget":1000`} {
		raw := []byte(`{"system_instruction":{"parts":[{"text":"system"}]},"generation_config":{"thinking_config":{` + control + `,"include_thoughts":false},"response_json_schema":{"type":"object"}},"request_id":"alias-id","requestId":"id-1","user_prompt_id":"prompt-1","request_type":"agent","enabled_credit_types":["test"]}`)
		var source map[string]any
		if err := json.Unmarshal(raw, &source); err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(source)
		_, out, err := buildAntigravityOAuthRequestFromRoot(CapabilityGeminiGenerateContent, "/v1beta/models/nebula:generateContent", source, "project")
		if err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(source)
		if !bytes.Equal(before, after) {
			t.Fatal("normalization mutated the payload shared by failover attempts")
		}
		var envelope map[string]any
		if err := json.Unmarshal(out, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope["requestId"] != "id-1" || envelope["requestType"] != "agent" || envelope["userPromptId"] != "prompt-1" || envelope["enabledCreditTypes"] == nil {
			t.Fatalf("incorrect native envelope: %s", out)
		}
		request, ok := envelope["request"].(map[string]any)
		if !ok {
			t.Fatalf("missing request object: %#v", envelope)
		}
		if request["generation_config"] != nil || request["system_instruction"] != nil || request["systemInstruction"] == nil {
			t.Fatalf("incorrect Gemini field normalization: %s", out)
		}
		thinking := antigravityThinkingConfig(request)
		if thinking["includeThoughts"] != false {
			t.Fatalf("visibility changed: %#v", thinking)
		}
		if strings.Contains(control, "thinking_level") && thinking["thinkingLevel"] != "high" {
			t.Fatalf("level=%v", thinking)
		}
		if strings.Contains(control, "thinking_budget") && thinking["thinkingBudget"] != float64(1000) {
			t.Fatalf("budget=%v", thinking)
		}
		generation, ok := request["generationConfig"].(map[string]any)
		if !ok {
			t.Fatalf("missing generation config: %#v", request)
		}
		if generation["responseSchema"] == nil || generation["response_json_schema"] != nil {
			t.Fatal("response schema alias not normalized")
		}
	}
}
