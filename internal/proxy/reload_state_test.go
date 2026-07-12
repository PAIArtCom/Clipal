package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/telemetry"
	"gopkg.in/yaml.v3"
)

func boolPtr(v bool) *bool { return &v }

func writeProxyReloadFixture(t *testing.T, dir string, global config.GlobalConfig, codex config.ClientConfig) {
	t.Helper()

	globalBytes, err := yaml.Marshal(global)
	if err != nil {
		t.Fatalf("yaml.Marshal global: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), globalBytes, 0o600); err != nil {
		t.Fatalf("WriteFile config.yaml: %v", err)
	}

	codexBytes, err := yaml.Marshal(codex)
	if err != nil {
		t.Fatalf("yaml.Marshal codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), codexBytes, 0o600); err != nil {
		t.Fatalf("WriteFile openai.yaml: %v", err)
	}
}

func TestProviderConfigFiles_WatchesCurrentNamesOnly(t *testing.T) {
	router := &Router{}
	got := strings.Join(router.providerConfigFiles(), ",")
	for _, want := range []string{"config.yaml", "claude.yaml", "openai.yaml", "gemini.yaml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("providerConfigFiles missing %q: %s", want, got)
		}
	}
	for _, unwanted := range []string{"claude-code.yaml", "codex.yaml"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("providerConfigFiles should not include legacy name %q: %s", unwanted, got)
		}
	}
}

func newReloadTestRouter(t *testing.T) (*Router, string) {
	t.Helper()

	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	global.LogLevel = config.LogLevelInfo
	global.Notifications.Enabled = false
	global.CircuitBreaker.FailureThreshold = 2
	global.CircuitBreaker.SuccessThreshold = 1
	global.CircuitBreaker.OpenTimeout = "30s"
	global.CircuitBreaker.HalfOpenMaxInFlight = 1

	codex := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
		},
	}
	writeProxyReloadFixture(t, dir, global, codex)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return NewRouter(cfg), dir
}

