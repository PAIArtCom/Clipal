package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func (m Manager) gooseTargetPath() (string, error) {
	return m.customClientPath(ProductGoose, "custom_providers/clipal.json")
}

func (m Manager) gooseConfigPath() (string, error) {
	dir, err := m.clientConfigDir(ProductGoose)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func (m Manager) planGoose(cfg *config.Config) ([]takeoverFile, error) {
	providerPath, err := m.gooseTargetPath()
	if err != nil {
		return nil, err
	}
	provider, err := planSingleFile(providerPath, func() (Preview, error) { return m.previewGoose(cfg) })
	if err != nil {
		return nil, err
	}

	configPath, err := m.gooseConfigPath()
	if err != nil {
		return nil, err
	}
	configFile, err := readBackupFile(configPath)
	if err != nil {
		return nil, err
	}
	configDoc := map[string]any{}
	if configFile.Existed {
		configDoc, err = readYAMLMap(configPath)
		if err != nil {
			return nil, fmt.Errorf("read Goose config: %w", err)
		}
	}

	// Goose resolves these environment variables before config.yaml. A file
	// takeover cannot win while they are exported by the launching process.
	if os.Getenv("GOOSE_PROVIDER") != "" || os.Getenv("GOOSE_MODEL") != "" {
		return nil, fmt.Errorf("GOOSE_PROVIDER and GOOSE_MODEL override config.yaml; unset them before applying Goose takeover")
	}

	providerDoc := map[string]any{}
	if err := json.Unmarshal(provider[0].Planned, &providerDoc); err != nil {
		return nil, fmt.Errorf("read planned Goose provider: %w", err)
	}
	models, _ := providerDoc["models"].([]any)
	if len(models) == 0 {
		return nil, fmt.Errorf("planned Goose provider has no model")
	}
	modelEntry, _ := models[0].(map[string]any)
	model, _ := modelEntry["name"].(string)
	if model == "" {
		return nil, fmt.Errorf("planned Goose provider model has no name")
	}

	providers, err := objectField(configDoc, "providers")
	if err != nil {
		return nil, fmt.Errorf("read Goose providers: %w", err)
	}
	active, err := objectField(providers, "clipal")
	if err != nil {
		return nil, fmt.Errorf("read Goose Clipal provider state: %w", err)
	}
	active["enabled"] = true
	active["model"] = model
	active["configured"] = true
	providers["clipal"] = active
	configDoc["providers"] = providers
	configDoc["active_provider"] = "clipal"
	configBytes, err := marshalYAMLMap(configDoc)
	if err != nil {
		return nil, fmt.Errorf("marshal Goose config: %w", err)
	}

	provider[0].Format = managedJSON
	return append(provider, takeoverFile{backupFile: configFile, Planned: configBytes, Format: managedYAML}), nil
}

func (m Manager) advertisedGooseBaseURL(cfg *config.Config) string {
	return m.advertisedBaseURL(cfg) + "/v1/chat/completions"
}

func (m Manager) previewGoose(cfg *config.Config) (Preview, error) {
	targetPath, err := m.gooseTargetPath()
	if err != nil {
		return Preview{}, err
	}

	current := ""
	if raw, err := os.ReadFile(targetPath); err == nil {
		current = string(raw)
	} else if !os.IsNotExist(err) {
		return Preview{}, fmt.Errorf("read Goose provider config for preview: %w", err)
	}

	body := map[string]any{}
	if current != "" {
		body, err = readJSONMap(targetPath)
		if err != nil {
			return Preview{}, err
		}
	}
	target, err := m.targetOptions(ProductGoose)
	if err != nil {
		return Preview{}, err
	}
	models, _ := body["models"].([]any)
	existing := ""
	if len(models) > 0 {
		item, _ := models[0].(map[string]any)
		existing, _ = item["name"].(string)
	}
	model, err := m.selectedModel(ProductGoose, targetPath, existing)
	if err != nil {
		return Preview{}, err
	}
	if len(models) == 0 || target.Model != "" {
		found := false
		for _, entry := range models {
			item, _ := entry.(map[string]any)
			if item["name"] == model {
				found = true
			}
		}
		if !found {
			models = append(models, map[string]any{"name": model})
		}
	}
	body["name"] = "clipal"
	body["engine"] = "openai"
	body["base_url"] = m.advertisedGooseBaseURL(cfg)
	body["models"] = models
	if body["display_name"] == nil {
		body["display_name"] = "Clipal"
	}
	if body["description"] == nil {
		body["description"] = "Clipal managed OpenAI-compatible provider"
	}
	if body["requires_auth"] == nil {
		body["requires_auth"] = false
	}
	plannedBytes, err := marshalJSONMap(body)
	if err != nil {
		return Preview{}, fmt.Errorf("marshal Goose preview: %w", err)
	}

	return Preview{
		Product:        ProductGoose,
		CurrentContent: current,
		PlannedContent: string(plannedBytes),
	}, nil
}
