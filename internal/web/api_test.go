package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/logger"
	oauthpkg "github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/proxy"
	"github.com/lansespirit/Clipal/internal/telemetry"
	"github.com/lansespirit/Clipal/internal/testutil"
)

func TestHandleGetGlobalConfig_ReturnsSnakeCase(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/config/global", nil)
	w := httptest.NewRecorder()
	api.HandleGetGlobalConfig(w, req)
	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if _, ok := got["listen_addr"]; !ok {
		t.Fatalf("expected listen_addr in response, got keys=%v", keys(got))
	}
	if _, ok := got["ListenAddr"]; ok {
		t.Fatalf("did not expect ListenAddr in response")
	}
	ntf, ok := got["notifications"].(map[string]any)
	if !ok {
		t.Fatalf("expected notifications object, got %T", got["notifications"])
	}
	if _, ok := ntf["min_level"]; !ok {
		t.Fatalf("expected notifications.min_level, got keys=%v", keys(ntf))
	}
	if _, ok := ntf["MinLevel"]; ok {
		t.Fatalf("did not expect notifications.MinLevel")
	}
	routing, ok := got["routing"].(map[string]any)
	if !ok {
		t.Fatalf("expected routing object, got %T", got["routing"])
	}
	sticky, ok := routing["sticky_sessions"].(map[string]any)
	if !ok {
		t.Fatalf("expected routing.sticky_sessions object, got %T", routing["sticky_sessions"])
	}
	if _, ok := sticky["explicit_ttl"]; !ok {
		t.Fatalf("expected routing.sticky_sessions.explicit_ttl, got keys=%v", keys(sticky))
	}
	if _, ok := routing["StickySessions"]; ok {
		t.Fatalf("did not expect routing.StickySessions")
	}
	if v, ok := got["log_stdout"]; !ok || (v != true && v != false) {
		t.Fatalf("expected log_stdout boolean, got %v (%T)", got["log_stdout"], got["log_stdout"])
	}
	if _, ok := got["ignore_count_tokens_failover"]; ok {
		t.Fatalf("did not expect ignore_count_tokens_failover in response")
	}
}

func TestHandleGetProviders_RedactsAPIKey_AndReturnsArray(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://example.com
    api_key: secret
    proxy_mode: custom
    proxy_url: http://proxy.internal:7890
    model: gpt-5.4
    reasoning_effort: high
    priority: 2
  - name: p2
    base_url: https://example2.com
    api_key: secret2
    priority: 1
    enabled: false
`), 0600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if got == nil {
		t.Fatalf("expected JSON array, got null")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(got))
	}
	if _, ok := got[0]["api_key"]; ok {
		t.Fatalf("did not expect api_key in provider listing")
	}
	if _, ok := got[0]["APIKey"]; ok {
		t.Fatalf("did not expect APIKey in provider listing")
	}
	if _, ok := got[0]["base_url"]; !ok {
		t.Fatalf("expected base_url in provider listing, got keys=%v", keys(got[0]))
	}
	if _, ok := got[0]["proxy_url"]; ok {
		t.Fatalf("did not expect proxy_url in provider listing")
	}
	if got[0]["key_count"] != float64(1) {
		t.Fatalf("expected key_count=1, got %v", got[0]["key_count"])
	}
	var first map[string]any
	for _, provider := range got {
		if provider["name"] == "p1" {
			first = provider
			break
		}
	}
	if first == nil {
		t.Fatalf("expected provider p1 in listing, got %#v", got)
	}
	if first["proxy_mode"] != "custom" {
		t.Fatalf("expected proxy_mode=custom, got %v", first["proxy_mode"])
	}
	if first["proxy_url_hint"] != "http://proxy.internal:7890" {
		t.Fatalf("expected proxy_url_hint redaction, got %v", first["proxy_url_hint"])
	}
	overrides, ok := first["overrides"].(map[string]any)
	if !ok {
		t.Fatalf("expected overrides object in listing, got %T", first["overrides"])
	}
	if overrides["model"] != "gpt-5.4" {
		t.Fatalf("expected model override in listing, got %v", overrides["model"])
	}
	openaiOver, ok := overrides["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai override in listing, got %T", overrides["openai"])
	}
	if openaiOver["reasoning_effort"] != "high" {
		t.Fatalf("expected reasoning_effort override in listing, got %v", openaiOver["reasoning_effort"])
	}
	if _, ok := first["usage"]; ok {
		t.Fatalf("did not expect usage in provider listing without telemetry data")
	}
}

func TestHandleGetProviders_IncludesUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://example.com
    api_key: secret
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	if err := api.telemetry.RecordUsage("openai", "p1", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{InputTokens: 11, OutputTokens: 13}, Usage: map[string]any{"prompt_tokens": 11.0, "completion_tokens": 13.0, "total_tokens": 24.0}}, time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	usage, ok := got[0]["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage object, got %#v", got[0]["usage"])
	}
	if usage["has_usage"] != true {
		t.Fatalf("expected usage.has_usage=true, got %#v", usage["has_usage"])
	}
	if usage["total_tokens"] != float64(24) {
		t.Fatalf("usage.total_tokens = %v", usage["total_tokens"])
	}
	if usage["input_tokens"] != float64(11) || usage["output_tokens"] != float64(13) {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestMapProviderUsageResponse_IncludesSpendRollups(t *testing.T) {
	now := time.Now().UTC()
	resp := mapProviderUsageResponse(telemetry.ProviderUsage{
		RequestCount:    2,
		SuccessCount:    2,
		ReasoningTokens: 11,
		ThoughtsTokens:  22,
		TotalCostMicros: 11_250_000,
		HasCost:         true,
		DailyCosts: map[string]telemetry.DailyCostBucket{
			now.Format("2006-01-02"): {
				CostMicros: 1_250_000,
				HasCost:    true,
			},
			now.AddDate(0, 0, -6).Format("2006-01-02"): {
				CostMicros: 3_000_000,
				HasCost:    true,
			},
			now.AddDate(0, 0, -8).Format("2006-01-02"): {
				CostMicros: 7_000_000,
				HasCost:    true,
			},
		},
		LastUsedAt: now,
	})
	if resp == nil {
		t.Fatalf("expected usage response")
	}
	if !resp.HasCost {
		t.Fatalf("expected has_cost=true: %#v", resp)
	}
	if resp.ReasoningTokens != 11 || resp.ThoughtsTokens != 22 {
		t.Fatalf("expected reasoning/thought tokens: %#v", resp)
	}
	if resp.SpendTodayMicros != 1_250_000 {
		t.Fatalf("spend_today_micros = %d", resp.SpendTodayMicros)
	}
	if resp.SpendWeekMicros != 4_250_000 {
		t.Fatalf("spend_week_micros = %d", resp.SpendWeekMicros)
	}
}

func TestHandleGetProviders_IncludesRequestOnlyUsageWithoutTokenUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://example.com
    api_key: secret
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	if err := api.telemetry.RecordUsage("openai", "p1", telemetry.UsageSnapshot{}, time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	usage, ok := got[0]["usage"].(map[string]any)
	if !ok {
		t.Fatalf("expected usage object, got %#v", got[0]["usage"])
	}
	if usage["request_count"] != float64(1) || usage["success_count"] != float64(1) {
		t.Fatalf("usage = %#v", usage)
	}
	if _, ok := usage["has_usage"]; ok {
		t.Fatalf("did not expect usage.has_usage for request-only telemetry, got %#v", usage["has_usage"])
	}
	if _, ok := usage["total_tokens"]; ok {
		t.Fatalf("did not expect token totals for request-only telemetry, got %#v", usage["total_tokens"])
	}
}

func TestNewAPI_LogsTelemetryLoadFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	var warned []string
	loggerHook := func(levelStr string, message string) {
		if levelStr == "WARN" {
			warned = append(warned, message)
		}
	}
	logger.SetHook(loggerHook)
	defer logger.SetHook(nil)

	api := NewAPI(dir, "test", nil)
	if api == nil {
		t.Fatalf("expected api")
	}
	if len(warned) == 0 {
		t.Fatalf("expected warning for telemetry load failure")
	}
	if !strings.Contains(warned[0], "failed to load usage telemetry") {
		t.Fatalf("warning = %q", warned[0])
	}
}

func TestHandleExportConfig_IncludesAPIKey_SnakeCase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://example.com
    api_key: secret
    model: gpt-5.4
    reasoning_effort: medium
    priority: 1
`), 0600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/config/export", nil)
	w := httptest.NewRecorder()
	api.HandleExportConfig(w, req)
	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	openAIObj, ok := got["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai object, got %T", got["openai"])
	}
	providers, ok := openAIObj["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("expected openai.providers array of len 1, got %T len=%d", openAIObj["providers"], len(providers))
	}
	p0, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("expected provider object, got %T", providers[0])
	}
	if p0["api_key"] != "secret" {
		t.Fatalf("expected api_key=secret, got %v", p0["api_key"])
	}
	if _, ok := p0["base_url"]; !ok {
		t.Fatalf("expected base_url in export, got keys=%v", keys(p0))
	}
	if _, ok := p0["BaseURL"]; ok {
		t.Fatalf("did not expect BaseURL in export")
	}
	overrides, ok := p0["overrides"].(map[string]any)
	if !ok {
		t.Fatalf("expected overrides object in export, got %T", p0["overrides"])
	}
	if overrides["model"] != "gpt-5.4" {
		t.Fatalf("expected model override in export, got %v", overrides["model"])
	}
	openaiOver, ok := overrides["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai override in export, got %T", overrides["openai"])
	}
	if openaiOver["reasoning_effort"] != "medium" {
		t.Fatalf("expected reasoning_effort in export, got %v", openaiOver["reasoning_effort"])
	}
}

