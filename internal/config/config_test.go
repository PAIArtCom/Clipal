package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClientConfigFile(t *testing.T, dir string, name string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func TestLoad_MigratesLegacyClientConfigFilenames(t *testing.T) {
	t.Run("migrates legacy files when new files are missing", func(t *testing.T) {
		dir := t.TempDir()
		writeClientConfigFile(t, dir, "claude-code.yaml", `
mode: manual
pinned_provider: claude-primary
providers:
  - name: claude-primary
    base_url: https://claude.example
    api_key: claude-key
    priority: 1
`)
		writeClientConfigFile(t, dir, "codex.yaml", `
mode: manual
pinned_provider: openai-primary
providers:
  - name: openai-primary
    base_url: https://openai.example
    api_key: openai-key
    priority: 1
`)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Claude.PinnedProvider != "claude-primary" {
			t.Fatalf("Claude.PinnedProvider = %q", cfg.Claude.PinnedProvider)
		}
		if cfg.OpenAI.PinnedProvider != "openai-primary" {
			t.Fatalf("OpenAI.PinnedProvider = %q", cfg.OpenAI.PinnedProvider)
		}
		if _, err := os.Stat(filepath.Join(dir, "claude-code.yaml")); !os.IsNotExist(err) {
			t.Fatalf("expected claude-code.yaml to be removed after migration, err=%v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "codex.yaml")); !os.IsNotExist(err) {
			t.Fatalf("expected codex.yaml to be removed after migration, err=%v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "claude.yaml")); err != nil {
			t.Fatalf("expected claude.yaml to exist: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "openai.yaml")); err != nil {
			t.Fatalf("expected openai.yaml to exist: %v", err)
		}
	})

	t.Run("uses new filenames directly when present", func(t *testing.T) {
		dir := t.TempDir()
		writeClientConfigFile(t, dir, "claude.yaml", `
mode: manual
pinned_provider: claude-new
providers:
  - name: claude-new
    base_url: https://claude.example
    api_key: claude-key
    priority: 1
`)
		writeClientConfigFile(t, dir, "openai.yaml", `
mode: manual
pinned_provider: openai-new
providers:
  - name: openai-new
    base_url: https://openai.example
    api_key: openai-key
    priority: 1
`)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Claude.PinnedProvider != "claude-new" {
			t.Fatalf("Claude.PinnedProvider = %q", cfg.Claude.PinnedProvider)
		}
		if cfg.OpenAI.PinnedProvider != "openai-new" {
			t.Fatalf("OpenAI.PinnedProvider = %q", cfg.OpenAI.PinnedProvider)
		}
		if _, err := os.Stat(filepath.Join(dir, "claude-code.yaml")); !os.IsNotExist(err) {
			t.Fatalf("did not expect claude-code.yaml to exist, err=%v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "codex.yaml")); !os.IsNotExist(err) {
			t.Fatalf("did not expect codex.yaml to exist, err=%v", err)
		}
	})

	t.Run("treats missing new and legacy files as empty config", func(t *testing.T) {
		dir := t.TempDir()
		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.Claude.Providers) != 0 || len(cfg.OpenAI.Providers) != 0 || len(cfg.Gemini.Providers) != 0 {
			t.Fatalf("expected empty client configs, got Claude=%d OpenAI=%d Gemini=%d",
				len(cfg.Claude.Providers), len(cfg.OpenAI.Providers), len(cfg.Gemini.Providers))
		}
	})

	t.Run("fails when new and legacy files both exist with different content", func(t *testing.T) {
		dir := t.TempDir()
		writeClientConfigFile(t, dir, "claude-code.yaml", `
providers:
  - name: legacy
    base_url: https://legacy.example
    api_key: legacy-key
    priority: 1
`)
		writeClientConfigFile(t, dir, "claude.yaml", `
providers:
  - name: modern
    base_url: https://modern.example
    api_key: modern-key
    priority: 1
`)

		_, err := Load(dir)
		if err == nil || !strings.Contains(err.Error(), "both claude.yaml and claude-code.yaml exist with different content") {
			t.Fatalf("Load err = %v", err)
		}
	})

	t.Run("removes legacy file when both new and legacy files have identical content", func(t *testing.T) {
		dir := t.TempDir()
		body := `
providers:
  - name: shared
    base_url: https://same.example
    api_key: same-key
    priority: 1
`
		writeClientConfigFile(t, dir, "openai.yaml", body)
		writeClientConfigFile(t, dir, "codex.yaml", body)

		cfg, err := Load(dir)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.OpenAI.Providers) != 1 || cfg.OpenAI.Providers[0].Name != "shared" {
			t.Fatalf("OpenAI providers = %#v", cfg.OpenAI.Providers)
		}
		if _, err := os.Stat(filepath.Join(dir, "openai.yaml")); err != nil {
			t.Fatalf("expected openai.yaml to remain: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "codex.yaml")); !os.IsNotExist(err) {
			t.Fatalf("expected codex.yaml to be removed, err=%v", err)
		}
	})
}

func TestValidate_CircuitBreakerDisabled_AllowsInvalidCBFields(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{Mode: ClientModeAuto},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	// Disable circuit breaker explicitly.
	cfg.Global.CircuitBreaker.FailureThreshold = 0
	// Intentionally make other CB fields invalid; validation should skip them when disabled.
	cfg.Global.CircuitBreaker.OpenTimeout = "not-a-duration"
	cfg.Global.CircuitBreaker.SuccessThreshold = 0
	cfg.Global.CircuitBreaker.HalfOpenMaxInFlight = 0

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected config to validate with circuit breaker disabled, got: %v", err)
	}
}

func TestExampleConfig_DoesNotExposeDeprecatedIgnoreCountTokensFailover(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "examples", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	if strings.Contains(string(data), "ignore_count_tokens_failover") {
		t.Fatalf("example config should not expose deprecated ignore_count_tokens_failover")
	}
}

func TestValidate_CircuitBreakerEnabled_StillValidatesCBFields(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{Mode: ClientModeAuto},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	cfg.Global.CircuitBreaker.FailureThreshold = 1
	cfg.Global.CircuitBreaker.OpenTimeout = "nope"

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid circuit_breaker.open_timeout when enabled")
	}
}

func TestValidate_CircuitBreakerFailureThresholdNegative_IsRejected(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{Mode: ClientModeAuto},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	cfg.Global.CircuitBreaker.FailureThreshold = -1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error for negative circuit_breaker.failure_threshold")
	}
}

func TestValidate_ManualMode_RequiresEnabledPinnedProvider(t *testing.T) {
	t.Parallel()

	provider := Provider{
		Name:     "p1",
		BaseURL:  "http://example.com",
		APIKey:   "key",
		Priority: 1,
		Enabled:  ptr(false),
	}

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{
			Mode:           ClientModeManual,
			PinnedProvider: "p1",
			Providers:      []Provider{provider},
		},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	// 1. Fails if provider is disabled
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for disabled pinned_provider")
	}

	// 2. Fails if pinned_provider is missing from list
	cfg.Claude.PinnedProvider = "ghost"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for non-existent pinned_provider")
	}

	// 3. Fails if pinned_provider is empty
	cfg.Claude.PinnedProvider = ""
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error for empty pinned_provider")
	}

	// 4. Passes if enabled and exists
	cfg.Claude.PinnedProvider = "p1"
	cfg.Claude.Providers[0].Enabled = ptr(true)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
}

