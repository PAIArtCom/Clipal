package integration

import (
	"fmt"
	"github.com/PAIArtCom/Clipal/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductRegistryCapabilities(t *testing.T) {
	seen := map[ProductID]bool{}
	for _, product := range SupportedProducts() {
		if seen[product] || !IsSupportedProduct(product) || ProductName(product) == string(product) {
			t.Fatalf("incomplete product %s", product)
		}
		seen[product] = true
		caps := ProductTargetCapabilities(product)
		tests := []struct {
			supported bool
			target    TargetOptions
		}{{caps.ConfigDir, TargetOptions{ConfigDir: t.TempDir()}}, {caps.ConfigFile, TargetOptions{ConfigFile: filepath.Join(t.TempDir(), "config")}}, {caps.Model, TargetOptions{Model: "arbitrary/model"}}, {caps.ContextWindow, TargetOptions{ContextWindow: 1024}}, {caps.Profile, TargetOptions{Profile: "work"}}, {caps.Shell, TargetOptions{Shell: "sh"}}, {caps.ShellDir, TargetOptions{ShellDir: t.TempDir()}}}
		for _, test := range tests {
			if err := validateTarget(product, test.target); (err == nil) != test.supported {
				t.Fatalf("%s target %+v supported %v: %v", product, test.target, test.supported, err)
			}
		}
	}
	if len(seen) != 18 || IsSupportedProduct("unknown") {
		t.Fatalf("unexpected product catalog: %v", seen)
	}
	products := SupportedProducts()
	products[0] = "mutated"
	if !IsSupportedProduct(ProductClaudeCode) {
		t.Fatal("caller mutated registry")
	}
}

func TestNewClientPathOverridesAndSelection(t *testing.T) {
	for _, test := range []struct {
		product  ProductID
		variable string
		relative string
	}{
		{ProductPi, "PI_CODING_AGENT_DIR", ".pi/agent"}, {ProductQwenCode, "QWEN_HOME", ".qwen"}, {ProductKimiCLI, "KIMI_CODE_HOME", ".kimi-code"}, {ProductCopilotCLI, "COPILOT_HOME", ".copilot"}, {ProductCodeBuddy, "CODEBUDDY_CONFIG_DIR", ".codebuddy"}, {ProductGrokBuild, "GROK_HOME", ".grok"}, {ProductDeepSeekHarness, "DSH_HOME", ".dsh"}, {ProductCrush, "CRUSH_GLOBAL_CONFIG", ".config/crush"}, {ProductKiloCLI, "KILO_CONFIG_DIR", ".config/kilo"},
	} {
		t.Run(string(test.product), func(t *testing.T) {
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			t.Setenv(test.variable, "")
			got, err := m.clientConfigDir(test.product)
			if err != nil || got != filepath.Join(m.homeDir, test.relative) {
				t.Fatalf("default %s %v", got, err)
			}
			foreign := t.TempDir()
			t.Setenv(test.variable, foreign)
			got, err = m.clientConfigDir(test.product)
			if err != nil || got != foreign {
				t.Fatalf("override %s %v", got, err)
			}
			selected := t.TempDir()
			if err = m.SetTarget(test.product, TargetOptions{ConfigDir: selected}); err != nil {
				t.Fatal(err)
			}
			got, err = m.clientConfigDir(test.product)
			if err != nil || got != selected {
				t.Fatalf("selected %s %v", got, err)
			}
			if _, err = os.Stat(filepath.Join(foreign, "config.toml")); !os.IsNotExist(err) {
				t.Fatal("path inspection wrote config")
			}
		})
	}
}

func TestNewClientsRejectMalformedBeforeCreatingBackup(t *testing.T) {
	for _, product := range SupportedProducts()[7:] {
		t.Run(string(product), func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, product)
			malformed := []byte("null")
			files, err := m.planTakeover(product, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if files[0].Format == managedTOML {
				malformed = []byte("[invalid")
			}
			if files[0].Format == managedBytes {
				malformed = []byte("# >>> Clipal Copilot CLI >>>\n")
			}
			if err = os.WriteFile(originals[0].TargetPath, malformed, originals[0].Mode); err != nil {
				t.Fatal(err)
			}
			originals[0].Original = malformed
			if _, err = m.Apply(product, cfg); err == nil {
				t.Fatal("accepted malformed config")
			}
			assertTakeoverFiles(t, originals)
			if m.hasLatestBackup(product) {
				t.Fatal("failed planning created baseline")
			}
		})
	}
}

