package integration

import (
	"fmt"
	"os"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const (
	openCodeSchemaURL      = "https://opencode.ai/config.json"
	openCodeProviderNPM    = "@ai-sdk/openai-compatible"
	openCodeProviderAPIKey = "clipal"
)

func (m Manager) opencodeTargetPath() (string, error) {
	return m.customClientPath(ProductOpenCode, "opencode.json")
}

func (m Manager) advertisedOpenCodeBaseURL(cfg *config.Config) string {
	return m.advertisedBaseURL(cfg)
}

func (m Manager) previewOpenCode(cfg *config.Config) (Preview, error) {
	targetPath, err := m.opencodeTargetPath()
	if err != nil {
		return Preview{}, err
	}

	current := ""
	doc := map[string]any{}
	if raw, err := os.ReadFile(targetPath); err == nil {
		current = string(raw)
		doc, err = readJSONCMap(targetPath)
		if err != nil {
			return Preview{}, fmt.Errorf("read OpenCode config for preview: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Preview{}, fmt.Errorf("read OpenCode config for preview: %w", err)
	}

	if _, ok := doc["$schema"]; !ok {
		doc["$schema"] = openCodeSchemaURL
	}

	selections := map[string]string{}
	for _, field := range []string{"model", "small_model"} {
		selections[field], _ = doc[field].(string)
	}
	primaryID, primaryName := inferOpenCodeModel(doc, "model", "")
	target, err := m.targetOptions(ProductOpenCode)
	if err != nil {
		return Preview{}, err
	}
	chosen, err := m.selectedModel(ProductOpenCode, targetPath, primaryID)
	if err != nil {
		return Preview{}, err
	}
	if target.Model != "" {
		primaryName = chosen
	}
	primaryID = chosen
	doc["model"] = "clipal/" + primaryID

	smallID, smallName := inferOpenCodeModel(doc, "small_model", "")
	if smallID != "" {
		doc["small_model"] = "clipal/" + smallID
	}

	providers, _ := doc["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	clipalProvider, _ := providers["clipal"].(map[string]any)
	if clipalProvider == nil {
		clipalProvider = map[string]any{}
	}
	if clipalProvider["npm"] == nil {
		clipalProvider["npm"] = openCodeProviderNPM
	}
	if clipalProvider["name"] == nil {
		clipalProvider["name"] = "Clipal"
	}

	options, _ := clipalProvider["options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
	}
	options["baseURL"] = m.advertisedOpenCodeBaseURL(cfg)
	if key, _ := options["apiKey"].(string); strings.TrimSpace(key) == "" {
		options["apiKey"] = openCodeProviderAPIKey
	}
	clipalProvider["options"] = options

	models, _ := clipalProvider["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
	}
	// Preserve declared wire IDs, limits and capabilities when moving an
	// existing model into the Clipal provider.
	for _, field := range []string{"model", "small_model"} {
		value := selections[field]
		provider, id, ok := strings.Cut(value, "/")
		if !ok || provider == "clipal" {
			continue
		}
		source, _ := providers[provider].(map[string]any)
		declared, _ := source["models"].(map[string]any)
		metadata, _ := declared[id].(map[string]any)
		if models[id] == nil && metadata != nil {
			copy := map[string]any{}
			for key, value := range metadata {
				copy[key] = value
			}
			models[id] = copy
		}
	}
	upsertOpenCodeModel(models, primaryID, primaryName)
	if smallID != "" {
		upsertOpenCodeModel(models, smallID, smallName)
	}
	clipalProvider["models"] = models

	providers["clipal"] = clipalProvider
	doc["provider"] = providers

	plannedBytes, err := marshalJSONMap(doc)
	if err != nil {
		return Preview{}, fmt.Errorf("marshal OpenCode preview: %w", err)
	}

	return Preview{
		Product:        ProductOpenCode,
		CurrentContent: current,
		PlannedContent: string(plannedBytes),
	}, nil
}

func inferOpenCodeModel(doc map[string]any, field, fallback string) (string, string) {
	value, _ := doc[field].(string)
	value = strings.TrimSpace(value)
	if value == "" {
		if fallback == "" {
			return "", ""
		}
		return fallback, fallback
	}

	providerKey := ""
	modelID := value
	if left, right, ok := strings.Cut(value, "/"); ok {
		providerKey = strings.TrimSpace(left)
		modelID = strings.TrimSpace(right)
	}
	if modelID == "" {
		modelID = fallback
	}
	modelName := modelID

	providers, _ := doc["provider"].(map[string]any)
	sourceProvider, _ := providers[providerKey].(map[string]any)
	models, _ := sourceProvider["models"].(map[string]any)
	modelConfig, _ := models[modelID].(map[string]any)
	if name, _ := modelConfig["name"].(string); strings.TrimSpace(name) != "" {
		modelName = name
	}

	return modelID, modelName
}

func upsertOpenCodeModel(models map[string]any, modelID, modelName string) {
	if strings.TrimSpace(modelID) == "" {
		return
	}
	model, _ := models[modelID].(map[string]any)
	if model == nil {
		model = map[string]any{}
	}
	if strings.TrimSpace(modelName) == "" {
		modelName = modelID
	}
	if model["name"] == nil {
		model["name"] = modelName
	}
	models[modelID] = model
}