func TestValidate_ProviderAPIKeys_MultiKeySupported(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:     "p1",
					BaseURL:  "https://example.com",
					APIKeys:  []string{" key1 ", "key2", "key1"},
					Priority: 1,
				},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	applyClientDefaults(&cfg.OpenAI)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
	if got := cfg.OpenAI.Providers[0].KeyCount(); got != 2 {
		t.Fatalf("key count: got %d want %d", got, 2)
	}
	if cfg.OpenAI.Providers[0].APIKey != "" {
		t.Fatalf("expected canonical multi-key provider to use api_keys, got api_key=%q", cfg.OpenAI.Providers[0].APIKey)
	}
}

func TestGlobalConfigRuntimeDurations(t *testing.T) {
	t.Parallel()

	cfg := DefaultGlobalConfig()
	got, err := cfg.RuntimeDurations()
	if err != nil {
		t.Fatalf("RuntimeDurations: %v", err)
	}
	if got.ReactivateAfter != time.Hour {
		t.Fatalf("ReactivateAfter = %s, want %s", got.ReactivateAfter, time.Hour)
	}
	if got.UpstreamIdleTimeout != 3*time.Minute {
		t.Fatalf("UpstreamIdleTimeout = %s, want %s", got.UpstreamIdleTimeout, 3*time.Minute)
	}
	if got.ResponseHeaderTimeout != 2*time.Minute {
		t.Fatalf("ResponseHeaderTimeout = %s, want %s", got.ResponseHeaderTimeout, 2*time.Minute)
	}
}