func TestHandleExportConfig_OAuthProviderMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex_acct_123
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/config/export", nil)
	w := httptest.NewRecorder()
	api.HandleExportConfig(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	openAIObj, ok := got["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai object, got %T", got["openai"])
	}
	providers, ok := openAIObj["providers"].([]any)
	if !ok || len(providers) != 1 {
		t.Fatalf("expected openai.providers array of len 1, got %T len=%d", openAIObj["providers"], len(providers))
	}
	p0, ok := providers[0].(map[string]any)
	if !ok {
		t.Fatalf("expected provider object, got %T", providers[0])
	}
	if p0["auth_type"] != "oauth" {
		t.Fatalf("expected auth_type=oauth, got %v", p0["auth_type"])
	}
	if p0["oauth_provider"] != "codex" {
		t.Fatalf("expected oauth_provider=codex, got %v", p0["oauth_provider"])
	}
	if p0["oauth_ref"] != "codex_acct_123" {
		t.Fatalf("expected oauth_ref=codex_acct_123, got %v", p0["oauth_ref"])
	}
	if _, ok := p0["api_key"]; ok {
		t.Fatalf("did not expect api_key in oauth export")
	}
	if _, ok := p0["api_keys"]; ok {
		t.Fatalf("did not expect api_keys in oauth export")
	}
}

func TestHandleUpdateGlobalConfig_AcceptsSnakeCaseNotifications(t *testing.T) {
	for _, proxyURL := range []string{"http://127.0.0.1:7890", "socks5://127.0.0.1:1080"} {
		t.Run(proxyURL, func(t *testing.T) {
			dir := t.TempDir()
			api := NewAPI(dir, "test", nil)

			body := []byte(`{
  "listen_addr": "127.0.0.1",
  "port": 3333,
  "log_level": "info",
  "reactivate_after": "10m",
  "upstream_idle_timeout": "1m",
  "response_header_timeout": "30s",
  "upstream_proxy_mode": "custom",
  "upstream_proxy_url": "` + proxyURL + `",
  "max_request_body_bytes": 12345,
  "log_dir": "",
  "log_retention_days": 7,
  "log_stdout": true,
  "notifications": {
    "enabled": true,
    "min_level": "warn",
    "provider_switch": false
  },
  "routing": {
    "sticky_sessions": {
      "enabled": true,
      "explicit_ttl": "45m"
    },
    "busy_backpressure": {
      "enabled": true,
      "short_retry_after_max": "5s",
      "max_inline_wait": "12s"
    }
  },
  "circuit_breaker": {
    "failure_threshold": 4,
    "success_threshold": 2,
    "open_timeout": "60s",
    "half_open_max_inflight": 1
  }
}`)

			req := httptest.NewRequest(http.MethodPut, "/api/config/global/update", bytes.NewReader(body))
			w := httptest.NewRecorder()
			api.HandleUpdateGlobalConfig(w, req)
			res := w.Result()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.StatusCode, w.Body.String())
			}

			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatalf("reload config: %v", err)
			}
			if !cfg.Global.Notifications.Enabled {
				t.Fatalf("expected notifications.enabled=true")
			}
			if cfg.Global.Notifications.MinLevel != config.LogLevelWarn {
				t.Fatalf("expected notifications.min_level=warn, got %q", cfg.Global.Notifications.MinLevel)
			}
			if cfg.Global.Notifications.ProviderSwitch == nil || *cfg.Global.Notifications.ProviderSwitch {
				t.Fatalf("expected notifications.provider_switch=false, got %v", cfg.Global.Notifications.ProviderSwitch)
			}
			if !cfg.Global.Routing.StickySessions.Enabled {
				t.Fatalf("expected routing.sticky_sessions.enabled=true")
			}
			if cfg.Global.Routing.StickySessions.ExplicitTTL != "45m" {
				t.Fatalf("expected routing.sticky_sessions.explicit_ttl=45m, got %q", cfg.Global.Routing.StickySessions.ExplicitTTL)
			}
			if !cfg.Global.Routing.BusyBackpressure.Enabled {
				t.Fatalf("expected routing.busy_backpressure.enabled=true")
			}
			if cfg.Global.Routing.BusyBackpressure.ShortRetryAfterMax != "5s" {
				t.Fatalf("expected routing.busy_backpressure.short_retry_after_max=5s, got %q", cfg.Global.Routing.BusyBackpressure.ShortRetryAfterMax)
			}
			if cfg.Global.Routing.BusyBackpressure.MaxInlineWait != "12s" {
				t.Fatalf("expected routing.busy_backpressure.max_inline_wait=12s, got %q", cfg.Global.Routing.BusyBackpressure.MaxInlineWait)
			}
			if cfg.Global.NormalizedUpstreamProxyMode() != config.GlobalUpstreamProxyModeCustom {
				t.Fatalf("expected upstream_proxy_mode=custom, got %q", cfg.Global.NormalizedUpstreamProxyMode())
			}
			if cfg.Global.NormalizedUpstreamProxyURL() != proxyURL {
				t.Fatalf("expected upstream_proxy_url to be saved, got %q", cfg.Global.NormalizedUpstreamProxyURL())
			}
		})
	}
}

