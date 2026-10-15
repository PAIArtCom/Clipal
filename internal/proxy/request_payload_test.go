package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestRequestPayloadCachesCodexOAuthRewrite(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)
	payload := newRequestPayload(body)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType: ClientOpenAI,
		Family:     ProtocolFamilyOpenAI,
		Capability: CapabilityOpenAIResponses,
	}
	provider := config.Provider{
		Name: "codex",
	}

	_, _, first, err := payload.codexOAuthRequest(req, requestCtx, provider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest first: %v", err)
	}
	_, _, second, err := payload.codexOAuthRequest(req, requestCtx, provider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest second: %v", err)
	}
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatalf("expected cached codex rewrite body to be reused")
	}
}

func TestRequestPayloadCachesCodexOAuthProviderOverridesSeparately(t *testing.T) {
	body := []byte(`{"model":"gpt-5.2","stream":false,"input":"hello"}`)
	payload := newRequestPayload(body)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType: ClientOpenAI,
		Family:     ProtocolFamilyOpenAI,
		Capability: CapabilityOpenAIResponses,
	}
	firstProvider := config.Provider{
		Name: "codex",
		Overrides: &config.ProviderOverrides{
			Model: strPtr("gpt-5.2-first"),
		},
	}
	secondProvider := config.Provider{
		Name: "codex",
		Overrides: &config.ProviderOverrides{
			Model: strPtr("gpt-5.2-second"),
		},
	}

	_, _, first, err := payload.codexOAuthRequest(req, requestCtx, firstProvider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest first provider: %v", err)
	}
	_, _, second, err := payload.codexOAuthRequest(req, requestCtx, secondProvider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest second provider: %v", err)
	}
	_, _, firstAgain, err := payload.codexOAuthRequest(req, requestCtx, firstProvider, "/v1/responses")
	if err != nil {
		t.Fatalf("codexOAuthRequest first provider again: %v", err)
	}
	if len(first) == 0 || len(firstAgain) == 0 || &first[0] != &firstAgain[0] {
		t.Fatalf("expected first provider cached codex rewrite body to be reused")
	}
	if len(second) == 0 || &first[0] == &second[0] {
		t.Fatalf("expected provider override rewrites to use separate cache entries")
	}

	firstRoot := decodeRawJSONMap(t, first)
	secondRoot := decodeRawJSONMap(t, second)
	if got := firstRoot["model"]; got != "gpt-5.2-first" {
		t.Fatalf("first model = %v", got)
	}
	if got := secondRoot["model"]; got != "gpt-5.2-second" {
		t.Fatalf("second model = %v", got)
	}
}

func TestRequestPayloadCachesGeminiOAuthRewrite(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	payload := newRequestPayload(body)
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1beta/models/gemini-2.5-pro:generateContent", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	requestCtx := RequestContext{
		ClientType: ClientGemini,
		Family:     ProtocolFamilyGemini,
		Capability: CapabilityGeminiGenerateContent,
	}
	provider := config.Provider{Name: "gemini"}

	_, _, first, err := payload.geminiOAuthRequest(req, requestCtx, provider, "/v1beta/models/gemini-2.5-pro:generateContent", "project-1")
	if err != nil {
		t.Fatalf("geminiOAuthRequest first: %v", err)
	}
	_, _, second, err := payload.geminiOAuthRequest(req, requestCtx, provider, "/v1beta/models/gemini-2.5-pro:generateContent", "project-1")
	if err != nil {
		t.Fatalf("geminiOAuthRequest second: %v", err)
	}
	if len(first) == 0 || len(second) == 0 || &first[0] != &second[0] {
		t.Fatalf("expected cached gemini rewrite body to be reused")
	}
}

func decodeRawJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("json.Unmarshal: %v body=%s", err, string(body))
	}
	return root
}

// oauthPayloadCase shares the request preparation entry points between behavioral
// checks and benchmarks; no HTTP server or credentials are needed.
type oauthPayloadCase struct {
	name    string
	ctx     RequestContext
	path    string
	body    string
	prepare func(*requestPayload, *http.Request, RequestContext, config.Provider, string) (string, string, []byte, error)
	build   func(string, []byte) (string, string, []byte, error)
}