func TestCircuitBreakerOpenTimeoutDuration(t *testing.T) {
	t.Parallel()

	d, err := CircuitBreakerConfig{OpenTimeout: "90s"}.OpenTimeoutDuration()
	if err != nil {
		t.Fatalf("OpenTimeoutDuration: %v", err)
	}
	if d != 90*time.Second {
		t.Fatalf("OpenTimeoutDuration = %s, want %s", d, 90*time.Second)
	}
}

func TestValidate_ProviderAPIKeys_RejectsMixedForms(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:     "p1",
					BaseURL:  "https://example.com",
					APIKey:   "key1",
					APIKeys:  []string{"key2"},
					Priority: 1,
				},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected validation error when api_key and api_keys are both set")
	}
}

func TestValidate_ProviderOAuthCodexSupportedForOpenAIWithoutBaseURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "codex-oauth",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderCodex,
					OAuthRef:      "codex_acct_123",
					Priority:      2,
				},
				{
					Name:     "api-backup",
					BaseURL:  "https://api.openai.com",
					APIKey:   "sk-test",
					Priority: 1,
				},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	applyClientDefaults(&cfg.OpenAI)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
	if got := cfg.OpenAI.Providers[0].NormalizedAuthType(); got != ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want %q", got, ProviderAuthTypeOAuth)
	}
	if got := cfg.OpenAI.Providers[0].NormalizedOAuthProvider(); got != OAuthProviderCodex {
		t.Fatalf("oauth_provider = %q, want %q", got, OAuthProviderCodex)
	}
}

func TestValidate_ProviderOAuthClaudeSupportedForClaudeWithoutBaseURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "claude-oauth",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderClaude,
					OAuthRef:      "claude_sean_example_com",
					Priority:      1,
				},
			},
		},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	applyClientDefaults(&cfg.Claude)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
	if got := cfg.Claude.Providers[0].NormalizedAuthType(); got != ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want %q", got, ProviderAuthTypeOAuth)
	}
	if got := cfg.Claude.Providers[0].NormalizedOAuthProvider(); got != OAuthProviderClaude {
		t.Fatalf("oauth_provider = %q, want %q", got, OAuthProviderClaude)
	}
}

func TestValidate_ProviderOAuthGeminiSupportedForGeminiWithoutBaseURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Gemini: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "gemini-oauth",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderGemini,
					OAuthRef:      "gemini_sean_example_com_project_123",
					Priority:      1,
				},
			},
		},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Claude: ClientConfig{Mode: ClientModeAuto},
	}

	applyClientDefaults(&cfg.Gemini)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
	if got := cfg.Gemini.Providers[0].NormalizedAuthType(); got != ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want %q", got, ProviderAuthTypeOAuth)
	}
	if got := cfg.Gemini.Providers[0].NormalizedOAuthProvider(); got != OAuthProviderGemini {
		t.Fatalf("oauth_provider = %q, want %q", got, OAuthProviderGemini)
	}
}

