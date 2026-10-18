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
	ConfigDir string `json:"config_dir,omitempty"`
	Profile   string `json:"profile,omitempty"`
	ShellDir  string `json:"shell_dir,omitempty"`
	Shell     string `json:"shell,omitempty"`
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func unifiedProduct(product ProductID) bool {
	return product == ProductCodexCLI || product == ProductClaudeCode || product == ProductAntigravityCLI
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

func validateTarget(product ProductID, target TargetOptions) error {
	if !unifiedProduct(product) {
		return fmt.Errorf("target selection is unsupported for %s", product)
	}
	for _, path := range []string{target.ConfigDir, target.ShellDir} {
		if path != "" && !filepath.IsAbs(path) {
			return fmt.Errorf("configuration directories must be absolute paths")
		}
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
	if target.ConfigDir != "" {
		return filepath.Clean(target.ConfigDir), nil
	}
	variable, defaultDir := "CODEX_HOME", ".codex"
	if product == ProductClaudeCode {
		variable, defaultDir = "CLAUDE_CONFIG_DIR", ".claude"
	}
	if product == ProductAntigravityCLI {
		variable, defaultDir = "", ".gemini/antigravity-cli"
	}
	if value := strings.TrimSpace(os.Getenv(variable)); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path", variable)
		}
		return filepath.Clean(value), nil
	}
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultDir), nil
}