func TestHandleUpdateGlobalConfig_AllowsClearingRoutingStrings(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)

	initial := config.DefaultGlobalConfig()
	initial.Routing.StickySessions.ExplicitTTL = "45m"
	initial.Routing.BusyBackpressure.ShortRetryAfterMax = "5s"
	initial.Routing.BusyBackpressure.MaxInlineWait = "12s"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), formatGlobalConfigYAML(initial), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	body := []byte(`{
  "listen_addr": "127.0.0.1",
  "port": 3333,
  "log_level": "info",
  "reactivate_after": "10m",
  "upstream_idle_timeout": "1m",
  "response_header_timeout": "30s",
  "upstream_proxy_mode": "direct",
  "upstream_proxy_url": "",
  "max_request_body_bytes": 12345,
  "log_dir": "",
  "log_retention_days": 7,
  "log_stdout": true,
  "notifications": {
    "enabled": true,
    "min_level": "warn",
    "provider_switch": false
  },
  "routing": {
    "sticky_sessions": {
      "enabled": true,
      "explicit_ttl": ""
    },
    "busy_backpressure": {
      "enabled": true,
      "short_retry_after_max": "",
      "max_inline_wait": ""
    }
  },
  "circuit_breaker": {
    "failure_threshold": 4,
    "success_threshold": 2,
    "open_timeout": "60s",
    "half_open_max_inflight": 1
  }
}`)

	req := httptest.NewRequest(http.MethodPut, "/api/config/global/update", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleUpdateGlobalConfig(w, req)
	res := w.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Global.Routing.StickySessions.ExplicitTTL != "" {
		t.Fatalf("expected routing.sticky_sessions.explicit_ttl to be cleared, got %q", cfg.Global.Routing.StickySessions.ExplicitTTL)
	}
	if cfg.Global.Routing.BusyBackpressure.ShortRetryAfterMax != "" {
		t.Fatalf("expected routing.busy_backpressure.short_retry_after_max to be cleared, got %q", cfg.Global.Routing.BusyBackpressure.ShortRetryAfterMax)
	}
	if cfg.Global.Routing.BusyBackpressure.MaxInlineWait != "" {
		t.Fatalf("expected routing.busy_backpressure.max_inline_wait to be cleared, got %q", cfg.Global.Routing.BusyBackpressure.MaxInlineWait)
	}
	if cfg.Global.NormalizedUpstreamProxyMode() != config.GlobalUpstreamProxyModeDirect {
		t.Fatalf("expected upstream_proxy_mode to be direct, got %q", cfg.Global.NormalizedUpstreamProxyMode())
	}
	if cfg.Global.NormalizedUpstreamProxyURL() != "" {
		t.Fatalf("expected upstream_proxy_url to be cleared, got %q", cfg.Global.NormalizedUpstreamProxyURL())
	}
}

