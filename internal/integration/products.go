package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type managedFormat uint8

const (
	managedAuto managedFormat = iota
	managedJSON
	managedJSONC
	managedYAML
	managedTOML
	managedBytes
)

// TargetCapabilities is the single source for API validation and UI controls.
type TargetCapabilities struct {
	ConfigDir     bool `json:"config_dir"`
	ConfigFile    bool `json:"config_file"`
	Model         bool `json:"model"`
	ContextWindow bool `json:"context_window"`
	Profile       bool `json:"profile"`
	ShellDir      bool `json:"shell_dir"`
	Shell         bool `json:"shell"`
}

type productDefinition struct {
	ID          ProductID
	Name        string
	Plan        func(Manager, *config.Config) ([]takeoverFile, error)
	Format      managedFormat
	Targets     TargetCapabilities
	DefaultDir  func(Manager, string) (string, error)
	FileEnv     string
	PreferJSONC bool
	Warning     string
}

func homeDirectory(relative, variable string) func(Manager, string) (string, error) {
	return func(_ Manager, home string) (string, error) {
		if value := strings.TrimSpace(os.Getenv(variable)); variable != "" && value != "" {
			return value, nil
		}
		return filepath.Join(home, relative), nil
	}
}
func xdgDirectory(relative, variable string) func(Manager, string) (string, error) {
	return func(_ Manager, home string) (string, error) {
		if value := strings.TrimSpace(os.Getenv(variable)); variable != "" && value != "" {
			return value, nil
		}
		return filepath.Join(clientXDGRoot(home), relative), nil
	}
}

