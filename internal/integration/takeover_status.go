package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
	toml "github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"
)

func (m Manager) takeoverStatus(product ProductID, cfg *config.Config) (Status, error) {
	files, err := m.planTakeover(product, cfg)
	if err != nil {
		var conflict *openCodeConflictError
		if errors.As(err, &conflict) {
			target, e := m.targetOptions(product)
			if e != nil {
				return Status{}, e
			}
			return Status{Product: product, State: StateNotConfigured, TargetPath: conflict.path, TargetOptions: target, BackupAvailable: m.hasLatestBackup(product), Verification: "blocked", Warning: conflict.Error()}, nil
		}
		var selection *modelSelectionError
		if errors.As(err, &selection) {
			target, e := m.targetOptions(product)
			if e != nil {
				return Status{}, e
			}
			return Status{Product: product, State: StateNotConfigured, TargetPath: selection.path, TargetOptions: target, BackupAvailable: m.hasLatestBackup(product), Verification: "not_verified", Warning: selection.Error()}, nil
		}
		return Status{}, err
	}
	target, err := m.targetOptions(product)
	if err != nil {
		return Status{}, err
	}
	status := Status{Product: product, TargetPath: files[0].TargetPath, State: StateNotConfigured, BackupAvailable: m.hasLatestBackup(product), Verification: "not_verified", TargetOptions: target}
	configured := true
	for _, file := range files {
		if !file.Existed {
			configured = false
			continue
		}
		if file.Format == managedBytes {
			configured = configured && string(file.Original) == string(file.Planned)
			continue
		}
		var before, after any
		switch file.Format {
		case managedYAML:
			err = yaml.Unmarshal(file.Original, &before)
			if err == nil {
				err = yaml.Unmarshal(file.Planned, &after)
			}
		case managedTOML:
			var b, a map[string]any
			err = toml.Unmarshal(file.Original, &b)
			if err == nil {
				err = toml.Unmarshal(file.Planned, &a)
			}
			before, after = b, a
		default:
			raw := file.Original
			if file.Format == managedJSONC {
				raw, err = hujson.Standardize(append([]byte(nil), raw...))
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
	if definition, ok := lookupProduct(product); ok && definition.Warning != "" {
		warnings = append(warnings, definition.Warning)
	}
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
	case ProductOpenCode:
		warnings = append(warnings, "OpenCode project, inline, or managed configuration may override this file. Use the selected OPENCODE_CONFIG/OPENCODE_CONFIG_DIR when launching.")
		if os.Getenv("OPENCODE_CONFIG_CONTENT") != "" {
			warnings = append(warnings, "OPENCODE_CONFIG_CONTENT is present and may override routing.")
		}
	case ProductContinue:
		warnings = append(warnings, "Continue saved config and --config override the default file; launch cn --config with this path and select the Clipal model. IDE model selection is separate.")
	case ProductAider:
		warnings = append(warnings, "Launch Aider with --config and this file to avoid repo/current-directory overrides. CLI flags and .env can also override routing. Existing weak-model/editor-model settings are preserved and may use other providers.")
	case ProductGoose:
		warnings = append(warnings, "Select the Clipal custom provider and a gateway-supported model in Goose. Use the matching GOOSE_PATH_ROOT/config directory; environment and saved provider selection may override routing.")
	case ProductAntigravityCLI, ProductCopilotCLI:
		var paths []string
		for _, file := range files {
			if file.Format != managedBytes {
				continue
			}
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

func stringListContains(values []any, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
