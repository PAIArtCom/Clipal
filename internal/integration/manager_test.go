package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestAdvertisedBaseURL_UsesConcreteListenAddr(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Global: config.DefaultGlobalConfig()}
	cfg.Global.ListenAddr = "192.168.1.10"
	cfg.Global.Port = 4455

	m := Manager{}
	if got := m.advertisedBaseURL(cfg); got != "http://192.168.1.10:4455/clipal" {
		t.Fatalf("advertisedBaseURL = %q", got)
	}
}

func TestAdvertisedBaseURL_FallsBackToLoopbackForWildcard(t *testing.T) {
	t.Parallel()

	for _, listenAddr := range []string{"0.0.0.0", "::", "[::]"} {
		cfg := &config.Config{Global: config.DefaultGlobalConfig()}
		cfg.Global.ListenAddr = listenAddr
		cfg.Global.Port = 3333

		m := Manager{}
		if got := m.advertisedBaseURL(cfg); got != "http://127.0.0.1:3333/clipal" {
			t.Fatalf("listen_addr=%q advertisedBaseURL=%q", listenAddr, got)
		}
	}
}

func TestBackupAndLatestManifest_ForExistingAndMissingFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	targetDir := filepath.Join(root, "home", ".claude")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	targetPath := filepath.Join(targetDir, "settings.json")
	if err := os.WriteFile(targetPath, []byte(`{"env":{"A":"B"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile target: %v", err)
	}

	m := Manager{configDir: filepath.Join(root, ".clipal")}

	snap1, err := m.createBackup(ProductClaudeCode, targetPath)
	if err != nil {
		t.Fatalf("createBackup existing: %v", err)
	}
	if !snap1.TargetExisted {
		t.Fatalf("expected first snapshot to report existing target")
	}
	if len(snap1.Original) == 0 {
		t.Fatalf("expected original bytes to be recorded")
	}

	latest1, err := m.loadLatestBackup(ProductClaudeCode)
	if err != nil {
		t.Fatalf("loadLatestBackup existing: %v", err)
	}
	if latest1.TargetPath != targetPath {
		t.Fatalf("latest target path = %q", latest1.TargetPath)
	}

	missingPath := filepath.Join(root, "home", ".codex", "config.toml")
	snap2, err := m.createBackup(ProductCodexCLI, missingPath)
	if err != nil {
		t.Fatalf("createBackup missing: %v", err)
	}
	if snap2.TargetExisted {
		t.Fatalf("expected missing snapshot to report non-existing target")
	}
	if len(snap2.Original) != 0 {
		t.Fatalf("expected no original bytes for missing target")
	}

	latest2, err := m.loadLatestBackup(ProductCodexCLI)
	if err != nil {
		t.Fatalf("loadLatestBackup missing: %v", err)
	}
	if latest2.TargetPath != missingPath {
		t.Fatalf("latest missing target path = %q", latest2.TargetPath)
	}
	if latest2.TargetExisted {
		t.Fatalf("expected latest missing target to remain non-existing")
	}
}

func TestSupportedProducts_IncludesGeminiCLI(t *testing.T) {
	t.Parallel()

	got := SupportedProducts()
	want := []ProductID{ProductClaudeCode, ProductCodexCLI, ProductOpenCode, ProductGeminiCLI, ProductContinue, ProductAider, ProductGoose}
	if len(got) != len(want) {
		t.Fatalf("len = %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("products[%d] = %q want %q", i, got[i], want[i])
		}
	}
	if got := ProductName(ProductOpenCode); got != "OpenCode" {
		t.Fatalf("ProductName(OpenCode) = %q", got)
	}
	if got := ProductName(ProductGeminiCLI); got != "Gemini CLI" {
		t.Fatalf("ProductName(GeminiCLI) = %q", got)
	}
	if got := ProductName(ProductContinue); got != "Continue" {
		t.Fatalf("ProductName(Continue) = %q", got)
	}
	if got := ProductName(ProductAider); got != "Aider" {
		t.Fatalf("ProductName(Aider) = %q", got)
	}
	if got := ProductName(ProductGoose); got != "Goose" {
		t.Fatalf("ProductName(Goose) = %q", got)
	}
}