func TestReloadProviderConfigsLocked_KeepOldConfigOnLoadOrValidationFailure(t *testing.T) {
	t.Run("load failure", func(t *testing.T) {
		router, dir := newReloadTestRouter(t)
		oldCfg := router.ConfigSnapshot()
		oldProxy := router.proxies[ClientOpenAI]

		if err := os.WriteFile(filepath.Join(dir, "openai.yaml"), []byte("providers: [\n"), 0o600); err != nil {
			t.Fatalf("WriteFile openai.yaml: %v", err)
		}

		levelCalls := 0
		notifyCalls := 0
		origSetLevel := loggerSetLevelFunc
		origNotify := notifyConfigureFunc
		loggerSetLevelFunc = func(config.LogLevel) { levelCalls++ }
		notifyConfigureFunc = func(config.NotificationsConfig) { notifyCalls++ }
		t.Cleanup(func() {
			loggerSetLevelFunc = origSetLevel
			notifyConfigureFunc = origNotify
		})

		if err := router.reloadProviderConfigsLocked(); err == nil {
			t.Fatalf("expected reload failure")
		}

		if router.ConfigSnapshot() != oldCfg {
			t.Fatalf("expected config pointer to stay unchanged on load failure")
		}
		if router.proxies[ClientOpenAI] != oldProxy {
			t.Fatalf("expected proxy pointer to stay unchanged on load failure")
		}
		if levelCalls != 0 || notifyCalls != 0 {
			t.Fatalf("unexpected reload side effects: level=%d notify=%d", levelCalls, notifyCalls)
		}
	})

	t.Run("validation failure", func(t *testing.T) {
		router, dir := newReloadTestRouter(t)
		oldCfg := router.ConfigSnapshot()
		oldProxy := router.proxies[ClientOpenAI]

		writeProxyReloadFixture(t, dir, config.DefaultGlobalConfig(), config.ClientConfig{
			Mode: config.ClientModeManual,
			Providers: []config.Provider{
				{Name: "p1", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
			},
		})

		levelCalls := 0
		notifyCalls := 0
		origSetLevel := loggerSetLevelFunc
		origNotify := notifyConfigureFunc
		loggerSetLevelFunc = func(config.LogLevel) { levelCalls++ }
		notifyConfigureFunc = func(config.NotificationsConfig) { notifyCalls++ }
		t.Cleanup(func() {
			loggerSetLevelFunc = origSetLevel
			notifyConfigureFunc = origNotify
		})

		if err := router.reloadProviderConfigsLocked(); err == nil {
			t.Fatalf("expected reload validation failure")
		}

		if router.ConfigSnapshot() != oldCfg {
			t.Fatalf("expected config pointer to stay unchanged on validation failure")
		}
		if router.proxies[ClientOpenAI] != oldProxy {
			t.Fatalf("expected proxy pointer to stay unchanged on validation failure")
		}
		if levelCalls != 0 || notifyCalls != 0 {
			t.Fatalf("unexpected reload side effects: level=%d notify=%d", levelCalls, notifyCalls)
		}
	})
}

func TestReloadProviderConfigsLocked_RebuildsLogLevelNotificationsAndBreakers(t *testing.T) {
	router, dir := newReloadTestRouter(t)
	oldProxy := router.proxies[ClientOpenAI]
	oldBreaker := oldProxy.breakers[0]
	oldBreaker.state = circuitOpen
	oldBreaker.openedAt = time.Now().Add(-5 * time.Second)

	levelCalls := []config.LogLevel{}
	notifyCalls := []config.NotificationsConfig{}
	origSetLevel := loggerSetLevelFunc
	origNotify := notifyConfigureFunc
	loggerSetLevelFunc = func(level config.LogLevel) {
		levelCalls = append(levelCalls, level)
		origSetLevel(level)
	}
	notifyConfigureFunc = func(cfg config.NotificationsConfig) {
		notifyCalls = append(notifyCalls, cfg)
	}
	t.Cleanup(func() {
		loggerSetLevelFunc = origSetLevel
		notifyConfigureFunc = origNotify
	})

	global := config.DefaultGlobalConfig()
	global.ListenAddr = "0.0.0.0"
	global.Port = 9999
	global.LogLevel = config.LogLevelDebug
	global.Notifications.Enabled = true
	global.Notifications.MinLevel = config.LogLevelWarn
	global.Notifications.ProviderSwitch = func() *bool { v := false; return &v }()
	global.CircuitBreaker.FailureThreshold = 5
	global.CircuitBreaker.SuccessThreshold = 3
	global.CircuitBreaker.OpenTimeout = "90s"
	global.CircuitBreaker.HalfOpenMaxInFlight = 2

	codex := config.ClientConfig{
		Mode:           config.ClientModeManual,
		PinnedProvider: "p2",
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://p1-new.example", APIKey: "k1", Priority: 2},
			{Name: "p2", BaseURL: "https://p2-new.example", APIKey: "k2", Priority: 1},
		},
	}
	writeProxyReloadFixture(t, dir, global, codex)

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	snapshot := router.ConfigSnapshot()
	if snapshot.Global.ListenAddr != "127.0.0.1" {
		t.Fatalf("listen_addr = %q, want old runtime listen addr", snapshot.Global.ListenAddr)
	}
	if snapshot.Global.Port != 3333 {
		t.Fatalf("port = %d, want old runtime port", snapshot.Global.Port)
	}
	if snapshot.Global.LogLevel != config.LogLevelDebug {
		t.Fatalf("log_level = %q, want debug", snapshot.Global.LogLevel)
	}
	if len(levelCalls) != 1 || levelCalls[0] != config.LogLevelDebug {
		t.Fatalf("levelCalls = %#v, want [debug]", levelCalls)
	}
	if len(notifyCalls) != 1 {
		t.Fatalf("notifyCalls = %#v", notifyCalls)
	}
	if !notifyCalls[0].Enabled || notifyCalls[0].MinLevel != config.LogLevelWarn {
		t.Fatalf("notify cfg = %#v", notifyCalls[0])
	}
	if notifyCalls[0].ProviderSwitch == nil || *notifyCalls[0].ProviderSwitch {
		t.Fatalf("provider_switch = %v, want false", notifyCalls[0].ProviderSwitch)
	}

	newProxy := router.proxies[ClientOpenAI]
	if newProxy == oldProxy {
		t.Fatalf("expected codex proxy to be rebuilt")
	}
	if newProxy.mode != config.ClientModeManual {
		t.Fatalf("mode = %q, want manual", newProxy.mode)
	}
	if newProxy.pinnedProvider != "p2" || newProxy.pinnedIndex != 0 || newProxy.currentIndex != 0 {
		t.Fatalf("pinned provider state = provider:%q pinnedIndex:%d currentIndex:%d", newProxy.pinnedProvider, newProxy.pinnedIndex, newProxy.currentIndex)
	}
	if len(newProxy.breakers) != 2 {
		t.Fatalf("breakers len = %d, want 2", len(newProxy.breakers))
	}
	if newProxy.breakers[0] == oldBreaker {
		t.Fatalf("expected breaker to be rebuilt")
	}
	if newProxy.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state = %s, want closed", newProxy.breakers[0].state)
	}
	if !newProxy.breakers[0].cfg.enabled ||
		newProxy.breakers[0].cfg.failureThreshold != 5 ||
		newProxy.breakers[0].cfg.successThreshold != 3 ||
		newProxy.breakers[0].cfg.halfOpenMaxInFlight != 2 ||
		newProxy.breakers[0].cfg.openTimeout != 90*time.Second {
		t.Fatalf("breaker cfg = %#v", newProxy.breakers[0].cfg)
	}
}