func TestHandleAddProvider_AcceptsAPIKeys(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)

	body := []byte(`{
  "name": "p1",
  "base_url": "https://example.com",
  "api_keys": ["key1", "key2"],
  "overrides": {
    "model": "gpt-5.4",
    "openai": {
      "reasoning_effort": "high"
    }
  },
  "priority": 1,
  "enabled": true
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/codex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleAddProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := cfg.OpenAI.Providers[0].KeyCount(); got != 2 {
		t.Fatalf("key count: got %d want %d", got, 2)
	}
	if cfg.OpenAI.Providers[0].APIKey != "" {
		t.Fatalf("expected multi-key provider to be persisted via api_keys")
	}
	if got := cfg.OpenAI.Providers[0].NormalizedProxyMode(); got != config.ProviderProxyModeDefault {
		t.Fatalf("proxy_mode = %q, want %q", got, config.ProviderProxyModeDefault)
	}
	if got := cfg.OpenAI.Providers[0].ModelOverride(); got != "gpt-5.4" {
		t.Fatalf("model = %q", got)
	}
	if got := cfg.OpenAI.Providers[0].OpenAIReasoningEffort(); got != "high" {
		t.Fatalf("reasoning_effort = %q", got)
	}
}

func TestHandleGetProviders_OAuthMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex_acct_123
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(got))
	}
	if got[0]["auth_type"] != "oauth" {
		t.Fatalf("expected auth_type=oauth, got %v", got[0]["auth_type"])
	}
	if got[0]["oauth_provider"] != "codex" {
		t.Fatalf("expected oauth_provider=codex, got %v", got[0]["oauth_provider"])
	}
	if got[0]["oauth_ref"] != "codex_acct_123" {
		t.Fatalf("expected oauth_ref=codex_acct_123, got %v", got[0]["oauth_ref"])
	}
	if got[0]["oauth_auth_status"] != "reauth_needed" {
		t.Fatalf("expected oauth_auth_status=reauth_needed, got %v", got[0]["oauth_auth_status"])
	}
	if _, ok := got[0]["api_key"]; ok {
		t.Fatalf("did not expect api_key in provider listing")
	}
	if _, ok := got[0]["api_keys"]; ok {
		t.Fatalf("did not expect api_keys in provider listing")
	}
}

func TestHandleGetProviders_OAuthMetadata_EnrichesCredentialStatus(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	lastRefresh := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Second)
	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    expiresAt,
		LastRefresh:  lastRefresh,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(got))
	}
	if got[0]["display_name"] != "sean@example.com" {
		t.Fatalf("display_name = %v, want sean@example.com", got[0]["display_name"])
	}
	if got[0]["oauth_auth_status"] != "ready" {
		t.Fatalf("oauth_auth_status = %v, want ready", got[0]["oauth_auth_status"])
	}
	if got[0]["oauth_expires_at"] != expiresAt.Format(time.RFC3339) {
		t.Fatalf("oauth_expires_at = %v, want %s", got[0]["oauth_expires_at"], expiresAt.Format(time.RFC3339))
	}
	if got[0]["oauth_last_refresh"] != lastRefresh.Format(time.RFC3339) {
		t.Fatalf("oauth_last_refresh = %v, want %s", got[0]["oauth_last_refresh"], lastRefresh.Format(time.RFC3339))
	}
}

func TestHandleGetProviders_OAuthMetadata_DoesNotEagerlyLoadCodexPlanAndLimits(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("did not expect usage endpoint to be called during provider list load")
	}))
	defer usageServer.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	api.oauth = oauthpkg.NewService(
		dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     "https://auth.openai.com/oauth/token",
			UsageURL:     usageServer.URL + "/wham/usage",
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   usageServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)

	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "account-123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(2 * time.Hour),
		LastRefresh:  now.Add(-30 * time.Minute),
		Metadata: map[string]string{
			"id_token": testOAuthJWTWithPlan("sean@example.com", "account-123", "free"),
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(got))
	}
	if _, ok := got[0]["oauth_plan_type"]; ok {
		t.Fatalf("did not expect oauth_plan_type in eager provider response")
	}
	if _, ok := got[0]["oauth_rate_limits"]; ok {
		t.Fatalf("did not expect oauth_rate_limits in eager provider response")
	}
}

func TestHandleGetProviderOAuthMetadata_IncludesCodexPlanAndLimits(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	weeklyReset := now.Add(7 * 24 * time.Hour)
	reviewReset := now.Add(6 * 24 * time.Hour)
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/wham/usage" {
			t.Fatalf("path = %s, want /wham/usage", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("authorization = %q", got)
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "account-123" {
			t.Fatalf("chatgpt-account-id = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{
  "plan_type": "free",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 100,
      "limit_window_seconds": 604800,
      "reset_after_seconds": 3600,
      "reset_at": %d
    }
  },
  "additional_rate_limits": [
    {
      "limit_name": "Code review",
      "metered_feature": "code_review",
      "rate_limit": {
        "allowed": true,
        "limit_reached": false,
        "primary_window": {
          "used_percent": 75,
          "limit_window_seconds": 604800,
          "reset_after_seconds": 7200,
          "reset_at": %d
        }
      }
    }
  ]
}`, weeklyReset.Unix(), reviewReset.Unix()))
	}))
	defer usageServer.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	api.oauth = oauthpkg.NewService(
		dir,
		oauthpkg.WithNowFunc(func() time.Time { return now }),
		oauthpkg.WithCodexClient(&oauthpkg.CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     "https://auth.openai.com/oauth/token",
			UsageURL:     usageServer.URL + "/wham/usage",
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   usageServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)

	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "account-123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(24 * time.Hour), // Set far future expiration to avoid refresh, // Set far future expiration to avoid refresh
		LastRefresh:  now.Add(-30 * time.Minute),
		Metadata: map[string]string{
			"id_token": testOAuthJWTWithPlan("sean@example.com", "account-123", "free"),
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai/codex-sean-example-com/oauth-metadata", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviderOAuthMetadata(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if got["oauth_plan_type"] != "free" {
		t.Fatalf("oauth_plan_type = %v, want free", got["oauth_plan_type"])
	}
	limits, ok := got["oauth_rate_limits"].(map[string]any)
	if !ok {
		t.Fatalf("expected oauth_rate_limits object, got %#v", got["oauth_rate_limits"])
	}
	primary, ok := limits["primary"].(map[string]any)
	if !ok {
		t.Fatalf("expected primary rate limit, got %#v", limits["primary"])
	}
	if primary["used_percent"] != float64(100) {
		t.Fatalf("primary.used_percent = %v, want 100", primary["used_percent"])
	}
	if primary["window_minutes"] != float64(10080) {
		t.Fatalf("primary.window_minutes = %v, want 10080", primary["window_minutes"])
	}
	if primary["resets_at"] != weeklyReset.Format(time.RFC3339) {
		t.Fatalf("primary.resets_at = %v, want %s", primary["resets_at"], weeklyReset.Format(time.RFC3339))
	}
	additional, ok := limits["additional"].([]any)
	if !ok || len(additional) != 1 {
		t.Fatalf("expected one additional rate limit, got %#v", limits["additional"])
	}
	review, ok := additional[0].(map[string]any)
	if !ok {
		t.Fatalf("expected additional limit object, got %#v", additional[0])
	}
	if review["limit_id"] != "code_review" {
		t.Fatalf("limit_id = %v, want code_review", review["limit_id"])
	}
	if review["limit_name"] != "Code review" {
		t.Fatalf("limit_name = %v, want Code review", review["limit_name"])
	}
	reviewPrimary, ok := review["primary"].(map[string]any)
	if !ok {
		t.Fatalf("expected review primary window, got %#v", review["primary"])
	}
	if reviewPrimary["used_percent"] != float64(75) {
		t.Fatalf("review.primary.used_percent = %v, want 75", reviewPrimary["used_percent"])
	}
	if reviewPrimary["resets_at"] != reviewReset.Format(time.RFC3339) {
		t.Fatalf("review.primary.resets_at = %v, want %s", reviewPrimary["resets_at"], reviewReset.Format(time.RFC3339))
	}
}

func TestHandleGetProviders_OAuthMetadata_RefreshDue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().UTC().Add(-10 * time.Minute),
		LastRefresh:  time.Now().UTC().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/providers/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\nbody=%s", err, w.Body.String())
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(got))
	}
	if got[0]["oauth_auth_status"] != "refresh_due" {
		t.Fatalf("oauth_auth_status = %v, want refresh_due", got[0]["oauth_auth_status"])
	}
}

func TestHandleAddProvider_RejectsOAuthAuthorizationFlowShortcut(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)

	body := []byte(`{
  "name": "codex-sean-example-com",
  "auth_type": "oauth",
  "oauth_provider": "codex",
  "oauth_ref": "codex_acct_123",
  "priority": 1,
  "enabled": true
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/codex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleAddProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleAddProvider_RejectsMixedOAuthAndAPIKeySources(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)

	body := []byte(`{
  "name": "codex-sean-example-com",
  "base_url": "https://api.openai.com",
  "auth_type": "oauth",
  "oauth_provider": "codex",
  "oauth_ref": "codex_acct_123",
  "api_key": "secret",
  "priority": 1
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/codex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleAddProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleAddProvider_RejectsUnsupportedOverrideFieldsForGemini(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)

	body := []byte(`{
  "name": "g1",
  "base_url": "https://gemini.example",
  "api_key": "gemini-key",
  "overrides": {
    "model": "gemini-2.5-pro",
    "openai": {
      "reasoning_effort": "high"
    },
    "claude": {
      "thinking_budget_tokens": 4096
    }
  },
  "priority": 1,
  "enabled": true
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/gemini", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleAddProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleAddProvider_AutoAssignsNextPriority(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    priority: 1
  - name: p2
    base_url: https://two.example
    api_key: key2
    priority: 2
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	body := []byte(`{
  "name": "p3",
  "base_url": "https://three.example",
  "api_key": "key3",
  "enabled": true
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/providers/codex", bytes.NewReader(body))
	w := httptest.NewRecorder()
	api.HandleAddProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 3 {
		t.Fatalf("providers len = %d, want 3", len(cfg.OpenAI.Providers))
	}
	if got := cfg.OpenAI.Providers[2].Priority; got != 3 {
		t.Fatalf("priority = %d, want 3", got)
	}
}

func TestHandleAddProvider_AcceptsCustomProxy(t *testing.T) {
	for _, proxyURL := range []string{"http://127.0.0.1:7890", "socks5://127.0.0.1:1080"} {
		t.Run(proxyURL, func(t *testing.T) {
			dir := t.TempDir()
			api := NewAPI(dir, "test", nil)

			body := []byte(`{
  "name": "p1",
  "base_url": "https://example.com",
  "api_key": "key1",
  "proxy_mode": "custom",
  "proxy_url": "` + proxyURL + `",
  "priority": 1,
  "enabled": true
}`)

			req := httptest.NewRequest(http.MethodPost, "/api/providers/codex", bytes.NewReader(body))
			w := httptest.NewRecorder()
			api.HandleAddProvider(w, req)
			if w.Result().StatusCode != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
			}

			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if got := cfg.OpenAI.Providers[0].NormalizedProxyMode(); got != config.ProviderProxyModeCustom {
				t.Fatalf("proxy_mode = %q", got)
			}
			if got := cfg.OpenAI.Providers[0].NormalizedProxyURL(); got != proxyURL {
				t.Fatalf("proxy_url = %q", got)
			}
		})
	}
}

func TestHandleAddProvider_RejectsProxyURLWithoutCustomMode(t *testing.T) {
	for _, mode := range []string{"direct", "default"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			api := NewAPI(dir, "test", nil)

			body := []byte(`{
  "name": "p1",
  "base_url": "https://example.com",
  "api_key": "key1",
  "proxy_mode": "` + mode + `",
  "proxy_url": "http://127.0.0.1:7890",
  "priority": 1,
  "enabled": true
}`)

			req := httptest.NewRequest(http.MethodPost, "/api/providers/codex", bytes.NewReader(body))
			w := httptest.NewRecorder()
			api.HandleAddProvider(w, req)
			if w.Result().StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
			}

			got := testutil.DecodeJSONMap(t, w.Body.Bytes())
			if got["error"] != "proxy_url requires proxy_mode custom" {
				t.Fatalf("error = %#v", got["error"])
			}

			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if len(cfg.OpenAI.Providers) != 0 {
				t.Fatalf("providers len = %d, want 0", len(cfg.OpenAI.Providers))
			}
		})
	}
}