func TestValidate_ProviderOAuthAntigravitySupportedForGeminiWithoutBaseURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Gemini: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "antigravity-oauth",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderAntigravity,
					OAuthRef:      "antigravity_sean_example_com_project_123",
					Priority:      1,
				},
			},
		},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Claude: ClientConfig{Mode: ClientModeAuto},
	}

	applyClientDefaults(&cfg.Gemini)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected validation success, got: %v", err)
	}
	if got := cfg.Gemini.Providers[0].NormalizedOAuthProvider(); got != OAuthProviderAntigravity {
		t.Fatalf("oauth_provider = %q, want %q", got, OAuthProviderAntigravity)
	}
}

func TestValidate_ProviderOAuthRejectsMixedCredentialSources(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "mixed",
					BaseURL:       "https://api.openai.com",
					APIKey:        "sk-test",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderCodex,
					OAuthRef:      "codex_acct_123",
					Priority:      1,
				},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be set when auth_type=oauth") {
		t.Fatalf("err = %v, want mixed credential source validation error", err)
	}
}

func TestValidate_ProviderOAuthRejectsMissingOAuthRef(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "missing-ref",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderCodex,
					Priority:      1,
				},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "oauth_ref is required") {
		t.Fatalf("err = %v, want oauth_ref validation error", err)
	}
}

func TestValidate_ProviderOAuthRejectsBaseURL(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "codex-oauth",
					BaseURL:       "https://chatgpt.com/backend-api/codex",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderCodex,
					OAuthRef:      "codex_acct_123",
					Priority:      1,
				},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "base_url is not allowed") {
		t.Fatalf("err = %v, want base_url rejection for oauth provider", err)
	}
}

func TestValidate_ProviderOAuthRejectsUnsupportedClient(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:          "claude-codex-oauth",
					AuthType:      ProviderAuthTypeOAuth,
					OAuthProvider: OAuthProviderCodex,
					OAuthRef:      "codex_acct_123",
					Priority:      1,
				},
			},
		},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `only supported for openai client`) {
		t.Fatalf("err = %v, want unsupported oauth client validation error", err)
	}
}

func TestValidate_ProviderThinkingBudgetRejectsNegative(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{
					Name:     "p1",
					BaseURL:  "https://example.com",
					APIKey:   "key1",
					Priority: 1,
					Overrides: &ProviderOverrides{
						Claude: &ClaudeOverrides{
							ThinkingBudgetTokens: ptr(-1),
						},
					},
				},
			},
		},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "thinking_budget_tokens") {
		t.Fatalf("err = %v, want thinking_budget_tokens validation error", err)
	}
}

func TestLoad_ProviderOverridesSupportNestedAndLegacyYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeClientConfigFile(t, dir, "openai.yaml", `
providers:
  - name: openai-primary
    base_url: https://openai.example
    api_key: openai-key
    overrides:
      model: " gpt-5.4 "
      openai:
        reasoning_effort: " high "
    priority: 1
`)
	writeClientConfigFile(t, dir, "claude.yaml", `
providers:
  - name: claude-primary
    base_url: https://claude.example
    api_key: claude-key
    model: claude-sonnet-4-5
    thinking_budget_tokens: 2048
    priority: 1
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenAI.Providers[0].Overrides == nil {
		t.Fatalf("expected openai overrides to be present")
	}
	if got := cfg.OpenAI.Providers[0].ModelOverride(); got != "gpt-5.4" {
		t.Fatalf("OpenAI model = %q", got)
	}
	if got := cfg.OpenAI.Providers[0].OpenAIReasoningEffort(); got != "high" {
		t.Fatalf("OpenAI reasoning_effort = %q", got)
	}
	if cfg.Claude.Providers[0].Overrides == nil {
		t.Fatalf("expected claude overrides to be present")
	}
	if got := cfg.Claude.Providers[0].ModelOverride(); got != "claude-sonnet-4-5" {
		t.Fatalf("Claude model = %q", got)
	}
	if got := cfg.Claude.Providers[0].ClaudeThinkingBudgetTokens(); got != 2048 {
		t.Fatalf("Claude thinking_budget_tokens = %d", got)
	}
}

func TestLoad_InvalidNegativeThinkingBudgetSurfacesDuringValidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeClientConfigFile(t, dir, "claude.yaml", `
providers:
  - name: claude-primary
    base_url: https://claude.example
    api_key: claude-key
    thinking_budget_tokens: -1
    priority: 1
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Claude.Providers[0].ClaudeThinkingBudgetTokens(); got != -1 {
		t.Fatalf("ThinkingBudgetTokens = %d, want -1", got)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "thinking_budget_tokens") {
		t.Fatalf("Validate err = %v, want thinking_budget_tokens validation error", err)
	}
}