func TestReloadProviderConfigsLocked_PreservesRuntimeStateAcrossHarmlessReload(t *testing.T) {
	router, dir := newReloadTestRouter(t)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.currentIndex = 0
	oldProxy.countTokensIndex = 0
	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "key cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)
	oldProxy.lastSwitch = ProviderSwitchEvent{
		At:     now.Add(-2 * time.Second),
		From:   "p0",
		To:     "p1",
		Reason: "rate_limit",
		Status: http.StatusTooManyRequests,
	}
	oldProxy.lastRequest = RequestOutcomeEvent{
		At:       now.Add(-time.Second),
		Provider: "p1",
		Status:   http.StatusTooManyRequests,
		Result:   "all_providers_failed",
		Detail:   "p1 returned HTTP 429 Too Many Requests",
	}

	global := config.DefaultGlobalConfig()
	global.ListenAddr = "0.0.0.0"
	global.Port = 9999
	global.LogLevel = config.LogLevelWarn
	global.Notifications.Enabled = true
	global.Notifications.MinLevel = config.LogLevelError
	global.CircuitBreaker.FailureThreshold = 2
	global.CircuitBreaker.SuccessThreshold = 1
	global.CircuitBreaker.OpenTimeout = "30s"
	global.CircuitBreaker.HalfOpenMaxInFlight = 1
	writeProxyReloadFixture(t, dir, global, config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
		},
	})

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	if newProxy == oldProxy {
		t.Fatalf("expected proxy to be rebuilt")
	}
	if newProxy.currentIndex != 0 || newProxy.countTokensIndex != 0 {
		t.Fatalf("current indices = %d/%d, want 0/0", newProxy.currentIndex, newProxy.countTokensIndex)
	}
	if newProxy.deactivated[0].reason != "rate_limit" || newProxy.deactivated[0].message != "slow down" {
		t.Fatalf("deactivation = %#v", newProxy.deactivated[0])
	}
	if newProxy.keyDeactivated[0][0].message != "key cooldown" {
		t.Fatalf("key deactivation = %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitOpen {
		t.Fatalf("breaker state = %s, want open", newProxy.breakers[0].state)
	}
	if newProxy.lastSwitch != oldProxy.lastSwitch {
		t.Fatalf("lastSwitch = %#v, want %#v", newProxy.lastSwitch, oldProxy.lastSwitch)
	}
	if newProxy.lastRequest != oldProxy.lastRequest {
		t.Fatalf("lastRequest = %#v, want %#v", newProxy.lastRequest, oldProxy.lastRequest)
	}
}

