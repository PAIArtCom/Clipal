package transfer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/oauth"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

func TestNativeCodecRoundTripAndRejectsLegacyShape(t *testing.T) {
	dataset := emptyDataset()
	dataset.Producer.Version = "v1.2.3"
	dataset.Data.Global = globalFromConfig(config.DefaultGlobalConfig())
	encoded, err := (nativeAdapter{}).Encode(dataset, EncodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := (nativeAdapter{}).Decode([]Input{{Name: "backup.json", Data: encoded}}, DecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != SchemaName || decoded.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected schema: %#v", decoded)
	}
	if _, _, err := (nativeAdapter{}).Decode([]Input{{Name: "legacy.json", Data: []byte(`{"global":{},"openai":{}}`)}}, DecodeOptions{}); err == nil {
		t.Fatal("legacy config shape should be rejected")
	}
}

func TestNativeCodecRejectsTrailingJSONAndUnknownClient(t *testing.T) {
	dataset := emptyDataset()
	dataset.Producer.Version = "test"
	dataset.Data.Global = globalFromConfig(config.DefaultGlobalConfig())
	encoded, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := (nativeAdapter{}).Decode([]Input{{Name: "trailing.json", Data: append(encoded, []byte(` {}`)...)}}, DecodeOptions{}); err == nil {
		t.Fatal("trailing JSON value should be rejected")
	}
	dataset.Data.Clients["future"] = Client{Mode: "auto", Providers: []Provider{}}
	encoded, err = json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := (nativeAdapter{}).Decode([]Input{{Name: "unknown-client.json", Data: encoded}}, DecodeOptions{}); err == nil {
		t.Fatal("unknown schema-v1 client should be rejected")
	}
}

func TestImportInputLimits(t *testing.T) {
	validChunk := make([]byte, MaxImportFileBytes)
	if err := validateInputs([]Input{
		{Name: "1.json", Data: validChunk},
		{Name: "2.json", Data: validChunk},
		{Name: "3.json", Data: validChunk},
		{Name: "4.json", Data: validChunk},
	}); err != nil {
		t.Fatalf("exact aggregate limit rejected: %v", err)
	}
	tests := []struct {
		name   string
		inputs []Input
	}{
		{name: "file count", inputs: make([]Input, MaxImportFiles+1)},
		{name: "file name", inputs: []Input{{Name: strings.Repeat("n", MaxImportFilenameBytes+1), Data: []byte("{}")}}},
		{name: "single file", inputs: []Input{{Name: "large.json", Data: make([]byte, MaxImportFileBytes+1)}}},
		{name: "aggregate", inputs: []Input{
			{Name: "1.json", Data: validChunk},
			{Name: "2.json", Data: validChunk},
			{Name: "3.json", Data: validChunk},
			{Name: "4.json", Data: validChunk},
			{Name: "5.json", Data: []byte{0}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateInputs(tc.inputs); err == nil {
				t.Fatal("expected input limit error")
			}
		})
	}
}

func TestAnalyzeCredentialsPreservesSourceFileCount(t *testing.T) {
	service, err := NewService(t.TempDir(), "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	credentials := []oauth.Credential{
		{Ref: "one", Provider: config.OAuthProviderCodex, AccessToken: "one"},
		{Ref: "two", Provider: config.OAuthProviderCodex, AccessToken: "two"},
	}
	plan, err := service.AnalyzeCredentials(credentials, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 || plan.Credentials != 2 {
		t.Fatalf("files=%d credentials=%d", plan.Files, plan.Credentials)
	}
}

func TestServiceNativeReplaceRoundTrip(t *testing.T) {
	source := t.TempDir()
	destination := t.TempDir()
	writeTestState(t, source, "source", "source-key")
	writeTestState(t, destination, "destination", "destination-key")

	sourceUsage, err := telemetry.NewStore(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceUsage.RecordUsage("openai", "source", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{InputTokens: 7, OutputTokens: 3}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := sourceUsage.Flush(); err != nil {
		t.Fatal(err)
	}
	cred := &oauth.Credential{Ref: "account", Provider: config.OAuthProviderCodex, Email: "owner@example.com", AccessToken: "secret-token"}
	if err := oauth.NewStore(source).Save(cred); err != nil {
		t.Fatal(err)
	}

	exporter, err := NewService(source, "v1.0.0", sourceUsage, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := exporter.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	importer, err := NewService(destination, "v1.0.0", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := importer.Analyze([]Input{{Name: "clipal-data.json", Data: data}}, FormatAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeReplace || !plan.Native {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if _, err := importer.Apply(plan); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.OpenAI.Providers) != 1 || cfg.OpenAI.Providers[0].Name != "source" || cfg.OpenAI.Providers[0].PrimaryAPIKey() != "source-key" {
		t.Fatalf("configuration not replaced: %#v", cfg.OpenAI)
	}
	credentials, err := oauth.NewStore(destination).List(config.OAuthProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].Email != "owner@example.com" {
		t.Fatalf("credentials not restored: %#v", credentials)
	}
	usage := importer.usage.Snapshot().Clients["openai"]["source"]
	if usage.TotalTokens != 10 {
		t.Fatalf("usage total=%d", usage.TotalTokens)
	}
}

func TestServiceNativeMergeUpdatesProvidersAndAddsUsage(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, "shared", "old-key")
	usageStore, err := telemetry.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := usageStore.RecordUsage("openai", "shared", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{TotalTokens: 5}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := usageStore.Flush(); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(dir, "test", usageStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := service.Export()
	if err != nil {
		t.Fatal(err)
	}
	dataset.Data.Global.Port = 4444
	dataset.Data.Clients["openai"] = Client{Mode: "auto", Providers: []Provider{
		{Name: "shared", BaseURL: "https://updated.example.com", APIKeys: []string{"new-key"}, AuthType: "api_key", Priority: 1},
		{Name: "added", BaseURL: "https://added.example.com", APIKeys: []string{"added-key"}, AuthType: "api_key", Priority: 2},
	}}
	data, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Analyze([]Input{{Name: "merge.json", Data: data}}, FormatClipal, ModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(plan); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Global.Port != 4444 || len(cfg.OpenAI.Providers) != 2 {
		t.Fatalf("config merge failed: %#v", cfg)
	}
	if cfg.OpenAI.Providers[0].PrimaryAPIKey() != "new-key" || cfg.OpenAI.Providers[1].Name != "added" {
		t.Fatalf("provider merge failed: %#v", cfg.OpenAI.Providers)
	}
	if got := usageStore.Snapshot().Clients["openai"]["shared"].TotalTokens; got != 10 {
		t.Fatalf("merged usage=%d, want 10", got)
	}
}

func TestExternalImportIsMergeOnlyAndLinksProvider(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, "existing", "key")
	service, err := NewService(dir, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"type":"codex","email":"new@example.com","access_token":"token"}`)
	if _, err := service.Analyze([]Input{{Name: "credential.json", Data: input}}, FormatAuto, ModeReplace); err == nil {
		t.Fatal("external replace should fail")
	}
	plan, err := service.Analyze([]Input{{Name: "credential.json", Data: input}}, FormatAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Format != FormatCLIProxyAPI || plan.Mode != ModeMerge {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if _, err := service.Apply(plan); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.OpenAI.Providers) != 2 {
		t.Fatalf("expected existing plus linked provider, got %#v", cfg.OpenAI.Providers)
	}
	if !cfg.OpenAI.Providers[1].UsesOAuth() || cfg.OpenAI.Providers[1].OAuthProvider != config.OAuthProviderCodex {
		t.Fatalf("credential not linked: %#v", cfg.OpenAI.Providers[1])
	}
}

func TestAutoDetectionCombinesDifferentExternalAdapters(t *testing.T) {
	registry := NewRegistry()
	inputs := []Input{
		{Name: "codex.json", Data: []byte(`{"type":"codex","email":"one@example.com","access_token":"one"}`)},
		{Name: "sub2api.json", Data: []byte(`{"accounts":[{"platform":"anthropic","type":"oauth","credentials":{"email":"two@example.com","account_id":"acct","access_token":"two"}}]}`)},
	}
	dataset, report, err := registry.Decode(FormatAuto, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if report.Format != FormatMixed || len(dataset.Data.Credentials) != 2 {
		t.Fatalf("unexpected mixed decode: format=%s credentials=%#v", report.Format, dataset.Data.Credentials)
	}
}

func TestApplyRollsBackAllDataWhenReloadFails(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, "before", "before-key")
	service, err := NewService(dir, "test", nil, func() error { return errors.New("reload failed") })
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := service.Export()
	if err != nil {
		t.Fatal(err)
	}
	dataset.Data.Clients["openai"] = Client{Mode: "auto", Providers: []Provider{{Name: "after", BaseURL: "https://example.com", APIKeys: []string{"after-key"}, AuthType: "api_key", Priority: 1}}}
	dataset.Data.Credentials = []Credential{{Ref: "rollback", Provider: "codex", Email: "rollback@example.com", AccessToken: "token"}}
	data, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Analyze([]Input{{Name: "backup.json", Data: data}}, FormatClipal, ModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(plan); err == nil {
		t.Fatal("expected reload failure")
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.OpenAI.Providers) != 1 || cfg.OpenAI.Providers[0].Name != "before" {
		t.Fatalf("configuration rollback failed: %#v", cfg.OpenAI)
	}
	credentials, err := oauth.NewStore(dir).List(config.OAuthProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 0 {
		t.Fatalf("credential rollback failed: %#v", credentials)
	}
	if _, err := os.Stat(filepath.Join(dir, "usage.json")); !os.IsNotExist(err) {
		t.Fatalf("usage file presence was not rolled back: %v", err)
	}
}

func TestApplyRollbackPreservesOAuthRootSymlinkAndTarget(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, "before", "before-key")
	external := t.TempDir()
	linkTarget := filepath.Base(external)
	// Use a relative target so rollback verifies the exact symlink text as well
	// as the external directory contents.
	if err := os.Symlink(filepath.Join("..", linkTarget), filepath.Join(dir, "oauth")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	old := &oauth.Credential{Ref: "old", Provider: config.OAuthProviderCodex, Email: "old@example.com", AccessToken: "old-token"}
	if err := oauth.NewStore(dir).Save(old); err != nil {
		t.Fatal(err)
	}
	usageStore, err := telemetry.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := usageStore.RecordUsage("openai", "before", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{TotalTokens: 7}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := usageStore.Flush(); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(dir, "test", usageStore, func() error { return errors.New("reload failed") })
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := service.Export()
	if err != nil {
		t.Fatal(err)
	}
	dataset.Data.Credentials = []Credential{{Ref: "new", Provider: "codex", Email: "new@example.com", AccessToken: "new-token"}}
	dataset.Data.Clients["openai"] = Client{Mode: "auto", Providers: []Provider{{Name: "new", AuthType: "oauth", OAuthProvider: "codex", OAuthRef: "new", Priority: 1}}}
	dataset.Data.Usage.Clients["openai"] = map[string]ProviderUsage{"new": {TotalTokens: 99}}
	raw, _ := json.Marshal(dataset)
	plan, err := service.Analyze([]Input{{Name: "backup.json", Data: raw}}, FormatClipal, ModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(plan); err == nil {
		t.Fatal("expected reload failure")
	}
	gotTarget, err := os.Readlink(filepath.Join(dir, "oauth"))
	if err != nil {
		t.Fatal(err)
	}
	if gotTarget != filepath.Join("..", linkTarget) {
		t.Fatalf("symlink target=%q", gotTarget)
	}
	items, err := oauth.NewStore(dir).List(config.OAuthProviderCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Ref != "old" || items[0].AccessToken != "old-token" {
		t.Fatalf("credentials not exactly restored: %#v", items)
	}
	if got := usageStore.Snapshot().Clients["openai"]["before"].TotalTokens; got != 7 {
		t.Fatalf("usage rollback=%d", got)
	}
}

func TestApplySerializesLiveTelemetryWithoutLosingUsage(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, "provider", "key")
	usageStore, err := telemetry.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	service, err := NewService(dir, "test", usageStore, func() error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := service.Export()
	if err != nil {
		t.Fatal(err)
	}
	dataset.Data.Usage.Clients["openai"] = map[string]ProviderUsage{"provider": {TotalTokens: 100}}
	raw, _ := json.Marshal(dataset)
	plan, err := service.Analyze([]Input{{Name: "backup.json", Data: raw}}, FormatClipal, ModeReplace)
	if err != nil {
		t.Fatal(err)
	}
	applyDone := make(chan error, 1)
	go func() {
		_, err := service.Apply(plan)
		applyDone <- err
	}()
	<-entered
	recordDone := make(chan error, 1)
	go func() {
		recordDone <- usageStore.RecordUsage("openai", "provider", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{TotalTokens: 1}}, time.Now())
	}()
	select {
	case <-recordDone:
		t.Fatal("live telemetry write should wait for import transaction")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-applyDone; err != nil {
		t.Fatal(err)
	}
	if err := <-recordDone; err != nil {
		t.Fatal(err)
	}
	if got := usageStore.Snapshot().Clients["openai"]["provider"].TotalTokens; got != 101 {
		t.Fatalf("total tokens=%d want=101", got)
	}
}

func TestApplyFailureStagesRestorePreexistingDataExactly(t *testing.T) {
	stages := []string{"credentials", "config", "usage", "reload"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			writeTestState(t, dir, "before", "before-key")
			oldCredential := &oauth.Credential{Ref: "old", Provider: config.OAuthProviderCodex, Email: "old@example.com", AccessToken: "old-token"}
			if err := oauth.NewStore(dir).Save(oldCredential); err != nil {
				t.Fatal(err)
			}
			beforeOAuth := snapshotTestTree(t, filepath.Join(dir, "oauth"))
			usageStore, err := telemetry.NewStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := usageStore.RecordUsage("openai", "before", telemetry.UsageSnapshot{UsageDelta: telemetry.UsageDelta{TotalTokens: 7}}, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := usageStore.Flush(); err != nil {
				t.Fatal(err)
			}
			beforeUsage := usageStore.Snapshot()
			beforeUsageFile, err := os.ReadFile(filepath.Join(dir, "usage.json"))
			if err != nil {
				t.Fatal(err)
			}
			beforeConfigs := map[string][]byte{}
			for _, name := range config.WatchedConfigFilenames() {
				beforeConfigs[name], err = os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
			}

			service, err := NewService(dir, "test", usageStore, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "credentials":
				service.hooks.beforeSaveCredential = func(index int) error {
					if index == 1 {
						return errors.New("injected credential failure")
					}
					return nil
				}
			case "config":
				service.hooks.beforeWriteConfig = func(index int, _ string) error {
					if index == 2 {
						return errors.New("injected config failure")
					}
					return nil
				}
			case "usage":
				service.hooks.beforeUsageFlush = func() error { return errors.New("injected usage failure") }
			case "reload":
				service.reload = func() error { return errors.New("injected reload failure") }
			}
			dataset, err := service.Export()
			if err != nil {
				t.Fatal(err)
			}
			dataset.Data.Credentials = []Credential{
				{Ref: "new-1", Provider: "codex", Email: "new-1@example.com", AccessToken: "token-1"},
				{Ref: "new-2", Provider: "codex", Email: "new-2@example.com", AccessToken: "token-2"},
			}
			dataset.Data.Clients["openai"] = Client{Mode: "auto", Providers: []Provider{
				{Name: "new-1", AuthType: "oauth", OAuthProvider: "codex", OAuthRef: "new-1", Priority: 1},
				{Name: "new-2", AuthType: "oauth", OAuthProvider: "codex", OAuthRef: "new-2", Priority: 2},
			}}
			dataset.Data.Usage.Clients["openai"] = map[string]ProviderUsage{"new-1": {TotalTokens: 99}}
			raw, _ := json.Marshal(dataset)
			plan, err := service.Analyze([]Input{{Name: "backup.json", Data: raw}}, FormatClipal, ModeReplace)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Apply(plan); err == nil {
				t.Fatal("expected injected failure")
			}
			for name, want := range beforeConfigs {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s not exactly restored: err=%v", name, err)
				}
			}
			items, err := oauth.NewStore(dir).List(config.OAuthProviderCodex)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].Ref != "old" || items[0].AccessToken != "old-token" {
				t.Fatalf("credentials not restored: %#v", items)
			}
			if got := snapshotTestTree(t, filepath.Join(dir, "oauth")); !reflect.DeepEqual(got, beforeOAuth) {
				t.Fatalf("credential tree not exactly restored:\n got=%#v\nwant=%#v", got, beforeOAuth)
			}
			if got := usageStore.Snapshot(); !reflect.DeepEqual(got, beforeUsage) {
				t.Fatalf("usage state not restored: %#v", got)
			}
			gotUsageFile, err := os.ReadFile(filepath.Join(dir, "usage.json"))
			if err != nil || !reflect.DeepEqual(gotUsageFile, beforeUsageFile) {
				t.Fatalf("usage file not exactly restored: err=%v", err)
			}
		})
	}
}

type testTreeEntry struct {
	Mode fs.FileMode
	Data string
	Link string
}

func snapshotTestTree(t *testing.T, root string) map[string]testTreeEntry {
	t.Helper()
	out := map[string]testTreeEntry{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		snapshot := testTreeEntry{Mode: info.Mode()}
		if info.Mode()&os.ModeSymlink != 0 {
			snapshot.Link, err = os.Readlink(path)
		} else if info.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(path)
			snapshot.Data = string(data)
		}
		if err != nil {
			return err
		}
		out[rel] = snapshot
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeTestState(t *testing.T, dir, provider, key string) {
	t.Helper()
	cfg := &config.Config{Global: config.DefaultGlobalConfig(), Claude: config.ClientConfig{Mode: config.ClientModeAuto}, OpenAI: config.ClientConfig{Mode: config.ClientModeAuto, Providers: []config.Provider{{Name: provider, BaseURL: "https://example.com", APIKey: key, Priority: 1}}}, Gemini: config.ClientConfig{Mode: config.ClientModeAuto}}
	if err := writeConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range config.WatchedConfigFilenames() {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode=%o", name, info.Mode().Perm())
		}
	}
}

func TestApplyBlocksConcurrentCredentialStoreWrites(t *testing.T) {
	dir := t.TempDir()
	writeTestState(t, dir, "existing", "key")
	saveStarted := make(chan struct{})
	saveDone := make(chan struct{})
	var saveErr error
	service, err := NewService(dir, "test", nil, func() error {
		// reload runs late in apply, after the oauth snapshot, while the
		// transfer owns the credential store exclusively.
		go func() {
			defer close(saveDone)
			close(saveStarted)
			live := &oauth.Credential{Ref: "live", Provider: config.OAuthProviderCodex, Email: "live@example.com", AccessToken: "live-token"}
			saveErr = oauth.NewStore(dir).Save(live)
		}()
		<-saveStarted
		select {
		case <-saveDone:
			return errors.New("concurrent credential save completed while the transfer held the store")
		case <-time.After(20 * time.Millisecond):
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Analyze([]Input{{Name: "credential.json", Data: []byte(`{"type":"codex","email":"import@example.com","access_token":"token"}`)}}, FormatAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(plan); err != nil {
		t.Fatal(err)
	}
	select {
	case <-saveDone:
	case <-time.After(time.Second):
		t.Fatal("blocked credential save never resumed after the transfer finished")
	}
	if saveErr != nil {
		t.Fatalf("blocked credential save failed: %v", saveErr)
	}
	if _, err := oauth.NewStore(dir).Load(config.OAuthProviderCodex, "live"); err != nil {
		t.Fatalf("credential saved after the transfer is missing: %v", err)
	}
}

func TestApplyPreservesOAuthBackupWhenRollbackRestoreFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not restrict root")
	}
	dir := t.TempDir()
	writeTestState(t, dir, "existing", "key")
	old := &oauth.Credential{Ref: "old", Provider: config.OAuthProviderCodex, Email: "old@example.com", AccessToken: "old-token"}
	if err := oauth.NewStore(dir).Save(old); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(dir, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.hooks.beforeSaveCredential = func(int) error {
		// The oauth snapshot exists by now; make its restore fail so rollback
		// cannot copy the original credentials back.
		backups, err := filepath.Glob(filepath.Join(dir, ".clipal-transfer-oauth-*"))
		if err != nil || len(backups) != 1 {
			t.Fatalf("backup directory not found: %v %v", backups, err)
		}
		unreadable := ""
		walkErr := filepath.WalkDir(backups[0], func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() && unreadable == "" {
				unreadable = path
				return os.Chmod(path, 0o000)
			}
			return err
		})
		if walkErr != nil || unreadable == "" {
			t.Fatalf("could not make backup unreadable: %v %q", walkErr, unreadable)
		}
		return errors.New("boom")
	}
	plan, err := service.Analyze([]Input{{Name: "credential.json", Data: []byte(`{"type":"codex","email":"import@example.com","access_token":"token"}`)}}, FormatAuto, "")
	if err != nil {
		t.Fatal(err)
	}
	_, applyErr := service.Apply(plan)
	if applyErr == nil {
		t.Fatal("expected apply to fail")
	}
	if !strings.Contains(applyErr.Error(), "original credentials preserved at") {
		t.Fatalf("error does not point at the preserved backup: %v", applyErr)
	}
	backups, err := filepath.Glob(filepath.Join(dir, ".clipal-transfer-oauth-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("preserved backup directory missing: %v %v", backups, err)
	}
	if err := os.Chmod(filepath.Join(backups[0]), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestRelinkKeepsProvidersBoundToOtherAccounts(t *testing.T) {
	dir := t.TempDir()
	existing := &oauth.Credential{Ref: "default", Provider: config.OAuthProviderCodex, Email: "x@example.com", AccountID: "acct-x", AccessToken: "token-x"}
	if err := oauth.NewStore(dir).Save(existing); err != nil {
		t.Fatal(err)
	}
	enabled := true
	cfg := &config.Config{
		Global: config.DefaultGlobalConfig(),
		Claude: config.ClientConfig{Mode: config.ClientModeAuto},
		OpenAI: config.ClientConfig{Mode: config.ClientModeAuto, Providers: []config.Provider{{
			Name: "work", AuthType: config.ProviderAuthTypeOAuth, OAuthProvider: config.OAuthProviderCodex,
			OAuthRef: existing.Ref, OAuthIdentity: oauth.AccountIdentityKey(existing), Priority: 1, Enabled: &enabled,
		}}},
		Gemini: config.ClientConfig{Mode: config.ClientModeAuto},
	}
	if err := writeConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(dir, "test", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	incoming := oauth.Credential{Ref: "default", Provider: config.OAuthProviderCodex, Email: "y@example.com", AccountID: "acct-y", AccessToken: "token-y"}
	dataset, err := service.Export()
	if err != nil {
		t.Fatal(err)
	}
	// The dataset carries only the incoming account; the "work" provider
	// bound to account X stays in the live config and survives the merge.
	dataset.Data.Clients["openai"] = Client{Mode: "auto", Providers: []Provider{
		{Name: "imported", AuthType: string(config.ProviderAuthTypeOAuth), OAuthProvider: string(config.OAuthProviderCodex),
			OAuthRef: incoming.Ref, OAuthIdentity: oauth.AccountIdentityKey(&incoming), Priority: 2, Enabled: &enabled},
	}}
	dataset.Data.Credentials = []Credential{credentialFromOAuth(incoming)}
	data, err := json.Marshal(dataset)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Analyze([]Input{{Name: "backup.json", Data: data}}, FormatClipal, ModeMerge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(plan); err != nil {
		t.Fatal(err)
	}
	merged, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]config.Provider{}
	for _, p := range merged.OpenAI.Providers {
		byName[p.Name] = p
	}
	work, ok := byName["work"]
	if !ok || work.NormalizedOAuthRef() != "default" {
		t.Fatalf("provider bound to the original account was rewired: %#v", byName["work"])
	}
	imported, ok := byName["imported"]
	if !ok || imported.NormalizedOAuthRef() == "default" || imported.NormalizedOAuthRef() == "" {
		t.Fatalf("imported provider was not relinked to the disambiguated ref: %#v", byName["imported"])
	}
	relinked, err := oauth.NewStore(dir).Load(config.OAuthProviderCodex, imported.NormalizedOAuthRef())
	if err != nil {
		t.Fatal(err)
	}
	if oauth.AccountIdentityKey(relinked) != oauth.AccountIdentityKey(&incoming) {
		t.Fatalf("relinked ref resolves to the wrong account: %#v", relinked)
	}
}