func TestValidate_RejectsDuplicateProviderNamesPerClient(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		OpenAI: ClientConfig{
			Mode: ClientModeAuto,
			Providers: []Provider{
				{Name: "p1", BaseURL: "https://one.example", APIKey: "key1", Priority: 1},
				{Name: "p1", BaseURL: "https://two.example", APIKey: "key2", Priority: 2},
			},
		},
		Claude: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), `duplicate provider name "p1"`) {
		t.Fatalf("err = %v, want duplicate provider name error", err)
	}
}

func TestGlobalConfigRuntimeDurations_ErrorsAndBoundaryValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*GlobalConfig)
		wantErr string
		wantDur RuntimeDurations
	}{
		{
			name: "MalformedReactivateAfter",
			mutate: func(gc *GlobalConfig) {
				gc.ReactivateAfter = "nope"
			},
			wantErr: "invalid reactivate_after",
		},
		{
			name: "MalformedUpstreamIdleTimeout",
			mutate: func(gc *GlobalConfig) {
				gc.UpstreamIdleTimeout = "bad"
			},
			wantErr: "invalid upstream_idle_timeout",
		},
		{
			name: "MalformedResponseHeaderTimeout",
			mutate: func(gc *GlobalConfig) {
				gc.ResponseHeaderTimeout = "bad"
			},
			wantErr: "invalid response_header_timeout",
		},
		{
			name: "NegativeDurations",
			mutate: func(gc *GlobalConfig) {
				gc.ReactivateAfter = "-1h"
				gc.UpstreamIdleTimeout = "-3m"
				gc.ResponseHeaderTimeout = "-2m"
			},
			wantDur: RuntimeDurations{
				ReactivateAfter:       -1 * time.Hour,
				UpstreamIdleTimeout:   -3 * time.Minute,
				ResponseHeaderTimeout: -2 * time.Minute,
			},
		},
		{
			name: "ZeroDurations",
			mutate: func(gc *GlobalConfig) {
				gc.ReactivateAfter = "0"
				gc.UpstreamIdleTimeout = "0"
				gc.ResponseHeaderTimeout = "0"
			},
			wantDur: RuntimeDurations{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gc := DefaultGlobalConfig()
			tt.mutate(&gc)
			got, err := gc.RuntimeDurations()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RuntimeDurations: %v", err)
			}
			if got != tt.wantDur {
				t.Fatalf("durations = %#v, want %#v", got, tt.wantDur)
			}
		})
	}
}

func TestDefaultGlobalConfig_IncludesRoutingDefaults(t *testing.T) {
	t.Parallel()

	cfg := DefaultGlobalConfig()

	if !cfg.Routing.StickySessions.Enabled {
		t.Fatalf("sticky_sessions.enabled: got false want true")
	}
	if got := cfg.Routing.StickySessions.ExplicitTTL; got != "30m" {
		t.Fatalf("sticky_sessions.explicit_ttl: got %q want %q", got, "30m")
	}
	if got := cfg.Routing.StickySessions.CacheHintTTL; got != "10m" {
		t.Fatalf("sticky_sessions.cache_hint_ttl: got %q want %q", got, "10m")
	}
	if got := cfg.Routing.StickySessions.DynamicFeatureTTL; got != "10m" {
		t.Fatalf("sticky_sessions.dynamic_feature_ttl: got %q want %q", got, "10m")
	}
	if got := cfg.Routing.StickySessions.DynamicFeatureCapacity; got != 1024 {
		t.Fatalf("sticky_sessions.dynamic_feature_capacity: got %d want %d", got, 1024)
	}
	if got := cfg.Routing.StickySessions.ResponseLookupTTL; got != "15m" {
		t.Fatalf("sticky_sessions.response_lookup_ttl: got %q want %q", got, "15m")
	}
	if !cfg.Routing.BusyBackpressure.Enabled {
		t.Fatalf("busy_backpressure.enabled: got false want true")
	}
	if got := cfg.Routing.BusyBackpressure.RetryDelays; len(got) != 2 || got[0] != "5s" || got[1] != "10s" {
		t.Fatalf("busy_backpressure.retry_delays: got %#v want [5s 10s]", got)
	}
	if got := cfg.Routing.BusyBackpressure.ProbeMaxInFlight; got != 1 {
		t.Fatalf("busy_backpressure.probe_max_inflight: got %d want %d", got, 1)
	}
	if got := cfg.Routing.BusyBackpressure.ShortRetryAfterMax; got != "3s" {
		t.Fatalf("busy_backpressure.short_retry_after_max: got %q want %q", got, "3s")
	}
	if got := cfg.Routing.BusyBackpressure.MaxInlineWait; got != "8s" {
		t.Fatalf("busy_backpressure.max_inline_wait: got %q want %q", got, "8s")
	}
}

