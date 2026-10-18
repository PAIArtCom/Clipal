package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
	toml "github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

func (m Manager) takeoverStatus(product ProductID, cfg *config.Config) (Status, error) {
	files, err := m.planTakeover(product, cfg)
	if err != nil {
		return Status{}, err
	}
	target, err := m.targetOptions(product)
	if err != nil {
		return Status{}, err
	}
	status := Status{Product: product, TargetPath: files[0].TargetPath, State: StateNotConfigured, BackupAvailable: m.hasLatestBackup(product), Verification: "not_verified", TargetOptions: target}
	configured := true
	for i, file := range files {
		if !file.Existed {
			configured = false
			continue
		}
		if product == ProductAntigravityCLI && i > 0 {
			configured = configured && string(file.Original) == string(file.Planned)
			continue
		}
		var before, after map[string]any
		if product == ProductCodexCLI {
			err = toml.Unmarshal(file.Original, &before)
			if err == nil {
				err = toml.Unmarshal(file.Planned, &after)
			}
		} else {
			raw := file.Original
			if product == ProductAntigravityCLI {
				raw, err = hujson.Standardize(raw)
			}
			if err == nil {
				err = json.Unmarshal(raw, &before)
			}
			if err == nil {
				err = json.Unmarshal(file.Planned, &after)
			}
		}
		if err != nil {
			return Status{}, err
		}
		configured = configured && reflect.DeepEqual(before, after)
	}
	if configured {
		status.State = StateConfigured
	}
	warnings := []string{"Configuration inspection only; runtime routing has not been verified. Restart the client after apply or rollback."}
	blocked := []string{}
	switch product {
	case ProductCodexCLI:
		if target.Profile != "" {
			warnings = append(warnings, fmt.Sprintf("Launch Codex with --profile %s and the selected CODEX_HOME.", target.Profile))
		} else {
			matches, e := filepath.Glob(filepath.Join(filepath.Dir(files[0].TargetPath), "*.config.toml"))
			if e != nil {
				return Status{}, e
			}
			if len(matches) > 0 {
				warnings = append(warnings, "Codex profile files can override this configuration; select the profile you use before applying.")
			}
		}
	case ProductClaudeCode:
		warnings = append(warnings, "Project, local, --settings, or managed Claude Code settings may override this user configuration.")
		body := map[string]any{}
		if files[0].Existed {
			body, err = readJSONMap(files[0].TargetPath)
			if err != nil {
				return Status{}, err
			}
		}
		env, _ := body["env"].(map[string]any)
		keys := []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_MANTLE"}
		for _, key := range keys {
			value, exists := env[key]
			v, _ := value.(string)
			if !exists {
				v = os.Getenv(key)
			}
			if takeoverFlagEnabled(v) {
				blocked = append(blocked, key+" selects another provider")
			}
		}
		if takeoverFlagEnabled(os.Getenv("CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST")) {
			blocked = append(blocked, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST prevents user settings from changing routing")
		}
		if claudeEnvHasClientAuth(env) && os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
			warnings = append(warnings, "A shell OAuth token is present; check Claude /status for credential conflicts.")
		}
	case ProductAntigravityCLI:
		var paths []string
		for _, file := range files[1:] {
			paths = append(paths, file.TargetPath)
		}
		warnings = append(warnings, "Shell startup files: "+strings.Join(paths, ", ")+". Open a fresh terminal to load or clear exported credentials; sourcing a restored profile does not unset old variables.")
	}
	if len(blocked) > 0 {
		status.Verification = "blocked"
		warnings = append(warnings, strings.Join(blocked, "; ")+". Resolve the conflict in the client launch environment or its settings.")
	}
	status.Warning = strings.Join(warnings, " ")
	return status, nil
}

func takeoverFlagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no":
		return false
	}
	return true
}
