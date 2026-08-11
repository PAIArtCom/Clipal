package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
	"gopkg.in/yaml.v3"
)

func boolPtr(v bool) *bool { return &v }

func stringPtr(v string) *string { return &v }

func intPtr(v int) *int { return &v }

func TestFormatClientConfigYAML_QuotesSpacingAndTiers(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKey: "k1", Priority: 1, Enabled: boolPtr(true)},
			{Name: "p2", BaseURL: "https://b.example", APIKey: "k2", Priority: 2, Enabled: boolPtr(false)},
			{Name: "p3", BaseURL: "https://c.example", APIKey: "k3", Priority: 3, Enabled: boolPtr(true)},
			{Name: "p4", BaseURL: "https://d.example", APIKey: "k4", Priority: 3, Enabled: boolPtr(true)},
		},
	}

	got := string(formatClientConfigYAML("codex", cc))

	// Quoted strings for readability (match examples/*.yaml style).
	for _, want := range []string{
		`name: "p1"`,
		`base_url: "https://a.example"`,
		`api_key: "k1"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}

	// Tier comments.
	for _, want := range []string{
		`# Priority level 1:`,
		`# Priority level 2:`,
		`# Priority level 3:`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}

	// Blank lines between providers (including within same tier).
	if !strings.Contains(got, "enabled: true\n\n  - name: \"p4\"") {
		t.Fatalf("expected blank line between providers\n%s", got)
	}
}

func TestFormatGlobalConfigYAML_HasHeaderAndQuotesEmptyStrings(t *testing.T) {
	gc := config.DefaultGlobalConfig()
	gc.LogDir = ""

	got := string(formatGlobalConfigYAML(gc))
	if !strings.HasPrefix(got, "# Global configuration for clipal\n") {
		t.Fatalf("expected header comment, got:\n%s", got)
	}
	if !strings.Contains(got, "listen_addr: \"127.0.0.1\"") {
		t.Fatalf("expected listen_addr to be quoted, got:\n%s", got)
	}
	if !strings.Contains(got, "log_dir: \"\"") {
		t.Fatalf("expected log_dir empty string to be quoted, got:\n%s", got)
	}
	if !strings.Contains(got, "\nnotifications:\n") {
		t.Fatalf("expected notifications block, got:\n%s", got)
	}
	if !strings.Contains(got, "\nrouting:\n") {
		t.Fatalf("expected routing block, got:\n%s", got)
	}
	if !strings.Contains(got, "sticky_sessions:\n") {
		t.Fatalf("expected sticky_sessions block, got:\n%s", got)
	}
}

func TestFormatGlobalConfigYAML_EscapesNewlines(t *testing.T) {
	gc := config.DefaultGlobalConfig()
	gc.ListenAddr = "127.0.0.1\nport: 1"

	got := string(formatGlobalConfigYAML(gc))
	if strings.Contains(got, "listen_addr: 127.0.0.1\nport: 1") {
		t.Fatalf("expected listen_addr to be quoted/escaped (no YAML injection), got:\n%s", got)
	}
	if !strings.Contains(got, "listen_addr: \"127.0.0.1\\nport: 1\"") {
		t.Fatalf("expected escaped newline in listen_addr, got:\n%s", got)
	}
}

func TestFormatClientConfigYAML_SparsePriorities_CanSkipMiddleTier(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKey: "k1", Priority: 1, Enabled: boolPtr(true)},
			{Name: "p2", BaseURL: "https://b.example", APIKey: "k2", Priority: 2, Enabled: boolPtr(true)},
			{Name: "p3", BaseURL: "https://c.example", APIKey: "k3", Priority: 1000, Enabled: boolPtr(true)},
		},
	}
	got := string(formatClientConfigYAML("codex", cc))

	if !strings.Contains(got, "# Priority level 1:") {
		t.Fatalf("expected tier 1 comment\n%s", got)
	}
	if !strings.Contains(got, "# Priority level 3:") {
		t.Fatalf("expected tier 3 comment\n%s", got)
	}
	if strings.Contains(got, "# Priority level 2:") {
		t.Fatalf("did not expect tier 2 comment for sparse priorities\n%s", got)
	}
}