func TestValidate_RoutingConfigRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "empty retry delays",
			mutate: func(cfg *Config) {
				cfg.Global.Routing.BusyBackpressure.RetryDelays = nil
			},
			wantErr: "routing.busy_backpressure.retry_delays",
		},
		{
			name: "bad explicit ttl",
			mutate: func(cfg *Config) {
				cfg.Global.Routing.StickySessions.ExplicitTTL = "bad"
			},
			wantErr: "routing.sticky_sessions.explicit_ttl",
		},
		{
			name: "non positive dynamic feature capacity",
			mutate: func(cfg *Config) {
				cfg.Global.Routing.StickySessions.DynamicFeatureCapacity = 0
			},
			wantErr: "routing.sticky_sessions.dynamic_feature_capacity",
		},
		{
			name: "negative probe inflight",
			mutate: func(cfg *Config) {
				cfg.Global.Routing.BusyBackpressure.ProbeMaxInFlight = -1
			},
			wantErr: "routing.busy_backpressure.probe_max_inflight",
		},
		{
			name: "bad max inline wait",
			mutate: func(cfg *Config) {
				cfg.Global.Routing.BusyBackpressure.MaxInlineWait = "later"
			},
			wantErr: "routing.busy_backpressure.max_inline_wait",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &Config{
				Global: DefaultGlobalConfig(),
				Claude: ClientConfig{Mode: ClientModeAuto},
				OpenAI: ClientConfig{Mode: ClientModeAuto},
				Gemini: ClientConfig{Mode: ClientModeAuto},
			}
			tt.mutate(cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestCircuitBreakerOpenTimeoutDuration_InvalidString(t *testing.T) {
	t.Parallel()

	if _, err := (CircuitBreakerConfig{OpenTimeout: "bad"}).OpenTimeoutDuration(); err == nil {
		t.Fatalf("expected invalid duration error")
	}
}

func TestLoad_RejectsUnknownYAMLFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("listen_addr: 127.0.0.1\nport: 3333\nunknown_field: true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Fatalf("Load error = %v, want unknown_field failure", err)
	}
}

func TestGetConfigDir_RespectsEnvironmentOverride(t *testing.T) {
	const want = "/tmp/clipal-config-override"
	t.Setenv("CLIPAL_CONFIG_DIR", want)
	if got := GetConfigDir(); got != want {
		t.Fatalf("GetConfigDir = %q, want %q", got, want)
	}
}

func TestLoad_ProviderProxyModeDefaultsToDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeClientConfigFile(t, dir, "openai.yaml", `
providers:
  - name: p1
    base_url: https://example.com
    api_key: key
    priority: 1
`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.OpenAI.Providers[0].NormalizedProxyMode(); got != ProviderProxyModeDefault {
		t.Fatalf("proxy mode = %q, want %q", got, ProviderProxyModeDefault)
	}
	if got := cfg.OpenAI.Providers[0].NormalizedProxyURL(); got != "" {
		t.Fatalf("proxy url = %q, want empty", got)
	}
}

func TestLoad_GlobalUpstreamProxyModeDefaultsToEnvironment(t *testing.T) {
	t.Parallel()

	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Global.NormalizedUpstreamProxyMode(); got != GlobalUpstreamProxyModeEnvironment {
		t.Fatalf("upstream proxy mode = %q, want %q", got, GlobalUpstreamProxyModeEnvironment)
	}
}

func TestValidate_ProviderProxySettings(t *testing.T) {
	t.Parallel()

	base := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{Mode: ClientModeAuto},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	makeProvider := func(mode ProviderProxyMode, proxyURL string) Provider {
		return Provider{
			Name:      "p1",
			BaseURL:   "https://example.com",
			APIKey:    "key",
			ProxyMode: mode,
			ProxyURL:  proxyURL,
			Priority:  1,
		}
	}

	t.Run("accepts direct", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeDirect, "")}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("accepts custom http proxy", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeCustom, "http://127.0.0.1:7890")}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("accepts custom socks5 proxy", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeCustom, "socks5://127.0.0.1:1080")}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("accepts custom socks5h proxy", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeCustom, "socks5h://127.0.0.1:1080")}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("rejects custom proxy without url", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeCustom, "")}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "proxy_url is required") {
			t.Fatalf("Validate err = %v", err)
		}
	})

	t.Run("rejects unsupported proxy scheme", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeCustom, "ftp://127.0.0.1:21")}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "proxy_url scheme must be http, https, socks5, or socks5h") {
			t.Fatalf("Validate err = %v", err)
		}
	})

	t.Run("rejects proxy url without custom mode", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyModeDirect, "http://127.0.0.1:7890")}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "proxy_url requires proxy_mode custom") {
			t.Fatalf("Validate err = %v", err)
		}
	})

	t.Run("rejects removed inherit mode", func(t *testing.T) {
		cfg := *base
		cfg.OpenAI.Providers = []Provider{makeProvider(ProviderProxyMode("inherit"), "")}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `invalid proxy_mode "inherit"`) {
			t.Fatalf("Validate err = %v", err)
		}
	})
}