func TestProductRegistryDoesNotOfferIneffectiveFileOverrides(t *testing.T) {
	for _, product := range []ProductID{ProductPi, ProductKimiCLI, ProductGrokBuild, ProductCopilotCLI, ProductCrush, ProductDeepSeekHarness} {
		if ProductTargetCapabilities(product).ConfigFile {
			t.Fatal("unsupported file option", product)
		}
	}
	if err := validateTarget(ProductKimiCLI, TargetOptions{ContextWindow: -1}); err == nil {
		t.Fatal("negative context accepted")
	}
	if err := validateTarget(ProductCopilotCLI, TargetOptions{Model: "bad\nmodel"}); err == nil || !strings.Contains(err.Error(), "single line") {
		t.Fatal("multiline model accepted")
	}
}

func TestKiloKnownLayersOrderAndReadOnlyGuards(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "custom"}[custom], func(t *testing.T) {
			m := Manager{homeDir: t.TempDir(), configDir: t.TempDir()}
			cfg := &config.Config{Global: config.DefaultGlobalConfig()}
			name, _, globals, dirs := openCodeLayout(ProductKiloCLI)
			root := filepath.Join(m.homeDir, ".config", name)
			names := globals
			target := TargetOptions{Model: "selected"}
			if custom {
				root = filepath.Join(m.homeDir, "custom")
				target.ConfigDir = root
				names = dirs
			}
			if err := m.SetTarget(ProductKiloCLI, target); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			for i, name := range names {
				value := fmt.Sprintf(`{"model":"other/model-%d","provider":{"clipal":{"options":{"headers":{"layer-%d":"keep"}}}}}`, i, i)
				if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0640); err != nil {
					t.Fatal(err)
				}
			}
			files, err := m.planTakeover(ProductKiloCLI, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if files[0].TargetPath != filepath.Join(root, names[len(names)-1]) {
				t.Fatalf("wrong last layer %s", files[0].TargetPath)
			}
			if _, err = m.Apply(ProductKiloCLI, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err = m.Rollback(ProductKiloCLI, cfg); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(files[0].TargetPath)
			m.beforeTakeoverWrite = func(_ int, _ string) error {
				return os.WriteFile(filepath.Join(root, names[0]), []byte(`{"disabled_providers":["clipal"]}`), 0640)
			}
			if _, err = m.Apply(ProductKiloCLI, cfg); err == nil {
				t.Fatal("ignored changed source")
			}
			after, _ := os.ReadFile(files[0].TargetPath)
			if string(after) != string(before) || m.hasLatestBackup(ProductKiloCLI) {
				t.Fatal("stale plan changed target or baseline")
			}
		})
	}
}

func TestKiloInlineRouteOverridesBlockApply(t *testing.T) {
	for _, inline := range []string{`{"disabled_providers":["clipal"]}`, `{"model":"other/ignored"}`, `{"provider":{"clipal":{"options":{"baseURL":"https://other.example"}}}}`} {
		t.Run(inline, func(t *testing.T) {
			m, cfg, originals := takeoverFixture(t, ProductKiloCLI)
			t.Setenv("KILO_CONFIG_CONTENT", inline)
			if _, err := m.Apply(ProductKiloCLI, cfg); err == nil {
				t.Fatal("ignored inline routing conflict")
			}
			assertTakeoverFiles(t, originals)
			if m.hasLatestBackup(ProductKiloCLI) {
				t.Fatal("conflict created baseline")
			}
		})
	}
}

// Keep heterogeneous configuration assertions strict without repeating casts.
func requireConfigValue[T any](t *testing.T, value any) T {
	t.Helper()
	typed, ok := value.(T)
	if !ok {
		t.Fatalf("configuration value has type %T; expected %T", value, typed)
	}
	return typed
}
