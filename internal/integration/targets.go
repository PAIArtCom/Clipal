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
	ConfigFile    string `json:"config_file,omitempty"`
	ContextWindow int64  `json:"context_window,omitempty"`
	Model         string `json:"model,omitempty"`
	ConfigDir     string `json:"config_dir,omitempty"`
	Profile       string `json:"profile,omitempty"`
	ShellDir      string `json:"shell_dir,omitempty"`
	Shell         string `json:"shell,omitempty"`
}

var profileName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

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
	if !IsSupportedProduct(product) {
		return fmt.Errorf("target selection is unsupported for %s", product)
	}
	for _, path := range []string{target.ConfigDir, target.ShellDir, target.ConfigFile} {
		if path != "" && !filepath.IsAbs(path) {
			return fmt.Errorf("configuration directories must be absolute paths")
		}
	}
	if target.ConfigFile != "" && !ProductTargetCapabilities(product).ConfigFile {
		return fmt.Errorf("config_file is unsupported for %s", product)
	}
	if target.Model != "" && !modelSelectionProduct(product) {
		return fmt.Errorf("model selection is unsupported for %s", product)
	}
	if strings.ContainsAny(target.Model, "\r\n") {
		return fmt.Errorf("model must be a single line")
	}
	if target.Profile != "" && (!ProductTargetCapabilities(product).Profile || !profileName.MatchString(target.Profile)) {
		return fmt.Errorf("codex profile must contain only letters, numbers, underscores, or hyphens")
	}
	if target.ConfigDir != "" && !ProductTargetCapabilities(product).ConfigDir {
		return fmt.Errorf("antigravity CLI uses its fixed settings directory; only shell and shell_dir can be selected")
	}
	if target.Shell != "" && (!ProductTargetCapabilities(product).Shell || (target.Shell != "bash" && target.Shell != "zsh" && target.Shell != "sh")) {
		return fmt.Errorf("shell must be bash, zsh, or sh for this client")
	}
	if !ProductTargetCapabilities(product).ShellDir && target.ShellDir != "" {
		return fmt.Errorf("shell_dir is unsupported for this client")
	}
	if target.ContextWindow < 0 || (target.ContextWindow != 0 && !ProductTargetCapabilities(product).ContextWindow) {
		return fmt.Errorf("context_window must be positive and supported for %s", product)
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
		definition, ok := lookupProduct(product)
		if !ok || definition.DefaultDir == nil {
			return "", fmt.Errorf("unsupported client %s", product)
		}
		dir, e = definition.DefaultDir(m, home)
		if e != nil {
			return "", e
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
	return ProductTargetCapabilities(product).Model
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
	definition, _ := lookupProduct(product)
	if definition.FileEnv != "" && target.ConfigDir == "" {
		if value := os.Getenv(definition.FileEnv); value != "" {
			if !filepath.IsAbs(value) {
				return "", fmt.Errorf("%s must be absolute", definition.FileEnv)
			}
			return filepath.Clean(value), nil
		}
	}
	dir, err := m.clientConfigDir(product)
	if err != nil {
		return "", err
	}
	if definition.PreferJSONC {
		path := filepath.Join(dir, strings.TrimSuffix(filename, ".json")+".jsonc")
		if _, err := os.Stat(path); err == nil { // #nosec G703 -- absolute user-selected client paths are intentionally supported.
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return filepath.Join(dir, filename), nil
}