func TestFormatClientConfigYAML_UsesAPIKeysForMultiKeyProvider(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKeys: []string{"k1", "k2"}, Priority: 1, Enabled: boolPtr(true)},
		},
	}

	got := string(formatClientConfigYAML("codex", cc))
	if !strings.Contains(got, "api_keys:\n") {
		t.Fatalf("expected api_keys block, got:\n%s", got)
	}
	if strings.Contains(got, `api_key: "k1"`) {
		t.Fatalf("did not expect single api_key field for multi-key provider, got:\n%s", got)
	}
	if !strings.Contains(got, `- "k1"`) || !strings.Contains(got, `- "k2"`) {
		t.Fatalf("expected both keys in yaml, got:\n%s", got)
	}
}

func TestFormatClientConfigYAML_OAuthProviderYAML(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:          "codex-sean-example-com",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex_acct_123",
				OAuthIdentity: "acct:acct_123",
				Priority:      1,
				Enabled:       boolPtr(true),
			},
		},
	}

	got := string(formatClientConfigYAML("openai", cc))
	for _, want := range []string{
		`name: "codex-sean-example-com"`,
		`auth_type: "oauth"`,
		`oauth_provider: "codex"`,
		`oauth_ref: "codex_acct_123"`,
		`oauth_identity: "acct:acct_123"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "api_key:") {
		t.Fatalf("did not expect api_key field for oauth provider, got:\n%s", got)
	}
	if strings.Contains(got, "api_keys:\n") {
		t.Fatalf("did not expect api_keys block for oauth provider, got:\n%s", got)
	}
	if strings.Contains(got, "base_url:") {
		t.Fatalf("did not expect base_url for oauth provider without explicit override, got:\n%s", got)
	}

	var parsed config.ClientConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, got)
	}
	if len(parsed.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(parsed.Providers))
	}
	if got := parsed.Providers[0].NormalizedAuthType(); got != config.ProviderAuthTypeOAuth {
		t.Fatalf("auth_type = %q, want %q", got, config.ProviderAuthTypeOAuth)
	}
	if got := parsed.Providers[0].NormalizedOAuthProvider(); got != config.OAuthProviderCodex {
		t.Fatalf("oauth_provider = %q, want %q", got, config.OAuthProviderCodex)
	}
	if got := parsed.Providers[0].NormalizedOAuthRef(); got != "codex_acct_123" {
		t.Fatalf("oauth_ref = %q", got)
	}
	if got := parsed.Providers[0].NormalizedOAuthIdentity(); got != "acct:acct_123" {
		t.Fatalf("oauth_identity = %q", got)
	}
}

func TestFormatClientConfigYAML_OAuthProviderYAMLRoundTrip_ClaudeAndGemini(t *testing.T) {
	tests := []struct {
		name         string
		clientType   string
		providerName string
		oauthRef     string
	}{
		{
			name:         "claude",
			clientType:   "claude",
			providerName: string(config.OAuthProviderClaude),
			oauthRef:     "claude_acct_123",
		},
		{
			name:         "gemini",
			clientType:   "gemini",
			providerName: string(config.OAuthProviderGemini),
			oauthRef:     "gemini_acct_123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc := config.ClientConfig{
				Providers: []config.Provider{
					{
						Name:          tt.providerName + "-sean-example-com",
						AuthType:      config.ProviderAuthTypeOAuth,
						OAuthProvider: config.OAuthProvider(tt.providerName),
						OAuthRef:      tt.oauthRef,
						Priority:      1,
						Enabled:       boolPtr(true),
					},
				},
			}

			got := string(formatClientConfigYAML(tt.clientType, cc))
			for _, want := range []string{
				`auth_type: "oauth"`,
				`oauth_provider: "` + tt.providerName + `"`,
				`oauth_ref: "` + tt.oauthRef + `"`,
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("expected output to contain %q\n%s", want, got)
				}
			}
			if strings.Contains(got, "api_key:") {
				t.Fatalf("did not expect api_key field for oauth provider, got:\n%s", got)
			}
			if strings.Contains(got, "base_url:") {
				t.Fatalf("did not expect base_url for oauth provider without explicit override, got:\n%s", got)
			}

			var parsed config.ClientConfig
			if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
				t.Fatalf("yaml.Unmarshal: %v\n%s", err, got)
			}
			if len(parsed.Providers) != 1 {
				t.Fatalf("providers len = %d, want 1", len(parsed.Providers))
			}
			if got := parsed.Providers[0].NormalizedAuthType(); got != config.ProviderAuthTypeOAuth {
				t.Fatalf("auth_type = %q, want %q", got, config.ProviderAuthTypeOAuth)
			}
			if got := parsed.Providers[0].NormalizedOAuthProvider(); got != config.OAuthProvider(tt.providerName) {
				t.Fatalf("oauth_provider = %q, want %q", got, config.OAuthProvider(tt.providerName))
			}
			if got := parsed.Providers[0].NormalizedOAuthRef(); got != tt.oauthRef {
				t.Fatalf("oauth_ref = %q, want %q", got, tt.oauthRef)
			}
		})
	}
}

func TestFormatClientConfigYAML_SingleNormalizedKeyUsesAPIKeyField(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKeys: []string{"", " single ", "single"}, Priority: 1, Enabled: boolPtr(true)},
		},
	}

	got := string(formatClientConfigYAML("codex", cc))
	if strings.Contains(got, "api_keys:\n") {
		t.Fatalf("did not expect api_keys block, got:\n%s", got)
	}
	if !strings.Contains(got, `api_key: "single"`) {
		t.Fatalf("expected normalized api_key field, got:\n%s", got)
	}
}

func TestFormatClientConfigYAML_WritesProxySettingsOnlyWhenNeeded(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "default", BaseURL: "https://a.example", APIKey: "k1", Priority: 1, Enabled: boolPtr(true)},
			{Name: "direct", BaseURL: "https://b.example", APIKey: "k2", ProxyMode: config.ProviderProxyModeDirect, Priority: 2, Enabled: boolPtr(true)},
			{Name: "custom", BaseURL: "https://c.example", APIKey: "k3", ProxyMode: config.ProviderProxyModeCustom, ProxyURL: "http://127.0.0.1:7890", Priority: 3, Enabled: boolPtr(true)},
		},
	}

	got := string(formatClientConfigYAML("codex", cc))
	if strings.Count(got, "proxy_mode:") != 2 {
		t.Fatalf("expected exactly two proxy_mode entries, got:\n%s", got)
	}
	if !strings.Contains(got, `name: "direct"`) || !strings.Contains(got, `proxy_mode: "direct"`) {
		t.Fatalf("expected direct proxy_mode, got:\n%s", got)
	}
	if !strings.Contains(got, `name: "custom"`) || !strings.Contains(got, `proxy_mode: "custom"`) || !strings.Contains(got, `proxy_url: "http://127.0.0.1:7890"`) {
		t.Fatalf("expected custom proxy settings, got:\n%s", got)
	}
}

func TestFormatClientConfigYAML_RoundTripAndEscapesSpecialCharacters(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{Name: "single", BaseURL: "https://a.example", APIKey: "only-one", Priority: 2, Enabled: boolPtr(true)},
			{
				Name:     "multi",
				BaseURL:  "https://b.example",
				APIKeys:  []string{`quote"`, `slash\`, "line\r\nbreak", "tab\tkey", "control\x01"},
				Priority: 1,
				Enabled:  boolPtr(false),
			},
			{Name: "empty", BaseURL: "https://c.example", APIKey: "", Priority: 3, Enabled: boolPtr(true)},
		},
	}

	got := string(formatClientConfigYAML("codex", cc))
	for _, want := range []string{
		`mode: auto`,
		`pinned_provider: ""`,
		`api_key: ""`,
		`\\"`,
		`\\`,
		`\r\n`,
		`\t`,
		`\x01`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}

	var parsed config.ClientConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, got)
	}
	if parsed.Mode != config.ClientModeAuto {
		t.Fatalf("mode = %q, want %q", parsed.Mode, config.ClientModeAuto)
	}
	if parsed.PinnedProvider != "" {
		t.Fatalf("pinned_provider = %q, want empty", parsed.PinnedProvider)
	}
	if len(parsed.Providers) != 3 {
		t.Fatalf("providers len = %d, want 3", len(parsed.Providers))
	}

	if parsed.Providers[0].Name != "multi" {
		t.Fatalf("providers[0].name = %q, want multi", parsed.Providers[0].Name)
	}
	if gotKeys := strings.Join(parsed.Providers[0].APIKeys, "|"); gotKeys != strings.Join([]string{`quote"`, `slash\`, "line\r\nbreak", "tab\tkey", "control\x01"}, "|") {
		t.Fatalf("providers[0].api_keys = %#v", parsed.Providers[0].APIKeys)
	}
	if parsed.Providers[1].APIKey != "only-one" {
		t.Fatalf("providers[1].api_key = %q, want %q", parsed.Providers[1].APIKey, "only-one")
	}
	if parsed.Providers[2].APIKey != "" {
		t.Fatalf("providers[2].api_key = %q, want empty", parsed.Providers[2].APIKey)
	}
}

func TestFormatClientConfigYAML_RoundTripViaConfigLoadWithNormalizedKeys(t *testing.T) {
	dir := t.TempDir()
	yamlBytes := formatClientConfigYAML("codex", config.ClientConfig{
		Mode:           config.ClientModeManual,
		PinnedProvider: "p2",
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://a.example", APIKey: " key-1 ", Priority: 2, Enabled: boolPtr(true)},
			{Name: "p2", BaseURL: "https://b.example", APIKeys: []string{"dup", "", "dup", " second "}, Priority: 1, Enabled: boolPtr(true)},
		},
	})
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), yamlBytes, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if loaded.OpenAI.Mode != config.ClientModeManual {
		t.Fatalf("mode = %q, want %q", loaded.OpenAI.Mode, config.ClientModeManual)
	}
	if loaded.OpenAI.PinnedProvider != "p2" {
		t.Fatalf("pinned_provider = %q, want p2", loaded.OpenAI.PinnedProvider)
	}
	if len(loaded.OpenAI.Providers) != 2 {
		t.Fatalf("providers len = %d, want 2", len(loaded.OpenAI.Providers))
	}
	if loaded.OpenAI.Providers[0].Name != "p2" {
		t.Fatalf("providers[0].name = %q, want p2", loaded.OpenAI.Providers[0].Name)
	}
	if got := strings.Join(loaded.OpenAI.Providers[0].NormalizedAPIKeys(), "|"); got != "dup|second" {
		t.Fatalf("providers[0] normalized keys = %q, want dup|second", got)
	}
	if got := loaded.OpenAI.Providers[1].PrimaryAPIKey(); got != "key-1" {
		t.Fatalf("providers[1] primary api key = %q, want key-1", got)
	}
}

func TestFormatClientConfigYAML_IncludesProviderOverrides(t *testing.T) {
	cc := config.ClientConfig{
		Providers: []config.Provider{
			{
				Name:     "openai-primary",
				BaseURL:  "https://openai.example",
				APIKey:   "key-1",
				Priority: 1,
				Enabled:  boolPtr(true),
				Overrides: &config.ProviderOverrides{
					Model: stringPtr("gpt-5.4"),
					OpenAI: &config.OpenAIOverrides{
						ReasoningEffort: stringPtr("high"),
					},
					Claude: &config.ClaudeOverrides{
						ThinkingBudgetTokens: intPtr(1024),
					},
				},
			},
		},
	}

	got := string(formatClientConfigYAML("codex", cc))
	for _, want := range []string{
		`overrides:`,
		`model: "gpt-5.4"`,
		`openai:`,
		`reasoning_effort: "high"`,
		`claude:`,
		`thinking_budget_tokens: 1024`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}

	var parsed config.ClientConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, got)
	}
	if parsed.Providers[0].Overrides == nil {
		t.Fatalf("expected overrides after round-trip")
	}
	if got := parsed.Providers[0].ModelOverride(); got != "gpt-5.4" {
		t.Fatalf("model = %q", got)
	}
	if got := parsed.Providers[0].OpenAIReasoningEffort(); got != "high" {
		t.Fatalf("reasoning_effort = %q", got)
	}
	if got := parsed.Providers[0].ClaudeThinkingBudgetTokens(); got != 1024 {
		t.Fatalf("thinking_budget_tokens = %d", got)
	}
}

func TestFormatClientConfigYAML_EmptyProvidersStableOutput(t *testing.T) {
	got := string(formatClientConfigYAML("codex", config.ClientConfig{}))
	for _, want := range []string{
		`mode: auto`,
		`pinned_provider: ""`,
		"providers: []",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}

	var parsed config.ClientConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, got)
	}
	if parsed.Mode != config.ClientModeAuto {
		t.Fatalf("mode = %q, want %q", parsed.Mode, config.ClientModeAuto)
	}
	if len(parsed.Providers) != 0 {
		t.Fatalf("providers len = %d, want 0", len(parsed.Providers))
	}
}

func TestFormatGlobalConfigYAML_RoundTripAndEscapesSpecialCharacters(t *testing.T) {
	gc := config.DefaultGlobalConfig()
	gc.ListenAddr = `host"quoted"\path`
	gc.LogDir = "logs\r\nfolder\tcontrol\x01"
	gc.LogRetentionDays = 7
	gc.LogStdout = boolPtr(false)
	gc.UpstreamProxyMode = config.GlobalUpstreamProxyModeCustom
	gc.UpstreamProxyURL = "http://127.0.0.1:7890"
	gc.Notifications.ProviderSwitch = boolPtr(false)

	got := string(formatGlobalConfigYAML(gc))
	for _, want := range []string{
		`listen_addr: "host\"quoted\"\\path"`,
		`log_dir: "logs\r\nfolder\tcontrol\x01"`,
		`upstream_proxy_mode: "custom" # environment | direct | custom`,
		`upstream_proxy_url: "http://127.0.0.1:7890"`,
		`log_retention_days: 7 # default 7 days`,
		`log_stdout: false`,
		`provider_switch: false`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q\n%s", want, got)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(got), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loaded, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if loaded.Global.ListenAddr != gc.ListenAddr {
		t.Fatalf("listen_addr = %q, want %q", loaded.Global.ListenAddr, gc.ListenAddr)
	}
	if loaded.Global.LogDir != gc.LogDir {
		t.Fatalf("log_dir = %q, want %q", loaded.Global.LogDir, gc.LogDir)
	}
	if loaded.Global.NormalizedUpstreamProxyMode() != config.GlobalUpstreamProxyModeCustom {
		t.Fatalf("upstream_proxy_mode = %q, want custom", loaded.Global.NormalizedUpstreamProxyMode())
	}
	if loaded.Global.NormalizedUpstreamProxyURL() != "http://127.0.0.1:7890" {
		t.Fatalf("upstream_proxy_url = %q", loaded.Global.NormalizedUpstreamProxyURL())
	}
	if loaded.Global.LogRetentionDays != 7 {
		t.Fatalf("log_retention_days = %d, want 7", loaded.Global.LogRetentionDays)
	}
	if loaded.Global.LogStdout == nil || *loaded.Global.LogStdout {
		t.Fatalf("log_stdout = %v, want false", loaded.Global.LogStdout)
	}
	if loaded.Global.Notifications.ProviderSwitch == nil || *loaded.Global.Notifications.ProviderSwitch {
		t.Fatalf("provider_switch = %v, want false", loaded.Global.Notifications.ProviderSwitch)
	}
}