func oauthPayloadCases() []oauthPayloadCase {
	return []oauthPayloadCase{
		{
			name: "codex",
			ctx:  RequestContext{ClientType: ClientOpenAI, Family: ProtocolFamilyOpenAI, Capability: CapabilityOpenAIResponses},
			path: "/v1/responses",
			body: `{"model":"gpt-5.2","stream":false,"input":"PROMPT","reasoning":{"effort":"low"}}`,
			prepare: func(p *requestPayload, r *http.Request, c RequestContext, provider config.Provider, path string) (string, string, []byte, error) {
				target, stream, body, err := p.codexOAuthRequest(r, c, provider, path)
				return target, strconv.FormatBool(stream), body, err
			},
			build: func(path string, body []byte) (string, string, []byte, error) {
				target, stream, out, err := buildCodexOAuthRequest(path, body)
				return target, strconv.FormatBool(stream), out, err
			},
		},
		{
			name: "gemini",
			ctx:  RequestContext{ClientType: ClientGemini, Family: ProtocolFamilyGemini, Capability: CapabilityGeminiGenerateContent},
			path: "/v1beta/models/gemini-2.5-pro:generateContent",
			body: `{"contents":[{"role":"user","parts":[{"text":"PROMPT"}]}]}`,
			prepare: func(p *requestPayload, r *http.Request, c RequestContext, provider config.Provider, path string) (string, string, []byte, error) {
				return p.geminiOAuthRequest(r, c, provider, path, "project-1")
			},
			build: func(path string, body []byte) (string, string, []byte, error) {
				return buildGeminiOAuthRequest(CapabilityGeminiGenerateContent, path, body, "project-1")
			},
		},
		{
			name: "antigravity",
			ctx:  RequestContext{ClientType: ClientGemini, Family: ProtocolFamilyGemini, Capability: CapabilityGeminiGenerateContent},
			path: "/v1beta/models/gemini-2.5-pro:generateContent",
			body: `{"contents":[{"role":"user","parts":[{"text":"PROMPT"}]}],"request_id":"request-1"}`,
			prepare: func(p *requestPayload, r *http.Request, c RequestContext, provider config.Provider, path string) (string, string, []byte, error) {
				target, body, err := p.antigravityOAuthRequest(r, c, provider, path, "project-1")
				return target, "", body, err
			},
			build: func(path string, body []byte) (string, string, []byte, error) {
				target, out, err := buildAntigravityOAuthRequest(CapabilityGeminiGenerateContent, path, body, "project-1")
				return target, "", out, err
			},
		},
	}
}

func TestOAuthPayloadPreparationParity(t *testing.T) {
	for _, tc := range oauthPayloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, variant := range []struct{ name, contentType, body string }{
				{"json", "application/json", tc.body},
				{"suffix", "application/example+json", tc.body},
				{"fallback", "text/plain", tc.body},
				{"missing_type", "", tc.body},
				{"invalid", "application/json", `{"broken":`},
				{"empty", "application/json", ""},
				{"array", "application/json", "[]"},
				{"null", "application/json", "null"},
			} {
				t.Run(variant.name, func(t *testing.T) {
					body := []byte(variant.body)
					originalBody := bytes.Clone(body)
					req := httptest.NewRequest(http.MethodPost, "http://proxy"+tc.path, bytes.NewReader(body))
					req.Header.Set("Content-Type", variant.contentType)
					p := newRequestPayload(body)
					for _, model := range []string{"gpt-5.2-first", "gpt-5.2-second"} {
						provider := config.Provider{Name: tc.name, Overrides: &config.ProviderOverrides{Model: strPtr(model)}}
						wantPath, wantMeta, wantBody, wantErr := tc.build(tc.path, newRequestPayload(body).providerBody(req, tc.ctx, provider))
						gotPath, gotMeta, gotBody, gotErr := tc.prepare(p, req, tc.ctx, provider, tc.path)
						if gotPath != wantPath || gotMeta != wantMeta || !bytes.Equal(gotBody, wantBody) || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
							t.Fatalf("preparation differs from byte builder: got (%q, %q, %s, %v), want (%q, %q, %s, %v)", gotPath, gotMeta, gotBody, gotErr, wantPath, wantMeta, wantBody, wantErr)
						}
						_, _, cached, cachedErr := tc.prepare(p, req, tc.ctx, provider, tc.path)
						if fmt.Sprint(cachedErr) != fmt.Sprint(gotErr) || !bytes.Equal(cached, gotBody) || (len(gotBody) > 0 && &cached[0] != &gotBody[0]) {
							t.Fatal("retry did not reuse the prepared result")
						}
					}
					if !bytes.Equal(p.Body(), originalBody) {
						t.Fatal("original body was modified")
					}
				})
			}
		})
	}
}

// BenchmarkOAuthPayload measures initial preparation separately from a cached
// retry. The payload is fresh for every first-call iteration; request setup is
// excluded. Sizes refer to prompt text, not the small surrounding JSON envelope.
func BenchmarkOAuthPayload(b *testing.B) {
	for _, tc := range oauthPayloadCases() {
		for _, size := range []int{1024, 256 * 1024} {
			for _, mode := range []string{"first", "retry"} {
				b.Run(fmt.Sprintf("%s/%d/%s", tc.name, size, mode), func(b *testing.B) {
					body := []byte(strings.Replace(tc.body, "PROMPT", strings.Repeat("x", size), 1))
					req := httptest.NewRequest(http.MethodPost, "http://proxy"+tc.path, bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					provider := config.Provider{Name: tc.name}
					p := newRequestPayload(body)
					if _, _, _, err := tc.prepare(p, req, tc.ctx, provider, tc.path); err != nil {
						b.Fatal(err)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						if mode == "first" {
							p = newRequestPayload(body)
						}
						if _, _, _, err := tc.prepare(p, req, tc.ctx, provider, tc.path); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