// Registry order determines the client card order. All planners share the
// transaction and backup lifecycle; descriptors contain no writes.
func productDefinitions() []productDefinition {
	return []productDefinition{
		{ID: ProductClaudeCode, Name: "Claude Code", Plan: Manager.planClaude, Format: managedJSON, Targets: TargetCapabilities{ConfigDir: true}, DefaultDir: homeDirectory(".claude", "CLAUDE_CONFIG_DIR")},
		{ID: ProductCodexCLI, Name: "Codex CLI", Plan: Manager.planCodex, Format: managedTOML, Targets: TargetCapabilities{ConfigDir: true, Profile: true}, DefaultDir: homeDirectory(".codex", "CODEX_HOME")},
		{ID: ProductOpenCode, Name: "OpenCode", Plan: Manager.planOpenCode, Format: managedJSONC, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: xdgDirectory("opencode", "OPENCODE_CONFIG_DIR"), FileEnv: "OPENCODE_CONFIG", PreferJSONC: true},
		{ID: ProductAntigravityCLI, Name: "Antigravity CLI", Plan: Manager.planAntigravity, Format: managedJSONC, Targets: TargetCapabilities{Shell: true, ShellDir: true}, DefaultDir: homeDirectory(".gemini/antigravity-cli", "")},
		{ID: ProductContinue, Name: "Continue", Plan: func(m Manager, c *config.Config) ([]takeoverFile, error) {
			p, e := m.continueTargetPath()
			if e != nil {
				return nil, e
			}
			return planSingleFile(p, func() (Preview, error) { return m.previewContinue(c) })
		}, Format: managedYAML, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: homeDirectory(".continue", "CONTINUE_GLOBAL_DIR")},
		{ID: ProductAider, Name: "Aider", Plan: func(m Manager, c *config.Config) ([]takeoverFile, error) {
			p, e := m.aiderTargetPath()
			if e != nil {
				return nil, e
			}
			return planSingleFile(p, func() (Preview, error) { return m.previewAider(c) })
		}, Format: managedYAML, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: homeDirectory("", "")},
		{ID: ProductGoose, Name: "Goose", Plan: func(m Manager, c *config.Config) ([]takeoverFile, error) {
			p, e := m.gooseTargetPath()
			if e != nil {
				return nil, e
			}
			return planSingleFile(p, func() (Preview, error) { return m.previewGoose(c) })
		}, Format: managedJSON, Targets: TargetCapabilities{ConfigDir: true, Model: true}, DefaultDir: func(_ Manager, home string) (string, error) {
			if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" {
				return filepath.Join(root, "config"), nil
			}
			return filepath.Join(clientXDGRoot(home), "goose"), nil
		}},
		{ID: ProductPi, Plan: Manager.planPi, Name: "Pi", Format: managedJSON, Targets: TargetCapabilities{ConfigDir: true, Model: true}, DefaultDir: homeDirectory(".pi/agent", "PI_CODING_AGENT_DIR"), Warning: "Select the Clipal provider and model; stored credentials, session model choices and project extensions can override routing."},
		{ID: ProductQwenCode, Plan: Manager.planQwen, Name: "Qwen Code", Format: managedJSONC, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: homeDirectory(".qwen", "QWEN_HOME"), Warning: "Project modelProviders replace user-level definitions; environment and command-line selections can override routing."},
		{ID: ProductKimiCLI, Plan: Manager.planKimi, Name: "Kimi Code CLI", Format: managedTOML, Targets: TargetCapabilities{ConfigDir: true, Model: true, ContextWindow: true}, DefaultDir: homeDirectory(".kimi-code", "KIMI_CODE_HOME"), Warning: "Keep model capabilities and context limits accurate. Project, environment, managed connections and auxiliary model choices may override routing."},
		{ID: ProductCopilotCLI, Plan: Manager.planCopilot, Name: "Copilot CLI", Format: managedBytes, Targets: TargetCapabilities{ConfigDir: true, Model: true, Shell: true, ShellDir: true}, DefaultDir: homeDirectory(".copilot", "COPILOT_HOME"), Warning: "Launch with the matching COPILOT_HOME in a fresh shell. Populated provider registries block this shell BYOK takeover; project and session selections may override routing."},
		{ID: ProductKiloCLI, Plan: Manager.planKilo, Name: "Kilo CLI", Format: managedJSONC, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: xdgDirectory("kilo", "KILO_CONFIG_DIR"), FileEnv: "KILO_CONFIG", PreferJSONC: true, Warning: "Launch with matching KILO_CONFIG/KILO_CONFIG_DIR; project, inline, session and managed settings may override routing."},
		{ID: ProductCodeBuddy, Plan: Manager.planCodeBuddy, Name: "CodeBuddy Code", Format: managedJSON, Targets: TargetCapabilities{ConfigDir: true, Model: true}, DefaultDir: homeDirectory(".codebuddy", "CODEBUDDY_CONFIG_DIR"), Warning: "Custom model URLs, environment, project settings, auxiliary and subagent model selections may override routing."},
		{ID: ProductCrush, Plan: Manager.planCrush, Name: "Crush", Format: managedJSON, Targets: TargetCapabilities{ConfigDir: true, Model: true}, DefaultDir: xdgDirectory("crush", "CRUSH_GLOBAL_CONFIG"), Warning: "Only the supported legacy JSON user configuration is managed. Executable crushrc and machine-state routing overrides block takeover. System and project configuration still require runtime verification."},
		{ID: ProductIFlow, Plan: Manager.planIFlow, Name: "iFlow CLI", Format: managedJSON, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: homeDirectory(".iflow", ""), Warning: "Environment variants and project settings can override the selected endpoint and model."},
		{ID: ProductTRAEAgent, Plan: Manager.planTRAE, Name: "TRAE Agent", Format: managedYAML, Targets: TargetCapabilities{ConfigDir: true, ConfigFile: true, Model: true}, DefaultDir: func(Manager, string) (string, error) {
			return "", fmt.Errorf("select an absolute TRAE config_file or config_dir and launch with --config-file")
		}, Warning: "Launch TRAE Agent with --config-file pointing to this selected file. Auxiliary models and provider environment overrides are preserved."},
		{ID: ProductGrokBuild, Plan: Manager.planGrok, Name: "Grok Build", Format: managedTOML, Targets: TargetCapabilities{ConfigDir: true, Model: true}, DefaultDir: homeDirectory(".grok", "GROK_HOME"), Warning: "This configures model inference, not Grok session services. Enterprise, project, environment and helper overrides require runtime verification."},
		{ID: ProductDeepSeekHarness, Plan: Manager.planDeepSeekHarness, Name: "DeepSeek Harness", Format: managedYAML, Targets: TargetCapabilities{ConfigDir: true, Model: true}, DefaultDir: homeDirectory(".dsh", "DSH_HOME"), Warning: "Launch with the selected DSH_HOME. Profiles, plugins, CLI flags and auxiliary models may use other routes."},
	}

}

func lookupProduct(product ProductID) (productDefinition, bool) {
	for _, d := range productDefinitions() {
		if d.ID == product {
			return d, true
		}
	}
	return productDefinition{}, false
}
func IsSupportedProduct(product ProductID) bool {
	d, ok := lookupProduct(product)
	return ok && d.Plan != nil
}
func ProductTargetCapabilities(product ProductID) TargetCapabilities {
	d, _ := lookupProduct(product)
	return d.Targets
}
func SupportedProducts() []ProductID {
	out := make([]ProductID, 0, len(productDefinitions()))
	for _, d := range productDefinitions() {
		if d.Plan != nil {
			out = append(out, d.ID)
		}
	}
	return out
}
func ProductName(product ProductID) string {
	if d, ok := lookupProduct(product); ok {
		return d.Name
	}
	return string(product)
}