func TestReloadProviderConfigsLocked_PreservesRuntimeStateAcrossEquivalentGlobalCustomProxyForms(t *testing.T) {
	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	global.UpstreamProxyMode = config.GlobalUpstreamProxyModeCustom
	global.UpstreamProxyURL = "socks5://proxy.example"

	codex := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
		},
	}
	writeProxyReloadFixture(t, dir, global, codex)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	router := NewRouter(cfg)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "key cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	global.UpstreamProxyURL = "SOCKS5://PROXY.EXAMPLE:1080"
	writeProxyReloadFixture(t, dir, global, codex)

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	wantPolicy := upstreamProxyPolicyKey{mode: upstreamProxyPolicyCustom, url: "socks5://proxy.example"}
	if newProxy.providerProxyPolicies[0] != wantPolicy {
		t.Fatalf("proxy policy = %#v, want %#v", newProxy.providerProxyPolicies[0], wantPolicy)
	}
	if newProxy.deactivated[0].reason != "rate_limit" || newProxy.deactivated[0].message != "slow down" {
		t.Fatalf("deactivation = %#v", newProxy.deactivated[0])
	}
	if newProxy.keyDeactivated[0][0].message != "key cooldown" {
		t.Fatalf("key deactivation = %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitOpen {
		t.Fatalf("breaker state = %s, want open", newProxy.breakers[0].state)
	}
}

func TestReloadProviderConfigsLocked_PreservesRuntimeStateAcrossEquivalentProviderCustomProxyForms(t *testing.T) {
	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333

	codex := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:      "p1",
				BaseURL:   "https://p1.example",
				APIKey:    "k1",
				Priority:  1,
				ProxyMode: config.ProviderProxyModeCustom,
				ProxyURL:  "http://proxy.example",
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, codex)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	router := NewRouter(cfg)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "key cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	codex.Providers[0].ProxyURL = "HTTP://PROXY.EXAMPLE:80/?ignored=1"
	writeProxyReloadFixture(t, dir, global, codex)

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	wantPolicy := upstreamProxyPolicyKey{mode: upstreamProxyPolicyCustom, url: "http://proxy.example"}
	if newProxy.providerProxyPolicies[0] != wantPolicy {
		t.Fatalf("proxy policy = %#v, want %#v", newProxy.providerProxyPolicies[0], wantPolicy)
	}
	if newProxy.deactivated[0].reason != "rate_limit" || newProxy.deactivated[0].message != "slow down" {
		t.Fatalf("deactivation = %#v", newProxy.deactivated[0])
	}
	if newProxy.keyDeactivated[0][0].message != "key cooldown" {
		t.Fatalf("key deactivation = %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitOpen {
		t.Fatalf("breaker state = %s, want open", newProxy.breakers[0].state)
	}
}

func TestReloadProviderConfigsLocked_DoesNotPreserveSuppressionStateWhenBaseURLChanges(t *testing.T) {
	router, dir := newReloadTestRouter(t)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "key cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	writeProxyReloadFixture(t, dir, global, config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://fresh.example", APIKey: "k1", Priority: 1},
		},
	})

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	if !newProxy.deactivated[0].until.IsZero() || newProxy.deactivated[0].reason != "" {
		t.Fatalf("provider cooldown should not carry across base_url change: %#v", newProxy.deactivated[0])
	}
	if !newProxy.keyDeactivated[0][0].until.IsZero() || newProxy.keyDeactivated[0][0].reason != "" {
		t.Fatalf("key cooldown should not carry across base_url change: %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state = %s, want closed", newProxy.breakers[0].state)
	}
}

