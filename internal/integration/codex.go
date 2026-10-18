package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func (m Manager) codexTargetPath() (string, error) {
	directory, err := m.clientConfigDir(ProductCodexCLI)
	if err != nil {
		return "", err
	}
	target, err := m.targetOptions(ProductCodexCLI)
	if err != nil {
		return "", err
	}
	filename := "config.toml"
	if target.Profile != "" {
		filename = target.Profile + ".config.toml"
	}
	return filepath.Join(directory, filename), nil
}

func (m Manager) planCodex(cfg *config.Config) ([]takeoverFile, error) {
	path, err := m.codexTargetPath()
	if err != nil {
		return nil, err
	}
	return planSingleFile(path, func() (Preview, error) { return m.previewCodex(cfg) })
}

func (m Manager) previewCodex(cfg *config.Config) (Preview, error) {
	targetPath, err := m.codexTargetPath()
	if err != nil {
		return Preview{}, err
	}

	current := ""
	doc := map[string]any{}
	if raw, err := os.ReadFile(targetPath); err == nil {
		current = string(raw)
		doc, err = readTOMLMap(targetPath)
		if err != nil {
			return Preview{}, fmt.Errorf("read Codex config for preview: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return Preview{}, fmt.Errorf("read Codex config for preview: %w", err)
	}

	doc["model_provider"] = "clipal"

	modelProviders, _ := doc["model_providers"].(map[string]any)
	if modelProviders == nil {
		modelProviders = map[string]any{}
	}
	modelProviders["clipal"] = map[string]any{
		"name":     "clipal",
		"base_url": m.advertisedBaseURL(cfg),
		"wire_api": "responses",
	}
	doc["model_providers"] = modelProviders

	plannedBytes, err := marshalTOMLMap(doc)
	if err != nil {
		return Preview{}, fmt.Errorf("marshal Codex preview: %w", err)
	}

	return Preview{
		Product:        ProductCodexCLI,
		CurrentContent: current,
		PlannedContent: string(plannedBytes),
	}, nil
}

func readTOMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := toml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func marshalTOMLMap(body map[string]any) ([]byte, error) {
	data, err := toml.Marshal(body)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "[model_providers]" {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n")), nil
}
