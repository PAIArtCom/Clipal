package integration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// CodeBuddy's user model registry uses full Chat Completions URLs by default.
// Sources checked 2026-10-18, docs build 5fcc78457744:
// https://www.codebuddy.ai/docs/cli/models
// https://www.codebuddy.ai/docs/cli/settings
// https://www.codebuddy.ai/docs/cli/env-vars
// Configuration directory: CODEBUDDY_CONFIG_DIR, otherwise ~/.codebuddy.
func (m Manager) planCodeBuddy(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductCodeBuddy)
	if err != nil {
		return nil, err
	}
	settingsFile, err := readBackupFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return nil, err
	}
	settings, err := parseJSONObject(settingsFile)
	if err != nil {
		return nil, fmt.Errorf("read CodeBuddy settings: %w", err)
	}
	env, err := objectField(settings, "env")
	if err != nil {
		return nil, err
	}
	existing := ""
	for _, field := range []struct {
		body map[string]any
		key  string
	}{{settings, "model"}, {env, "CODEBUDDY_MODEL"}} {
		if value, exists := field.body[field.key]; exists {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("CodeBuddy %s must be a string", field.key)
			}
			if strings.TrimSpace(text) != "" {
				existing = text
			}
		}
	}
	model, err := m.selectedModel(ProductCodeBuddy, settingsFile.TargetPath, existing)
	if err != nil {
		return nil, err
	}
	modelsFile, err := readBackupFile(filepath.Join(dir, "models.json"))
	if err != nil {
		return nil, err
	}
	registry, err := parseJSONObject(modelsFile)
	if err != nil {
		return nil, fmt.Errorf("read CodeBuddy models: %w", err)
	}
	models, err := objectArrayField(registry, "models")
	if err != nil {
		return nil, err
	}
	var selected map[string]any
	for _, item := range models {
		entry, _ := item.(map[string]any) // objectArrayField validates entries.
		id, ok := entry["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("CodeBuddy model id must be a nonempty string")
		}
		if id == model {
			if selected != nil {
				return nil, fmt.Errorf("CodeBuddy model %q is duplicated", model)
			}
			selected = entry
		}
	}
	if selected == nil {
		selected = map[string]any{"id": model, "name": model}
		models = append(models, selected)
	}
	selected["url"] = m.advertisedBaseURL(cfg) + "/v1/chat/completions"
	// Omitting api selects the documented Chat Completions protocol. In
	// particular, an existing openai-responses value would interpret url as a base.
	delete(selected, "api")
	if value, exists := selected["apiKey"]; exists {
		key, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("CodeBuddy model apiKey must be a string")
		}
		if strings.TrimSpace(key) == "" {
			selected["apiKey"] = "clipal"
		}
	} else {
		selected["apiKey"] = "clipal"
	}
	registry["models"] = models
	if value, exists := registry["availableModels"]; exists {
		available, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("CodeBuddy availableModels must be an array")
		}
		found := false
		for _, item := range available {
			id, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("CodeBuddy availableModels entries must be strings")
			}
			found = found || id == model
		}
		// An empty list means all models; preserve that meaning.
		if len(available) > 0 && !found {
			registry["availableModels"] = append(available, model)
		}
	}
	settings["model"] = model
	if _, exists := env["CODEBUDDY_MODEL"]; exists {
		env["CODEBUDDY_MODEL"] = model
	}
	settingsBody, err := marshalJSONMap(settings)
	if err != nil {
		return nil, err
	}
	modelsBody, err := marshalJSONMap(registry)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{
		{backupFile: settingsFile, Planned: settingsBody, Format: managedJSON},
		{backupFile: modelsFile, Planned: modelsBody, Format: managedJSON},
	}, nil
}