func TestReloadProviderConfigsLocked_ReconcilesTelemetryFromYAMLChanges(t *testing.T) {
	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	initial := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:     "rename-me",
				BaseURL:  "https://rename.example",
				APIKeys:  []string{"key-b", "key-a"},
				Priority: 1,
				Enabled:  boolPtr(false),
			},
			{
				Name:     "delete-me",
				BaseURL:  "https://delete.example",
				APIKey:   "delete-key",
				Priority: 2,
				Enabled:  boolPtr(false),
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, initial)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	router := NewRouter(cfg)
	if router.telemetry == nil {
		t.Fatalf("expected telemetry store")
	}

	recordedAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	if err := router.telemetry.RecordUsage("openai", "rename-me", telemetry.UsageSnapshot{
		UsageDelta: telemetry.UsageDelta{InputTokens: 5, OutputTokens: 7},
		Usage:      map[string]any{"prompt_tokens": 5.0, "completion_tokens": 7.0, "total_tokens": 12.0},
	}, recordedAt); err != nil {
		t.Fatalf("RecordUsage rename-me: %v", err)
	}
	if err := router.telemetry.RecordUsage("openai", "delete-me", telemetry.UsageSnapshot{
		UsageDelta: telemetry.UsageDelta{InputTokens: 3, OutputTokens: 4},
		Usage:      map[string]any{"prompt_tokens": 3.0, "completion_tokens": 4.0, "total_tokens": 7.0},
	}, recordedAt.Add(time.Minute)); err != nil {
		t.Fatalf("RecordUsage delete-me: %v", err)
	}

	reloaded := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:     "renamed-provider",
				BaseURL:  "https://rename.example",
				APIKeys:  []string{"key-a", "key-b"},
				Priority: 1,
				Enabled:  boolPtr(false),
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, reloaded)

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	if _, ok := router.telemetry.ProviderSnapshot("openai", "rename-me"); ok {
		t.Fatalf("expected old renamed provider telemetry to be removed")
	}
	renamed, ok := router.telemetry.ProviderSnapshot("openai", "renamed-provider")
	if !ok {
		t.Fatalf("expected renamed provider telemetry to be preserved")
	}
	if renamed.TotalTokens != 12 || renamed.RequestCount != 1 || renamed.SuccessCount != 1 {
		t.Fatalf("renamed telemetry = %#v", renamed)
	}
	if _, ok := router.telemetry.ProviderSnapshot("openai", "delete-me"); ok {
		t.Fatalf("expected deleted provider telemetry to be removed")
	}
}

func TestCoordinateDataTransferKeepsWatcherBehindTelemetryTransaction(t *testing.T) {
	router, dir := newReloadTestRouter(t)
	router.snapshotProviderConfigModTimes()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	reloaded := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p2", BaseURL: "https://p2.example", APIKey: "k2", Priority: 1},
		},
	}
	writeProxyReloadFixture(t, dir, global, reloaded)

	watcherStarted := make(chan struct{})
	watcherDone := make(chan struct{})
	err := router.CoordinateDataTransfer(func(reload func() error) error {
		usageTx, err := router.telemetry.BeginTransfer()
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				_ = usageTx.Rollback()
			}
		}()
		go func() {
			close(watcherStarted)
			router.reloadIfProviderConfigsChanged()
			close(watcherDone)
		}()
		<-watcherStarted
		select {
		case <-watcherDone:
			return fmt.Errorf("watcher entered reload while transfer session held reloadMu")
		case <-time.After(20 * time.Millisecond):
		}
		if err := reload(); err != nil {
			return err
		}
		usageTx.Commit()
		committed = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-watcherDone:
	case <-time.After(time.Second):
		t.Fatal("watcher did not resume after transfer released reloadMu")
	}
}