func TestHandleUpdateProvider_ProxySettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://example.com
    api_key: key1
    proxy_mode: custom
    proxy_url: http://127.0.0.1:7890
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)

	t.Run("retain existing custom proxy url", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{
  "proxy_mode": "custom"
}`)))
		w := httptest.NewRecorder()
		api.HandleUpdateProvider(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
		}

		cfg, err := config.Load(dir)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if got := cfg.OpenAI.Providers[0].NormalizedProxyURL(); got != "http://127.0.0.1:7890" {
			t.Fatalf("proxy_url = %q", got)
		}
	})

	t.Run("switch to direct clears proxy url", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{
  "proxy_mode": "direct"
}`)))
		w := httptest.NewRecorder()
		api.HandleUpdateProvider(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
		}

		cfg, err := config.Load(dir)
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if got := cfg.OpenAI.Providers[0].NormalizedProxyMode(); got != config.ProviderProxyModeDirect {
			t.Fatalf("proxy_mode = %q", got)
		}
		if got := cfg.OpenAI.Providers[0].NormalizedProxyURL(); got != "" {
			t.Fatalf("proxy_url = %q, want empty", got)
		}
	})

	t.Run("reject proxy url without mode", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{
  "proxy_url": "http://127.0.0.1:8899"
}`)))
		w := httptest.NewRecorder()
		api.HandleUpdateProvider(w, req)
		if w.Result().StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
		}
	})

	t.Run("reject proxy url without custom mode", func(t *testing.T) {
		for _, mode := range []string{"direct", "default"} {
			t.Run(mode, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
providers:
  - name: p1
    base_url: https://example.com
    api_key: key1
    proxy_mode: custom
    proxy_url: http://127.0.0.1:7890
    priority: 1
`), 0o600); err != nil {
					t.Fatal(err)
				}

				api := NewAPI(dir, "test", nil)
				req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{
  "proxy_mode": "`+mode+`",
  "proxy_url": "http://127.0.0.1:8899"
}`)))
				w := httptest.NewRecorder()
				api.HandleUpdateProvider(w, req)
				if w.Result().StatusCode != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
				}

				got := testutil.DecodeJSONMap(t, w.Body.Bytes())
				if got["error"] != "proxy_url requires proxy_mode custom" {
					t.Fatalf("error = %#v", got["error"])
				}

				cfg, err := config.Load(dir)
				if err != nil {
					t.Fatalf("load config: %v", err)
				}
				if got := cfg.OpenAI.Providers[0].NormalizedProxyMode(); got != config.ProviderProxyModeCustom {
					t.Fatalf("proxy_mode = %q, want %q", got, config.ProviderProxyModeCustom)
				}
				if got := cfg.OpenAI.Providers[0].NormalizedProxyURL(); got != "http://127.0.0.1:7890" {
					t.Fatalf("proxy_url = %q", got)
				}
			})
		}
	})
}

func TestHandleGetClientConfig_ReturnsConfiguredModeAndPin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: manual
pinned_provider: p1
providers:
  - name: p1
    base_url: https://example.com
    api_key: secret
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/client-config/openai", nil)
	w := httptest.NewRecorder()
	api.HandleGetClientConfig(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["mode"] != "manual" || got["pinned_provider"] != "p1" {
		t.Fatalf("body=%#v", got)
	}
	support, ok := got["override_support"].(map[string]any)
	if !ok {
		t.Fatalf("expected override_support in response, got %#v", got)
	}
	openAI, ok := support["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai override support in response, got %#v", support)
	}
	claude, ok := support["claude"].(map[string]any)
	if !ok {
		t.Fatalf("expected claude override support in response, got %#v", support)
	}
	if support["model"] != true || openAI["reasoning_effort"] != true || claude["thinking_budget_tokens"] != false {
		t.Fatalf("override_support=%#v", support)
	}
}

func TestHandleGetClientConfig_ReturnsOverrideSupportForGemini(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gemini.yaml"), []byte(`
mode: auto
providers:
  - name: g1
    base_url: https://gemini.example
    api_key: key1
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/client-config/gemini", nil)
	w := httptest.NewRecorder()
	api.HandleGetClientConfig(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	support, ok := got["override_support"].(map[string]any)
	if !ok {
		t.Fatalf("expected override_support in response, got %#v", got)
	}
	openAI, ok := support["openai"].(map[string]any)
	if !ok {
		t.Fatalf("expected openai override support in response, got %#v", support)
	}
	claude, ok := support["claude"].(map[string]any)
	if !ok {
		t.Fatalf("expected claude override support in response, got %#v", support)
	}
	if support["model"] != false || openAI["reasoning_effort"] != false || claude["thinking_budget_tokens"] != false {
		t.Fatalf("override_support=%#v", support)
	}
}

