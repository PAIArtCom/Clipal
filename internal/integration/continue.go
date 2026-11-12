package integration

import (
	"fmt"
	"os"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func (m Manager) continueTargetPath() (string, error) {
	return m.customClientPath(ProductContinue, "config.yaml")
}

func (m Manager) previewContinue(cfg *config.Config) (Preview, error) {
	targetPath, err := m.continueTargetPath()
	if err != nil {
		return Preview{}, err
	}

	current := ""
	doc := map[string]any{}
	if raw, err := os.ReadFile(targetPath); err == nil {
		current = string(raw)
		doc, err = readYAMLMap(targetPath)
		if err != nil {
			return Preview{}, fmt.Errorf("read Continue config for preview: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Preview{}, fmt.Errorf("read Continue config for preview: %w", err)
	}

	model, err := m.selectedModel(ProductContinue, targetPath, inferContinuePrimaryModelFromDoc(doc))
	if err != nil {
		return Preview{}, err
	}
	upsertContinueClipalModel(doc, m.advertisedBaseURL(cfg), model)

	plannedBytes, err := marshalYAMLMap(doc)
	if err != nil {
		return Preview{}, fmt.Errorf("marshal Continue preview: %w", err)
	}

	return Preview{
		Product:        ProductContinue,
		CurrentContent: current,
		PlannedContent: string(plannedBytes),
	}, nil
}

func upsertContinueClipalModel(doc map[string]any, baseURL, modelID string) {
	if _, ok := doc["name"]; !ok {
		doc["name"] = "Clipal"
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = "1.0.0"
	}
	if _, ok := doc["schema"]; !ok {
		doc["schema"] = "v1"
	}

	models, _ := doc["models"].([]any)
	clipal := findContinueClipalModel(doc)
	if clipal == nil {
		clipal = map[string]any{}
	}
	clipal["name"] = "Clipal"
	clipal["provider"] = "openai"
	clipal["model"] = modelID
	clipal["apiBase"] = baseURL
	if key, _ := clipal["apiKey"].(string); strings.TrimSpace(key) == "" {
		clipal["apiKey"] = "clipal"
	}
	if clipal["roles"] == nil {
		clipal["roles"] = []any{"chat", "edit", "apply"}
	}

	replaced := false
	out := make([]any, 0, len(models)+1)
	for _, item := range models {
		model, _ := item.(map[string]any)
		if isContinueClipalModel(model) {
			if !replaced {
				out = append(out, clipal)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, clipal)
	}
	doc["models"] = out
}

func findContinueClipalModel(doc map[string]any) map[string]any {
	models, _ := doc["models"].([]any)
	for _, item := range models {
		model, _ := item.(map[string]any)
		if isContinueClipalModel(model) {
			return model
		}
	}
	return nil
}

func isContinueClipalModel(model map[string]any) bool {
	if model == nil {
		return false
	}
	name, _ := model["name"].(string)
	return strings.TrimSpace(name) == "Clipal"
}

func inferContinuePrimaryModelFromDoc(doc map[string]any) string {
	if existing := findContinueClipalModel(doc); existing != nil {
		if value, _ := existing["model"].(string); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