func TestReloadProviderConfigsLocked_DoesNotPreserveSuppressionStateWhenProxyChanges(t *testing.T) {
	router, dir := newReloadTestRouter(t)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "key cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	writeProxyReloadFixture(t, dir, global, config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:      "p1",
				BaseURL:   "https://p1.example",
				APIKey:    "k1",
				ProxyMode: config.ProviderProxyModeDirect,
				Priority:  1,
			},
		},
	})

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	if !newProxy.deactivated[0].until.IsZero() || newProxy.deactivated[0].reason != "" {
		t.Fatalf("provider cooldown should not carry across proxy change: %#v", newProxy.deactivated[0])
	}
	if !newProxy.keyDeactivated[0][0].until.IsZero() || newProxy.keyDeactivated[0][0].reason != "" {
		t.Fatalf("key cooldown should not carry across proxy change: %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state = %s, want closed", newProxy.breakers[0].state)
	}
}

func TestReloadProviderConfigsLocked_DoesNotPreserveSuppressionStateWhenGlobalProxyChangesForDefaultProvider(t *testing.T) {
	router, dir := newReloadTestRouter(t)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "key cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	global.UpstreamProxyMode = config.GlobalUpstreamProxyModeDirect
	writeProxyReloadFixture(t, dir, global, config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{Name: "p1", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
		},
	})

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	if !newProxy.deactivated[0].until.IsZero() || newProxy.deactivated[0].reason != "" {
		t.Fatalf("provider cooldown should not carry across global proxy change: %#v", newProxy.deactivated[0])
	}
	if !newProxy.keyDeactivated[0][0].until.IsZero() || newProxy.keyDeactivated[0][0].reason != "" {
		t.Fatalf("key cooldown should not carry across global proxy change: %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state = %s, want closed", newProxy.breakers[0].state)
	}
}

func TestReloadProviderConfigsLocked_PreservesOAuthSuppressionStateWhenIdentityMatches(t *testing.T) {
	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	initial := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:          "codex-oauth",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-sean-example-com",
				Priority:      1,
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, initial)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	router := NewRouter(cfg)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "account cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	reloaded := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:          "codex-oauth",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-sean-example-com",
				Priority:      1,
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, reloaded)

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	if got := newProxy.deactivated[0].reason; got != "rate_limit" {
		t.Fatalf("provider cooldown reason = %q", got)
	}
	if newProxy.deactivated[0].until.IsZero() {
		t.Fatalf("expected provider cooldown to be preserved")
	}
	if got := newProxy.keyDeactivated[0][0].reason; got != "rate_limit" {
		t.Fatalf("key cooldown reason = %q", got)
	}
	if newProxy.keyDeactivated[0][0].until.IsZero() {
		t.Fatalf("expected key cooldown to be preserved")
	}
	if newProxy.breakers[0].state != circuitOpen {
		t.Fatalf("breaker state = %s, want open", newProxy.breakers[0].state)
	}
}

