package integration

import (
	"fmt"
	"os"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func (m Manager) aiderTargetPath() (string, error) {
	return m.customClientPath(ProductAider, ".aider.conf.yml")
}

func (m Manager) previewAider(cfg *config.Config) (Preview, error) {
	targetPath, err := m.aiderTargetPath()
	if err != nil {
		return Preview{}, err
	}

	current := ""
	doc := map[string]any{}
	if raw, err := os.ReadFile(targetPath); err == nil {
		current = string(raw)
		doc, err = readYAMLMap(targetPath)
		if err != nil {
			return Preview{}, fmt.Errorf("read Aider config for preview: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Preview{}, fmt.Errorf("read Aider config for preview: %w", err)
	}

	doc["openai-api-base"] = m.advertisedBaseURL(cfg)
	existing, _ := doc["model"].(string)
	model, err := m.selectedModel(ProductAider, targetPath, existing)
	if err != nil {
		return Preview{}, err
	}
	target, err := m.targetOptions(ProductAider)
	if err != nil {
		return Preview{}, err
	}
	doc["model"], err = managedOpenAIModel(model, target.Model != "")
	if err != nil {
		return Preview{}, err
	}
	if key, _ := doc["openai-api-key"].(string); strings.TrimSpace(key) == "" {
		doc["openai-api-key"] = "clipal"
	}

	plannedBytes, err := marshalYAMLMap(doc)
	if err != nil {
		return Preview{}, fmt.Errorf("marshal Aider preview: %w", err)
	}

	return Preview{
		Product:        ProductAider,
		CurrentContent: current,
		PlannedContent: string(plannedBytes),
	}, nil
}
