package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TargetOptions selects the user configuration layer to manage. Empty fields
// resolve from the client's environment and then its documented defaults.
type TargetOptions struct {
	ConfigFile string `json:"config_file,omitempty"`
	Model      string `json:"model,omitempty"`
	ConfigDir  string `json:"config_dir,omitempty"`
	Profile    string `json:"profile,omitempty"`
	ShellDir   string `json:"shell_dir,omitempty"`
	Shell      string `json:"shell,omitempty"`
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func unifiedProduct(product ProductID) bool {
	for _, supported := range SupportedProducts() {
		if product == supported {
			return true
		}
	}
	return false
}

func (m Manager) targetOptions(product ProductID) (TargetOptions, error) {
	data, err := os.ReadFile(filepath.Join(m.configDir, "integration-targets.json"))
	if os.IsNotExist(err) {
		return TargetOptions{}, nil
	}
	if err != nil {
		return TargetOptions{}, err
	}
	var all map[ProductID]TargetOptions
	if err = json.Unmarshal(data, &all); err != nil {
		return TargetOptions{}, fmt.Errorf("read integration targets: %w", err)
	}
	target := all[product]
	return target, validateTarget(product, target)
}

// GetTarget keeps the selected location visible when inspecting a malformed
// client file fails, so the UI can show or correct that selection.
func (m Manager) GetTarget(product ProductID) (TargetOptions, error) {
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	return m.targetOptions(product)
}

func validateTarget(product ProductID, target TargetOptions) error {
	if !unifiedProduct(product) {
		return fmt.Errorf("target selection is unsupported for %s", product)
	}
	for _, path := range []string{target.ConfigDir, target.ShellDir, target.ConfigFile} {
		if path != "" && !filepath.IsAbs(path) {
			return fmt.Errorf("configuration directories must be absolute paths")
		}
	}
	if target.ConfigFile != "" && product != ProductOpenCode && product != ProductContinue && product != ProductAider {
		return fmt.Errorf("config_file is unsupported for %s", product)
	}
	if target.Model != "" && !modelSelectionProduct(product) {
		return fmt.Errorf("model selection is unsupported for %s", product)
	}
	if strings.ContainsAny(target.Model, "\r\n") {
		return fmt.Errorf("model must be a single line")
	}
	if target.Profile != "" && (product != ProductCodexCLI || !profileName.MatchString(target.Profile)) {
		return fmt.Errorf("codex profile must contain only letters, numbers, underscores, or hyphens")
	}
	if product == ProductAntigravityCLI && target.ConfigDir != "" {
		return fmt.Errorf("antigravity CLI uses its fixed settings directory; only shell and shell_dir can be selected")
	}
	if target.Shell != "" && (product != ProductAntigravityCLI || (target.Shell != "bash" && target.Shell != "zsh" && target.Shell != "sh")) {
		return fmt.Errorf("shell must be bash, zsh, or sh for Antigravity CLI")
	}
	if product != ProductAntigravityCLI && target.ShellDir != "" {
		return fmt.Errorf("shell_dir is only supported for Antigravity CLI")
	}
	return nil
}

func (m Manager) SetTarget(product ProductID, target TargetOptions) error {
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	target.ConfigDir = strings.TrimSpace(target.ConfigDir)
	target.ConfigFile = strings.TrimSpace(target.ConfigFile)
	target.Model = strings.TrimSpace(target.Model)
	target.Profile = strings.TrimSpace(target.Profile)
	target.ShellDir = strings.TrimSpace(target.ShellDir)
	target.Shell = strings.TrimSpace(target.Shell)
	if err := validateTarget(product, target); err != nil {
		return err
	}
	if m.hasLatestBackup(product) {
		return fmt.Errorf("restore the active takeover before changing its target")
	}
	if _, err := m.loadTakeoverCycle(product); err == nil {
		return fmt.Errorf("restore the active takeover before changing its target")
	} else if !os.IsNotExist(err) {
		return err
	}
	path := filepath.Join(m.configDir, "integration-targets.json")
	before, err := readBackupFile(path)
	if err != nil {
		return err
	}
	all := map[ProductID]TargetOptions{}
	if before.Existed {
		if err = json.Unmarshal(before.Original, &all); err != nil {
			return err
		}
	}
	if all == nil {
		all = map[ProductID]TargetOptions{}
	}
	all[product] = target
	body, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return m.writeTakeoverFiles([]takeoverFile{{backupFile: before, Planned: append(body, '\n')}}, nil)
}

func (m Manager) clientConfigDir(product ProductID) (string, error) {
	target, err := m.targetOptions(product)
	if err != nil {
		return "", err
	}
	dir := target.ConfigDir
	if dir == "" {
		home, e := m.resolveHomeDir()
		if e != nil {
			return "", e
		}
		variable, defaultDir := "", ""
		switch product {
		case ProductCodexCLI:
			variable, defaultDir = "CODEX_HOME", filepath.Join(home, ".codex")
		case ProductClaudeCode:
			variable, defaultDir = "CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")
		case ProductAntigravityCLI:
			defaultDir = filepath.Join(home, ".gemini", "antigravity-cli")
		case ProductOpenCode:
			variable, defaultDir = "OPENCODE_CONFIG_DIR", filepath.Join(clientXDGRoot(home), "opencode")
		case ProductContinue:
			variable, defaultDir = "CONTINUE_GLOBAL_DIR", filepath.Join(home, ".continue")
		case ProductAider:
			defaultDir = home
		case ProductGoose:
			defaultDir = filepath.Join(clientXDGRoot(home), "goose")
			if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" {
				defaultDir = filepath.Join(root, "config")
			}
		default:
			return "", fmt.Errorf("unsupported client %s", product)
		}
		dir = strings.TrimSpace(os.Getenv(variable))
		if dir == "" {
			dir = defaultDir
		}
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("client configuration directory must be absolute")
	}
	return filepath.Clean(dir), nil
}

func clientXDGRoot(home string) string {
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return root
	}
	return filepath.Join(home, ".config")
}

func modelSelectionProduct(product ProductID) bool {
	return product == ProductOpenCode || product == ProductContinue || product == ProductAider || product == ProductGoose
}

// customClientPath uses only paths the client can load; explicit paths require
// launching the client with its matching --config/environment setting.
func (m Manager) customClientPath(product ProductID, filename string) (string, error) {
	target, err := m.targetOptions(product)
	if err != nil {
		return "", err
	}
	if target.ConfigFile != "" {
		return filepath.Clean(target.ConfigFile), nil
	}
	if product == ProductOpenCode && target.ConfigDir == "" {
		if value := os.Getenv("OPENCODE_CONFIG"); value != "" {
			if !filepath.IsAbs(value) {
				return "", fmt.Errorf("OPENCODE_CONFIG must be absolute")
			}
			return filepath.Clean(value), nil
		}
	}
	dir, err := m.clientConfigDir(product)
	if err != nil {
		return "", err
	}
	if product == ProductOpenCode {
		path := filepath.Join(dir, "opencode.jsonc")
		if _, err := os.Stat(path); err == nil { // #nosec G703 -- absolute user-selected client paths are intentionally supported.
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return filepath.Join(dir, filename), nil
}