func TestReloadProviderConfigsLocked_DoesNotPreserveOAuthSuppressionStateWhenOAuthRefChanges(t *testing.T) {
	dir := t.TempDir()
	global := config.DefaultGlobalConfig()
	global.ListenAddr = "127.0.0.1"
	global.Port = 3333
	initial := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:          "codex-oauth",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-sean-example-com",
				Priority:      1,
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, initial)

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	router := NewRouter(cfg)
	oldProxy := router.proxies[ClientOpenAI]
	now := time.Now()

	oldProxy.deactivated[0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(30 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "slow down",
	}
	oldProxy.keyDeactivated[0][0] = providerDeactivation{
		at:      now.Add(-time.Second),
		until:   now.Add(20 * time.Second),
		reason:  "rate_limit",
		status:  http.StatusTooManyRequests,
		message: "account cooldown",
	}
	oldProxy.breakers[0].state = circuitOpen
	oldProxy.breakers[0].openedAt = now.Add(-5 * time.Second)

	reloaded := config.ClientConfig{
		Mode: config.ClientModeAuto,
		Providers: []config.Provider{
			{
				Name:          "codex-oauth",
				AuthType:      config.ProviderAuthTypeOAuth,
				OAuthProvider: config.OAuthProviderCodex,
				OAuthRef:      "codex-sean-other-account",
				Priority:      1,
			},
		},
	}
	writeProxyReloadFixture(t, dir, global, reloaded)

	if err := router.reloadProviderConfigsLocked(); err != nil {
		t.Fatalf("reloadProviderConfigsLocked: %v", err)
	}

	newProxy := router.proxies[ClientOpenAI]
	if !newProxy.deactivated[0].until.IsZero() || newProxy.deactivated[0].reason != "" {
		t.Fatalf("provider cooldown should not carry across oauth_ref change: %#v", newProxy.deactivated[0])
	}
	if !newProxy.keyDeactivated[0][0].until.IsZero() || newProxy.keyDeactivated[0][0].reason != "" {
		t.Fatalf("key cooldown should not carry across oauth_ref change: %#v", newProxy.keyDeactivated[0][0])
	}
	if newProxy.breakers[0].state != circuitClosed {
		t.Fatalf("breaker state = %s, want closed", newProxy.breakers[0].state)
	}
}

func TestTimeUntilNextAvailable_PicksEarliestBlockedSource(t *testing.T) {
	cbCfg := circuitBreakerConfig{
		enabled:             true,
		failureThreshold:    2,
		successThreshold:    1,
		openTimeout:         10 * time.Second,
		halfOpenMaxInFlight: 1,
	}
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "provider-cooldown", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
		{Name: "key-cooldown", BaseURL: "https://p2.example", APIKey: "k2", Priority: 2},
		{Name: "breaker-open", BaseURL: "https://p3.example", APIKey: "k3", Priority: 3},
	}, time.Hour, 0, testResponseHeaderTimeout, cbCfg)

	now := time.Now()
	cp.deactivated[0] = providerDeactivation{until: now.Add(4 * time.Second), reason: "network"}
	cp.keyDeactivated[1][0] = providerDeactivation{until: now.Add(2 * time.Second), reason: "rate_limit"}
	cp.breakers[2].state = circuitOpen
	cp.breakers[2].openedAt = now.Add(-9 * time.Second)

	wait, reason, ok := cp.timeUntilNextAvailable()
	if !ok {
		t.Fatalf("expected next availability")
	}
	if reason != string(circuitBlockOpen) {
		t.Fatalf("reason = %q, want %q", reason, circuitBlockOpen)
	}
	if wait <= 0 || wait > 2*time.Second {
		t.Fatalf("wait = %v, want between 0 and 2s", wait)
	}
}

func TestHandleAllUnavailable_RetryAfterAndStatusBranches(t *testing.T) {
	tests := []struct {
		name       string
		reason     string
		wantStatus int
	}{
		{name: "rate limit", reason: "rate_limit", wantStatus: http.StatusTooManyRequests},
		{name: "service unavailable", reason: "network", wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
				{Name: "p1", BaseURL: "https://p1.example", APIKey: "k1", Priority: 1},
			}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
			cp.deactivated[0] = providerDeactivation{
				until:  time.Now().Add(1500 * time.Millisecond),
				reason: tt.reason,
			}

			rr := httptest.NewRecorder()
			if !cp.handleAllUnavailable(rr) {
				t.Fatalf("expected handler to write response")
			}

			res := rr.Result()
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantStatus)
			}
			retryAfter := res.Header.Get("Retry-After")
			if retryAfter == "" {
				t.Fatalf("expected Retry-After header")
			}
			secs, err := strconv.Atoi(retryAfter)
			if err != nil || secs < 1 {
				t.Fatalf("Retry-After = %q, err=%v", retryAfter, err)
			}
		})
	}
}
