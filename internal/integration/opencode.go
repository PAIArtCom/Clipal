package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const (
	openCodeSchemaURL      = "https://opencode.ai/config.json"
	openCodeProviderNPM    = "@ai-sdk/openai-compatible"
	openCodeProviderAPIKey = "clipal"
	openCodeFallbackModel  = "gpt-5.4"
)

func (m Manager) opencodeTargetPath() (string, error) {
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "opencode", "opencode.json"), nil
}

func (m Manager) advertisedOpenCodeBaseURL(cfg *config.Config) string {
	return m.advertisedBaseURL(cfg)
}

func (m Manager) opencodeStatus(cfg *config.Config) (Status, error) {
	targetPath, err := m.opencodeTargetPath()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Product:         ProductOpenCode,
		State:           StateNotConfigured,
		TargetPath:      targetPath,
		BackupAvailable: m.hasLatestBackup(ProductOpenCode),
		Warning:         "Project-local opencode.json and environment variables can still override user config.",
	}

	doc, err := readJSONCMap(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return Status{}, fmt.Errorf("read OpenCode config: %w", err)
	}

	model, _ := doc["model"].(string)
	providers, _ := doc["provider"].(map[string]any)
	clipalProvider, _ := providers["clipal"].(map[string]any)
	options, _ := clipalProvider["options"].(map[string]any)
	baseURL, _ := options["baseURL"].(string)

	if strings.HasPrefix(strings.TrimSpace(model), "clipal/") && baseURL == m.advertisedOpenCodeBaseURL(cfg) {
		status.State = StateConfigured
	}
	return status, nil
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

	primaryID, primaryName := inferOpenCodeModel(doc, "model", openCodeFallbackModel)
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
	clipalProvider["npm"] = openCodeProviderNPM
	clipalProvider["name"] = "Clipal"

	options, _ := clipalProvider["options"].(map[string]any)
	if options == nil {
		options = map[string]any{}
	}
	options["baseURL"] = m.advertisedOpenCodeBaseURL(cfg)
	options["apiKey"] = openCodeProviderAPIKey
	clipalProvider["options"] = options

	models, _ := clipalProvider["models"].(map[string]any)
	if models == nil {
		models = map[string]any{}
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

func (m Manager) applyOpenCode(cfg *config.Config) (Result, error) {
	status, err := m.opencodeStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{
			Product: ProductOpenCode,
			Status:  status,
			Message: "already configured",
		}, nil
	}

	if _, err := m.createBackup(ProductOpenCode, status.TargetPath); err != nil {
		return Result{}, err
	}

	preview, err := m.previewOpenCode(cfg)
	if err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(filepath.Dir(status.TargetPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare OpenCode config path: %w", err)
	}
	if err := os.WriteFile(status.TargetPath, []byte(preview.PlannedContent), 0o600); err != nil {
		return Result{}, fmt.Errorf("write OpenCode config: %w", err)
	}

	status, err = m.opencodeStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: ProductOpenCode,
		Status:  status,
		Message: "OpenCode configured to use Clipal",
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
	model["name"] = modelName
	models[modelID] = model
}