func TestHandleUpdateClientConfig_SavesChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://example.com
    api_key: secret
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test", nil)
	req := httptest.NewRequest(http.MethodPut, "/api/client-config/codex", bytes.NewReader([]byte(`{"mode":"manual","pinned_provider":"p1"}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateClientConfig(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.OpenAI.Mode != config.ClientModeManual || cfg.OpenAI.PinnedProvider != "p1" {
		t.Fatalf("codex cfg = %#v", cfg.OpenAI)
	}
}

func TestHandleUpdateProvider_Paths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    priority: 1
  - name: p2
    base_url: https://two.example
    api_key: key2
    priority: 2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	t.Run("rename conflict", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{"name":"p2"}`)))
		w := httptest.NewRecorder()
		api.HandleUpdateProvider(w, req)
		if w.Result().StatusCode != http.StatusConflict {
			t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/missing", bytes.NewReader([]byte(`{"base_url":"https://new.example"}`)))
		w := httptest.NewRecorder()
		api.HandleUpdateProvider(w, req)
		if w.Result().StatusCode != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		if err := api.telemetry.RecordUsage("openai", "p1", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{InputTokens: 5, OutputTokens: 7}, Usage: map[string]any{"prompt_tokens": 5.0, "completion_tokens": 7.0}}, time.Now()); err != nil {
			t.Fatalf("RecordUsage: %v", err)
		}
		req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{
  "name":"p3",
  "base_url":"https://three.example",
  "api_keys":["k3","k4"],
  "overrides":{
    "model":"gpt-5.4-mini",
    "openai":{
      "reasoning_effort":"low"
    }
  },
  "priority":5,
  "enabled":false
}`)))
		w := httptest.NewRecorder()
		api.HandleUpdateProvider(w, req)
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
		}

		cfg, err := config.Load(dir)
		if err != nil {
			t.Fatalf("config.Load: %v", err)
		}
		var updated *config.Provider
		for i := range cfg.OpenAI.Providers {
			if cfg.OpenAI.Providers[i].Name == "p3" {
				updated = &cfg.OpenAI.Providers[i]
				break
			}
		}
		if updated == nil {
			t.Fatalf("providers=%#v", cfg.OpenAI.Providers)
		}
		if updated.BaseURL != "https://three.example" {
			t.Fatalf("provider=%#v", updated)
		}
		if updated.Priority != 5 {
			t.Fatalf("priority=%d", updated.Priority)
		}
		if updated.Enabled == nil || *updated.Enabled {
			t.Fatalf("enabled=%v", updated.Enabled)
		}
		if got := updated.KeyCount(); got != 2 {
			t.Fatalf("key count=%d", got)
		}
		if got := updated.ModelOverride(); got != "gpt-5.4-mini" {
			t.Fatalf("model=%q", got)
		}
		if got := updated.OpenAIReasoningEffort(); got != "low" {
			t.Fatalf("reasoning_effort=%q", got)
		}
		if _, ok := api.telemetry.ProviderSnapshot("openai", "p1"); ok {
			t.Fatalf("expected renamed provider usage to remove old name")
		}
		if got, ok := api.telemetry.ProviderSnapshot("openai", "p3"); !ok || got.TotalTokens != 12 {
			t.Fatalf("renamed usage snapshot = %#v ok=%v", got, ok)
		}
	})
}

func TestHandleUpdateProvider_RejectsOAuthCredentialSourceChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/p1", bytes.NewReader([]byte(`{
  "auth_type":"oauth",
  "oauth_provider":"codex",
  "oauth_ref":"codex_acct_123"
}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(cfg.OpenAI.Providers))
	}
	provider := cfg.OpenAI.Providers[0]
	if got := provider.NormalizedAuthType(); got != config.ProviderAuthTypeAPIKey {
		t.Fatalf("auth_type = %q, want %q", got, config.ProviderAuthTypeAPIKey)
	}
}

func TestHandleUpdateProvider_AllowsEnabledOnlyUpdateForOAuthProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/codex-sean-example-com", bytes.NewReader([]byte(`{
  "enabled": false
}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	provider := cfg.OpenAI.Providers[0]
	if provider.Enabled == nil || *provider.Enabled {
		t.Fatalf("enabled=%v", provider.Enabled)
	}
}

func TestHandleUpdateProvider_RejectsEditingExistingOAuthProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/codex-sean-example-com", bytes.NewReader([]byte(`{
  "base_url":"https://api.openai.com"
}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleUpdateProvider_RejectsRenamingExistingOAuthProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/codex-sean-example-com", bytes.NewReader([]byte(`{
  "name":"primary"
}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleUpdateProvider_ClearsOverrideFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    overrides:
      model: claude-sonnet-4-5
      claude:
        thinking_budget_tokens: 4096
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/claude/p1", bytes.NewReader([]byte(`{
  "overrides":{
    "model":"",
    "claude":{
      "thinking_budget_tokens":0
    }
  }
}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := cfg.Claude.Providers[0].ModelOverride(); got != "" {
		t.Fatalf("model = %q, want empty", got)
	}
	if got := cfg.Claude.Providers[0].ClaudeThinkingBudgetTokens(); got != 0 {
		t.Fatalf("thinking_budget_tokens = %d, want 0", got)
	}
	if cfg.Claude.Providers[0].Overrides != nil {
		t.Fatalf("expected overrides to be pruned after clearing, got %#v", cfg.Claude.Providers[0].Overrides)
	}
}

func TestHandleUpdateProvider_RejectsUnsupportedOverrideFieldsForGemini(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gemini.yaml"), []byte(`
mode: auto
providers:
  - name: g1
    base_url: https://gemini.example
    api_key: key1
    priority: 1
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/gemini/g1", bytes.NewReader([]byte(`{
  "overrides":{
    "model":"gemini-2.5-pro",
    "openai":{
      "reasoning_effort":"high"
    },
    "claude":{
      "thinking_budget_tokens":4096
    }
  }
}`)))
	w := httptest.NewRecorder()
	api.HandleUpdateProvider(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}
}

func TestHandleDeleteProvider_Paths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    priority: 1
  - name: p2
    base_url: https://two.example
    api_key: key2
    priority: 2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/providers/codex/missing", nil)
	w := httptest.NewRecorder()
	api.HandleDeleteProvider(w, req)
	if w.Result().StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/providers/codex/p1", nil)
	if err := api.telemetry.RecordUsage("openai", "p1", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{InputTokens: 3, OutputTokens: 4}, Usage: map[string]any{"prompt_tokens": 3.0, "completion_tokens": 4.0}}, time.Now()); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	w = httptest.NewRecorder()
	api.HandleDeleteProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 || cfg.OpenAI.Providers[0].Name != "p2" {
		t.Fatalf("providers=%#v", cfg.OpenAI.Providers)
	}
	if _, ok := api.telemetry.ProviderSnapshot("openai", "p1"); ok {
		t.Fatalf("expected usage snapshot for deleted provider to be removed")
	}
}

func TestHandleDeleteProvider_OAuthDeletesUnsharedCredential(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)
	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: manual
pinned_provider: codex-sean-example-com
providers:
  - name: codex-sean-example-com
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
  - name: backup
    base_url: https://api.openai.com
    api_key: key1
    priority: 2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := api.telemetry.RecordUsage("openai", "codex-sean-example-com", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{InputTokens: 1, OutputTokens: 2}}, time.Now()); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/providers/codex/codex-sean-example-com", nil)
	w := httptest.NewRecorder()
	api.HandleDeleteProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 || cfg.OpenAI.Providers[0].Name != "backup" {
		t.Fatalf("providers=%#v", cfg.OpenAI.Providers)
	}
	if cfg.OpenAI.Mode != config.ClientModeAuto {
		t.Fatalf("mode = %q, want auto", cfg.OpenAI.Mode)
	}
	if strings.TrimSpace(cfg.OpenAI.PinnedProvider) != "" {
		t.Fatalf("pinned_provider = %q, want empty", cfg.OpenAI.PinnedProvider)
	}
	if _, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com"); !os.IsNotExist(err) {
		t.Fatalf("Load err = %v, want oauth credential removed", err)
	}
	if _, ok := api.telemetry.ProviderSnapshot("openai", "codex-sean-example-com"); ok {
		t.Fatalf("expected usage snapshot for deleted oauth provider to be removed")
	}
}

func TestHandleDeleteProvider_OAuthKeepsSharedCredential(t *testing.T) {
	dir := t.TempDir()
	api := NewAPI(dir, "test", nil)
	if err := api.oauth.Store().Save(&oauthpkg.Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: primary
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 1
  - name: backup
    auth_type: oauth
    oauth_provider: codex
    oauth_ref: codex-sean-example-com
    priority: 2
`), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/providers/codex/primary", nil)
	w := httptest.NewRecorder()
	api.HandleDeleteProvider(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.OpenAI.Providers) != 1 || cfg.OpenAI.Providers[0].Name != "backup" {
		t.Fatalf("providers=%#v", cfg.OpenAI.Providers)
	}
	cred, err := api.oauth.Load(config.OAuthProviderCodex, "codex-sean-example-com")
	if err != nil {
		t.Fatalf("Load err = %v", err)
	}
	if got := cred.Email; got != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", got)
	}
}

