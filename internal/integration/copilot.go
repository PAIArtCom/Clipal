package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// The official @github/copilot platform packages 1.0.98 and 1.0.100-1
// document BYOK environment variables in `help providers` (checked 2026-10-18).
// https://registry.npmjs.org/@github/copilot-darwin-arm64/1.0.98
// https://registry.npmjs.org/@github/copilot-darwin-arm64/1.0.100-1
// https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models
// Newer configuration docs describe providers.json taking precedence, but do
// not specify its provider/model schema; refuse populated registries rather
// than guessing that schema or claiming shell exports override it.
// https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference
func (m Manager) planCopilot(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductCopilotCLI)
	if err != nil {
		return nil, err
	}
	registryPath := filepath.Join(dir, "providers.json")
	if override := strings.TrimSpace(os.Getenv("COPILOT_PROVIDERS_CONFIG")); override != "" {
		if !filepath.IsAbs(override) {
			return nil, fmt.Errorf("COPILOT_PROVIDERS_CONFIG must be absolute")
		}
		registryPath = filepath.Clean(override)
	}
	registryFile, err := readBackupFile(registryPath)
	if err != nil {
		return nil, err
	}
	registry, err := parseJSONObject(registryFile)
	if err != nil {
		return nil, fmt.Errorf("read Copilot provider registry: %w", err)
	}
	for _, key := range []string{"providers", "models"} {
		value, exists := registry[key]
		if !exists {
			continue
		}
		count := 0
		switch value := value.(type) {
		case map[string]any:
			count = len(value)
		case []any:
			count = len(value)
		default:
			return nil, fmt.Errorf("copilot registry %s must be an object or array", key)
		}
		if count > 0 {
			return nil, fmt.Errorf("copilot %s declares %s that override BYOK environment settings; use an empty provider registry before applying shell takeover", registryPath, key)
		}
	}
	model, err := m.selectedModel(ProductCopilotCLI, registryPath, "")
	if err != nil {
		return nil, err
	}
	files, err := m.planShellProfiles(ProductCopilotCLI, "Copilot CLI", [][2]string{
		{"COPILOT_PROVIDER_TYPE", "openai"},
		{"COPILOT_PROVIDER_WIRE_API", "completions"},
		{"COPILOT_PROVIDER_TRANSPORT", "http"},
		{"COPILOT_PROVIDER_BASE_URL", m.advertisedBaseURL(cfg) + "/v1"},
		{"COPILOT_PROVIDER_API_KEY", "clipal"},
		{"COPILOT_MODEL", model},
		{"COPILOT_PROVIDER_MODEL_ID", model},
		{"COPILOT_PROVIDER_WIRE_MODEL", model},
	}, "COPILOT_PROVIDER_BEARER_TOKEN", "COPILOT_PROVIDER_API_KEY_COMMAND", "COPILOT_PROVIDER_HEADERS")
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Dependencies = append(files[i].Dependencies, registryFile)
	}
	return files, nil
}
