package integration

import (
	"fmt"
	"os"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func (m Manager) gooseTargetPath() (string, error) {
	return m.customClientPath(ProductGoose, "custom_providers/clipal.json")
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