func TestHandleReorderProviders_Paths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    priority: 1
  - name: p2
    base_url: https://two.example
    api_key: key2
    priority: 2
  - name: p3
    base_url: https://three.example
    api_key: key3
    priority: 3
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(dir, "test", nil)

	req := httptest.NewRequest(http.MethodPut, "/api/providers/codex/_reorder", bytes.NewReader([]byte(`{"providers":["missing"]}`)))
	w := httptest.NewRecorder()
	api.HandleReorderProviders(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/providers/codex/_reorder", bytes.NewReader([]byte(`{"providers":["p3","p1"]}`)))
	w = httptest.NewRecorder()
	api.HandleReorderProviders(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if got := []string{cfg.OpenAI.Providers[0].Name, cfg.OpenAI.Providers[1].Name, cfg.OpenAI.Providers[2].Name}; strings.Join(got, ",") != "p3,p1,p2" {
		t.Fatalf("providers order=%v", got)
	}
	if got := []int{cfg.OpenAI.Providers[0].Priority, cfg.OpenAI.Providers[1].Priority, cfg.OpenAI.Providers[2].Priority}; got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("priorities=%v", got)
	}
}

func TestHandleGetStatus_FallsBackToFirstEnabledProviderWithoutRuntime(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte(`
mode: auto
providers:
  - name: p1
    base_url: https://one.example
    api_key: key1
    priority: 1
  - name: p2
    base_url: https://two.example
    api_key: key2
    priority: 2
    enabled: false
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(dir, "test-version", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	api.HandleGetStatus(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Result().StatusCode, w.Body.String())
	}

	got := testutil.DecodeJSONMap(t, w.Body.Bytes())
	if got["version"] != "test-version" {
		t.Fatalf("body=%#v", got)
	}
	clients, ok := got["clients"].(map[string]any)
	if !ok {
		t.Fatalf("clients=%T %#v", got["clients"], got["clients"])
	}
	openAI, ok := clients["openai"].(map[string]any)
	if !ok {
		t.Fatalf("openai=%T %#v", clients["openai"], clients["openai"])
	}
	if openAI["current_provider"] != "p1" {
		t.Fatalf("openai status=%#v", openAI)
	}
	if openAI["provider_count"] != float64(2) {
		t.Fatalf("openai status=%#v", openAI)
	}
	if _, ok := openAI["current_providers"].(map[string]any); !ok {
		t.Fatalf("openai current_providers=%T %#v", openAI["current_providers"], openAI["current_providers"])
	}
}

func TestReorderProviders_PreservesUnmentioned_AndRejectsUnknown(t *testing.T) {
	in := []config.Provider{
		{Name: "a", Priority: 1},
		{Name: "b", Priority: 2},
		{Name: "c", Priority: 3},
	}
	out, err := reorderProviders(in, []string{"b"})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if len(out) != 3 || out[0].Name != "b" || out[1].Name != "a" || out[2].Name != "c" {
		t.Fatalf("unexpected reorder result: %+v", out)
	}
	if out[0].Priority != 1 || out[1].Priority != 2 || out[2].Priority != 3 {
		t.Fatalf("expected priorities normalized, got %+v", out)
	}

	if _, err := reorderProviders(in, []string{"nope"}); err == nil {
		t.Fatalf("expected error for unknown provider")
	}
}

func TestBuildClientStatus_IncludesLastRequestOutcome(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1},
		},
	}
	now := time.Date(2026, 3, 18, 16, 32, 24, 0, time.UTC)
	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p1",
		LastRequest: &proxy.RequestOutcomeEvent{
			At:       now,
			Provider: "p1",
			Status:   200,
			Delivery: "committed_complete",
			Protocol: "completed",
			Cause:    "",
			Bytes:    123,
		},
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.LastRequest == nil {
		t.Fatalf("expected last_request to be populated")
	}
	if got.LastRequest.Provider != "p1" {
		t.Fatalf("last_request.provider: got %q want %q", got.LastRequest.Provider, "p1")
	}
	if got.LastRequest.Delivery != "committed_complete" {
		t.Fatalf("last_request.delivery: got %q want %q", got.LastRequest.Delivery, "committed_complete")
	}
	if got.LastRequest.Protocol != "completed" {
		t.Fatalf("last_request.protocol: got %q want %q", got.LastRequest.Protocol, "completed")
	}
	if got.LastRequest.Bytes != 123 {
		t.Fatalf("last_request.bytes: got %d want %d", got.LastRequest.Bytes, 123)
	}
	if got.LastRequest.At != now.Format(time.RFC3339) {
		t.Fatalf("last_request.at: got %q want %q", got.LastRequest.At, now.Format(time.RFC3339))
	}
	if got.LastRequest.Result != "completed" {
		t.Fatalf("last_request.result: got %q want %q", got.LastRequest.Result, "completed")
	}
	if got.LastRequest.Label != "Completed via p1" {
		t.Fatalf("last_request.label: got %q want %q", got.LastRequest.Label, "Completed via p1")
	}
	if got.LastRequest.Detail == "" {
		t.Fatalf("expected last_request.detail to be populated")
	}
}

func TestBuildClientStatus_ReflectsLastRequestCapability(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1},
		},
	}
	now := time.Date(2026, 3, 18, 16, 32, 24, 0, time.UTC)
	lastRequest := &proxy.RequestOutcomeEvent{
		At:       now,
		Provider: "p1",
		Status:   200,
		Delivery: "committed_complete",
		Protocol: "completed",
		Bytes:    123,
	}
	inputField := reflect.ValueOf(lastRequest).Elem().FieldByName("Capability")
	if !inputField.IsValid() {
		t.Fatalf("expected RequestOutcomeEvent to expose Capability")
	}
	inputField.SetString("openai_responses")

	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p1",
		LastRequest:     lastRequest,
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.LastRequest == nil {
		t.Fatalf("expected last_request to be populated")
	}

	outputField := reflect.ValueOf(*got.LastRequest).FieldByName("Capability")
	if !outputField.IsValid() {
		t.Fatalf("expected RequestOutcomeStatus to expose Capability")
	}
	if gotCapability := outputField.String(); gotCapability != "openai_responses" {
		t.Fatalf("last_request.capability: got %q want %q", gotCapability, "openai_responses")
	}
}

func TestBuildClientStatus_HidesCountTokensCapabilityFromUserFacingStatus(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1},
		},
	}
	now := time.Date(2026, 3, 18, 16, 32, 24, 0, time.UTC)
	lastRequest := &proxy.RequestOutcomeEvent{
		At:       now,
		Provider: "p1",
		Status:   200,
		Delivery: "committed_complete",
		Protocol: "completed",
		Bytes:    64,
	}
	inputField := reflect.ValueOf(lastRequest).Elem().FieldByName("Capability")
	if !inputField.IsValid() {
		t.Fatalf("expected RequestOutcomeEvent to expose Capability")
	}
	inputField.SetString("claude_count_tokens")

	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p1",
		LastRequest:     lastRequest,
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.LastRequest == nil {
		t.Fatalf("expected last_request to be populated")
	}

	outputField := reflect.ValueOf(*got.LastRequest).FieldByName("Capability")
	if !outputField.IsValid() {
		t.Fatalf("expected RequestOutcomeStatus to expose Capability")
	}
	if gotCapability := outputField.String(); gotCapability != "" {
		t.Fatalf("last_request.capability: got %q want empty", gotCapability)
	}
}

func TestBuildClientStatus_NormalizesDisplayFields(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1},
			{Name: "p2", Priority: 2},
		},
	}
	now := time.Date(2026, 3, 18, 16, 32, 24, 0, time.UTC)
	deactivatedUntil := time.Now().Add(30 * time.Second)
	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p2",
		LastSwitch: &proxy.ProviderSwitchEvent{
			At:     now,
			From:   "p1",
			To:     "p2",
			Reason: "rate_limit",
			Status: 429,
		},
		Providers: []proxy.ProviderRuntimeSnapshot{
			{
				Name:              "p1",
				DeactivatedReason: "rate_limit",
				DeactivatedUntil:  deactivatedUntil,
			},
			{
				Name:         "p2",
				CircuitState: "half_open",
			},
		},
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.LastSwitch == nil {
		t.Fatalf("expected last_switch to be populated")
	}
	if got.LastSwitch.Label != "p1 -> p2" {
		t.Fatalf("last_switch.label: got %q want %q", got.LastSwitch.Label, "p1 -> p2")
	}
	if got.LastSwitch.Detail == "" {
		t.Fatalf("expected last_switch.detail to be populated")
	}
	if len(got.Providers) != 2 {
		t.Fatalf("providers len: got %d want %d", len(got.Providers), 2)
	}
	if got.Providers[0].State != "cooling_down" {
		t.Fatalf("provider[0].state: got %q want %q", got.Providers[0].State, "cooling_down")
	}
	if got.Providers[0].Label != "p1 (cooling down)" {
		t.Fatalf("provider[0].label: got %q want %q", got.Providers[0].Label, "p1 (cooling down)")
	}
	if got.Providers[0].Detail == "" {
		t.Fatalf("expected provider[0].detail to be populated")
	}
	if got.Providers[1].State != "recovery_probe" {
		t.Fatalf("provider[1].state: got %q want %q", got.Providers[1].State, "recovery_probe")
	}
}

func TestBuildClientStatus_IncludesTerminalFailureOutcome(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1},
		},
	}
	now := time.Date(2026, 3, 18, 16, 32, 24, 0, time.UTC)
	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p1",
		LastRequest: &proxy.RequestOutcomeEvent{
			At:       now,
			Provider: "p1",
			Status:   http.StatusServiceUnavailable,
			Result:   "all_providers_failed",
			Detail:   "p1 returned HTTP 503 Service Unavailable",
		},
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.LastRequest == nil {
		t.Fatalf("expected last_request to be populated")
	}
	if got.LastRequest.Result != "all_providers_failed" {
		t.Fatalf("last_request.result: got %q want %q", got.LastRequest.Result, "all_providers_failed")
	}
	if got.LastRequest.Label != "All providers failed" {
		t.Fatalf("last_request.label: got %q want %q", got.LastRequest.Label, "All providers failed")
	}
	if got.LastRequest.Detail != "p1 returned HTTP 503 Service Unavailable" {
		t.Fatalf("last_request.detail: got %q", got.LastRequest.Detail)
	}
}

func TestBuildClientStatus_IncludesRequestRejectedOutcome(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "broken", Priority: 1},
		},
	}
	now := time.Date(2026, 3, 18, 16, 32, 24, 0, time.UTC)
	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "broken",
		LastRequest: &proxy.RequestOutcomeEvent{
			At:       now,
			Provider: "broken",
			Status:   http.StatusBadGateway,
			Result:   "request_rejected",
			Detail:   "broken request could not be prepared locally: invalid base_url",
		},
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.LastRequest == nil {
		t.Fatalf("expected last_request to be populated")
	}
	if got.LastRequest.Result != "request_rejected" {
		t.Fatalf("last_request.result: got %q want %q", got.LastRequest.Result, "request_rejected")
	}
	if got.LastRequest.Label != "Request rejected by proxy" {
		t.Fatalf("last_request.label: got %q want %q", got.LastRequest.Label, "Request rejected by proxy")
	}
}

func TestBuildClientStatus_ReportsNoAvailableKeys(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeManual,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1, APIKeys: []string{"k1", "k2"}},
		},
	}
	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p1",
		Providers: []proxy.ProviderRuntimeSnapshot{
			{
				Name:              "p1",
				KeyCount:          2,
				AvailableKeyCount: 0,
			},
		},
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if len(got.Providers) != 1 {
		t.Fatalf("providers len: got %d want %d", len(got.Providers), 1)
	}
	if got.Providers[0].State != "unavailable" {
		t.Fatalf("provider.state: got %q want %q", got.Providers[0].State, "unavailable")
	}
	if got.Providers[0].SkipReason != "keys_exhausted" {
		t.Fatalf("provider.skip_reason: got %q want %q", got.Providers[0].SkipReason, "keys_exhausted")
	}
	if got.Providers[0].AvailableKeyCount != 0 {
		t.Fatalf("provider.available_key_count: got %d want %d", got.Providers[0].AvailableKeyCount, 0)
	}
}

func TestBuildClientStatus_ExposesScopeSpecificCurrentProviders(t *testing.T) {
	cc := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", Priority: 1},
			{Name: "p2", Priority: 2},
		},
	}
	rt := proxy.ClientRuntimeSnapshot{
		CurrentProvider: "p1",
		CurrentProviders: map[string]string{
			"default":                        "p1",
			"openai_responses":               "p2",
			"gemini_stream_generate_content": "p2",
		},
	}

	got := buildClientStatus(cc, cc.Providers, rt)
	if got.CurrentProvider != "p1" {
		t.Fatalf("current_provider: got %q want %q", got.CurrentProvider, "p1")
	}
	if len(got.CurrentProviders) != 3 {
		t.Fatalf("current_providers len: got %d want %d", len(got.CurrentProviders), 3)
	}
	if got.CurrentProviders["default"] != "p1" {
		t.Fatalf("current_providers[default]: got %q want %q", got.CurrentProviders["default"], "p1")
	}
	if got.CurrentProviders["openai_responses"] != "p2" {
		t.Fatalf("current_providers[openai_responses]: got %q want %q", got.CurrentProviders["openai_responses"], "p2")
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
