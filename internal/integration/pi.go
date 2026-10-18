package integration

import (
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// Pi models/settings contract: earendil-works/pi at 482c1381a00d.
// https://github.com/earendil-works/pi/blob/482c1381a00d8d00bc81697e4a980c593db6803e/packages/coding-agent/docs/models.md
func (m Manager) planPi(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductPi)
	if err != nil {
		return nil, err
	}
	modelsFile, err := readBackupFile(filepath.Join(dir, "models.json"))
	if err != nil {
		return nil, err
	}
	settingsFile, err := readBackupFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return nil, err
	}
	models, err := parseJSONObject(modelsFile)
	if err != nil {
		return nil, err
	}
	settings, err := parseJSONObject(settingsFile)
	if err != nil {
		return nil, err
	}
	providers, err := objectField(models, "providers")
	if err != nil {
		return nil, err
	}
	for name := range providers {
		candidate, e := objectField(providers, name)
		if e != nil {
			return nil, e
		}
		if _, e = objectArrayField(candidate, "models"); e != nil {
			return nil, e
		}
	}
	provider, err := objectField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	entries, err := objectArrayField(provider, "models")
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"defaultModel", "defaultProvider"} {
		if _, err := stringField(settings, key); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"baseUrl", "api", "apiKey"} {
		if _, err := stringField(provider, key); err != nil {
			return nil, err
		}
	}
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		for _, key := range []string{"id", "baseUrl", "api"} {
			if _, err := stringField(item, key); err != nil {
				return nil, err
			}
		}
	}
	existing, _ := stringField(settings, "defaultModel")
	model, err := m.selectedModel(ProductPi, modelsFile.TargetPath, existing)
	if err != nil {
		return nil, err
	}
	var selected map[string]any
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		if item["id"] == model {
			selected = item
			break
		}
	}
	if selected == nil {
		selected = map[string]any{"id": model}
		entries = append(entries, selected)
	}
	// Per-model routing overrides the provider, so both must point at the gateway.
	selected["baseUrl"] = m.advertisedBaseURL(cfg)
	selected["api"] = "openai-completions"
	provider["baseUrl"] = m.advertisedBaseURL(cfg)
	provider["api"] = "openai-completions"
	key, _ := stringField(provider, "apiKey")
	if strings.TrimSpace(key) == "" {
		provider["apiKey"] = "clipal"
	}
	provider["models"] = entries
	providers["clipal"] = provider
	models["providers"] = providers
	settings["defaultProvider"], settings["defaultModel"] = "clipal", model
	modelBytes, err := marshalJSONMap(models)
	if err != nil {
		return nil, err
	}
	settingsBytes, err := marshalJSONMap(settings)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{
		{backupFile: modelsFile, Planned: modelBytes, Format: managedJSON},
		{backupFile: settingsFile, Planned: settingsBytes, Format: managedJSON},
	}, nil
}