func TestValidate_GlobalUpstreamProxySettings(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Global: DefaultGlobalConfig(),
		Claude: ClientConfig{Mode: ClientModeAuto},
		OpenAI: ClientConfig{Mode: ClientModeAuto},
		Gemini: ClientConfig{Mode: ClientModeAuto},
	}

	cfg.Global.UpstreamProxyMode = GlobalUpstreamProxyModeCustom
	cfg.Global.UpstreamProxyURL = "http://127.0.0.1:7890"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	cfg.Global.UpstreamProxyURL = "socks5://127.0.0.1:1080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	cfg.Global.UpstreamProxyURL = "socks5h://127.0.0.1:1080"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	cfg.Global.UpstreamProxyURL = "ftp://127.0.0.1:21"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "proxy_url scheme must be http, https, socks5, or socks5h") {
		t.Fatalf("Validate err = %v", err)
	}

	cfg.Global.UpstreamProxyMode = GlobalUpstreamProxyMode("inherit")
	cfg.Global.UpstreamProxyURL = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), `invalid upstream_proxy_mode "inherit"`) {
		t.Fatalf("Validate err = %v", err)
	}
}

func TestApplyProviderProxySettings(t *testing.T) {
	t.Parallel()

	t.Run("create defaults to default mode", func(t *testing.T) {
		var provider Provider
		if err := ApplyProviderProxySettings(&provider, ProviderProxySettingsPatch{}, false); err != nil {
			t.Fatalf("ApplyProviderProxySettings: %v", err)
		}
		if provider.ProxyMode != ProviderProxyModeDefault || provider.ProxyURL != "" {
			t.Fatalf("provider = %#v", provider)
		}
	})

	t.Run("update retains existing custom url", func(t *testing.T) {
		provider := Provider{ProxyMode: ProviderProxyModeCustom, ProxyURL: "http://127.0.0.1:7890"}
		mode := "custom"
		if err := ApplyProviderProxySettings(&provider, ProviderProxySettingsPatch{Mode: &mode}, true); err != nil {
			t.Fatalf("ApplyProviderProxySettings: %v", err)
		}
		if provider.ProxyMode != ProviderProxyModeCustom || provider.ProxyURL != "http://127.0.0.1:7890" {
			t.Fatalf("provider = %#v", provider)
		}
	})

	t.Run("update clears url on direct", func(t *testing.T) {
		provider := Provider{ProxyMode: ProviderProxyModeCustom, ProxyURL: "http://127.0.0.1:7890"}
		mode := "direct"
		if err := ApplyProviderProxySettings(&provider, ProviderProxySettingsPatch{Mode: &mode}, true); err != nil {
			t.Fatalf("ApplyProviderProxySettings: %v", err)
		}
		if provider.ProxyMode != ProviderProxyModeDirect || provider.ProxyURL != "" {
			t.Fatalf("provider = %#v", provider)
		}
	})

	t.Run("rejects default with url", func(t *testing.T) {
		mode := "default"
		rawURL := "http://127.0.0.1:7890"
		if err := ApplyProviderProxySettings(&Provider{}, ProviderProxySettingsPatch{Mode: &mode, URL: &rawURL}, false); err == nil || !strings.Contains(err.Error(), "proxy_url requires proxy_mode custom") {
			t.Fatalf("ApplyProviderProxySettings err = %v", err)
		}
	})

	t.Run("rejects removed inherit mode", func(t *testing.T) {
		mode := "inherit"
		if err := ApplyProviderProxySettings(&Provider{}, ProviderProxySettingsPatch{Mode: &mode}, false); err == nil || !strings.Contains(err.Error(), "proxy_mode must be one of default, direct, custom") {
			t.Fatalf("ApplyProviderProxySettings err = %v", err)
		}
	})
}

