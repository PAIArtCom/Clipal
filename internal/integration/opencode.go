package integration

import (
	"fmt"
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

func (m Manager) planOpenCode(cfg *config.Config) ([]takeoverFile, error) {
	return m.planOpenCodeCompatible(ProductOpenCode, cfg)
}

func (m Manager) planOpenCodeCompatible(product ProductID, cfg *config.Config) ([]takeoverFile, error) {
	targetPath, err := m.openCodeCompatiblePath(product)
	if err != nil {
		return nil, err
	}

	state, err := m.readOpenCodeCompatibleConfig(product, targetPath)
	if err != nil {
		return nil, err
	}
	effective := state.effective(nil)
	if reason := openCodeRestriction(effective); reason != "" {
		return nil, &openCodeConflictError{path: targetPath, reason: reason}
	}
	doc := map[string]any{}
	if state.target.Existed {
		doc, err = parseJSONCMap(state.target.Original)
		if err != nil {
			return nil, err
		}
	}

	if _, ok := doc["$schema"]; !ok {
		schema := openCodeSchemaURL
		if product == ProductKiloCLI {
			schema = "https://app.kilo.ai/config.json"
		}
		doc["$schema"] = schema
	}

	selections := map[string]string{}
	for _, field := range []string{"model", "small_model"} {
		selections[field], _ = effective[field].(string)
	}
	primaryID, primaryName := inferOpenCodeModel(effective, "model", "")
	target, err := m.targetOptions(product)
	if err != nil {
		return nil, err
	}
	chosen, err := m.selectedModel(product, targetPath, primaryID)
	if err != nil {
		return nil, err
	}
	if target.Model != "" {
		primaryName = chosen
	}
	primaryID = chosen
	doc["model"] = "clipal/" + primaryID

	smallID, smallName := inferOpenCodeModel(effective, "small_model", "")
	if smallID != "" {
		doc["small_model"] = "clipal/" + smallID
	}

	providers, err := objectField(doc, "provider")
	if err != nil {
		return nil, err
	}
	clipalProvider, err := objectField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	if clipalProvider["npm"] == nil {
		clipalProvider["npm"] = openCodeProviderNPM
		if inherited := openCodeProvider(effective); inherited["npm"] != nil {
			clipalProvider["npm"] = inherited["npm"]
		}
	}
	if clipalProvider["name"] == nil {
		clipalProvider["name"] = "Clipal"
		if inherited := openCodeProvider(effective); inherited["name"] != nil {
			clipalProvider["name"] = inherited["name"]
		}
	}

	options, err := objectField(clipalProvider, "options")
	if err != nil {
		return nil, err
	}
	options["baseURL"] = m.advertisedOpenCodeBaseURL(cfg)
	inheritedOptions, _ := openCodeProvider(effective)["options"].(map[string]any)
	effectiveKey, _ := inheritedOptions["apiKey"].(string)
	if key, _ := options["apiKey"].(string); strings.TrimSpace(key) == "" && strings.TrimSpace(effectiveKey) == "" {
		options["apiKey"] = openCodeProviderAPIKey
	}
	clipalProvider["options"] = options

	models, err := objectField(clipalProvider, "models")
	if err != nil {
		return nil, err
	}
	// Seed only the selected models from the merged view; leave all other
	// inherited provider metadata in its original files.
	for _, field := range []string{"model", "small_model"} {
		id := primaryID
		if field == "small_model" {
			id = smallID
		}
		if id == "" {
			continue
		}
		inheritedModels, _ := openCodeProvider(effective)["models"].(map[string]any)
		metadata, _ := inheritedModels[id].(map[string]any)
		if metadata == nil {
			provider, sourceID, ok := strings.Cut(selections[field], "/")
			if ok && sourceID == id {
				sources, _ := effective["provider"].(map[string]any)
				source, _ := sources[provider].(map[string]any)
				declared, _ := source["models"].(map[string]any)
				metadata, _ = declared[id].(map[string]any)
			}
		}
		merged := map[string]any{}
		mergeOpenCodeRouting(merged, metadata)
		local, _ := models[id].(map[string]any)
		mergeOpenCodeRouting(merged, local)
		models[id] = merged
	}
	upsertOpenCodeModel(models, primaryID, primaryName)
	if smallID != "" {
		upsertOpenCodeModel(models, smallID, smallName)
	}
	if target.Model != "" {
		model, _ := models[primaryID].(map[string]any)
		model["id"] = primaryID
	}
	clipalProvider["models"] = models

	providers["clipal"] = clipalProvider
	doc["provider"] = providers

	plannedBytes, err := marshalJSONMap(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal compatible client preview: %w", err)
	}

	if openCodeRouteShadow(doc, state.effective(doc)) {
		return nil, &openCodeConflictError{path: targetPath, reason: "A later known configuration layer overrides the selected file's model or provider routing; select the higher-priority file or resolve that override."}
	}
	return []takeoverFile{{backupFile: state.target, Planned: plannedBytes, Dependencies: state.dependencies}}, nil
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