func TestApplyUpstreamProxySettings(t *testing.T) {
	t.Parallel()

	t.Run("omitted patch preserves existing values", func(t *testing.T) {
		global := GlobalConfig{
			UpstreamProxyMode: GlobalUpstreamProxyModeCustom,
			UpstreamProxyURL:  "http://127.0.0.1:7890",
		}
		if err := ApplyUpstreamProxySettings(&global, UpstreamProxySettingsPatch{}); err != nil {
			t.Fatalf("ApplyUpstreamProxySettings: %v", err)
		}
		if global.UpstreamProxyMode != GlobalUpstreamProxyModeCustom || global.UpstreamProxyURL != "http://127.0.0.1:7890" {
			t.Fatalf("global = %#v", global)
		}
	})

	t.Run("switches to environment and clears url", func(t *testing.T) {
		global := GlobalConfig{
			UpstreamProxyMode: GlobalUpstreamProxyModeCustom,
			UpstreamProxyURL:  "http://127.0.0.1:7890",
		}
		mode := "environment"
		if err := ApplyUpstreamProxySettings(&global, UpstreamProxySettingsPatch{Mode: &mode}); err != nil {
			t.Fatalf("ApplyUpstreamProxySettings: %v", err)
		}
		if global.UpstreamProxyMode != GlobalUpstreamProxyModeEnvironment || global.UpstreamProxyURL != "" {
			t.Fatalf("global = %#v", global)
		}
	})

	t.Run("rejects url without mode", func(t *testing.T) {
		rawURL := "http://127.0.0.1:7890"
		if err := ApplyUpstreamProxySettings(&GlobalConfig{}, UpstreamProxySettingsPatch{URL: &rawURL}); err == nil || !strings.Contains(err.Error(), "upstream_proxy_url requires upstream_proxy_mode") {
			t.Fatalf("ApplyUpstreamProxySettings err = %v", err)
		}
	})

	t.Run("rejects removed inherit mode", func(t *testing.T) {
		mode := "inherit"
		if err := ApplyUpstreamProxySettings(&GlobalConfig{}, UpstreamProxySettingsPatch{Mode: &mode}); err == nil || !strings.Contains(err.Error(), "upstream_proxy_mode must be one of environment, direct, custom") {
			t.Fatalf("ApplyUpstreamProxySettings err = %v", err)
		}
	})
}
