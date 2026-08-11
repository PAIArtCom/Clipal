package integration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const gooseFallbackModel = "gpt-5.4"

func (m Manager) gooseTargetPath() (string, error) {
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "goose", "custom_providers", "clipal.json"), nil
}

func (m Manager) advertisedGooseBaseURL(cfg *config.Config) string {
	return m.advertisedBaseURL(cfg) + "/v1/chat/completions"
}

func (m Manager) gooseStatus(cfg *config.Config) (Status, error) {
	targetPath, err := m.gooseTargetPath()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Product:         ProductGoose,
		State:           StateNotConfigured,
		TargetPath:      targetPath,
		BackupAvailable: m.hasLatestBackup(ProductGoose),
		Warning:         "Goose may still require you to select the Clipal provider or model inside Goose, and environment overrides can still change behavior.",
	}

	doc, err := readJSONMap(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return Status{}, fmt.Errorf("read Goose provider config: %w", err)
	}

	baseURL, _ := doc["base_url"].(string)
	engine, _ := doc["engine"].(string)
	if baseURL == m.advertisedGooseBaseURL(cfg) && engine == "openai" {
		status.State = StateConfigured
	}
	return status, nil
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

	body := map[string]any{
		"name":         "clipal",
		"engine":       "openai",
		"display_name": "Clipal",
		"description":  "Clipal managed OpenAI-compatible provider",
		"base_url":     m.advertisedGooseBaseURL(cfg),
		"models": []any{
			map[string]any{
				"name":          gooseFallbackModel,
				"context_limit": 128000,
			},
		},
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

func (m Manager) applyGoose(cfg *config.Config) (Result, error) {
	status, err := m.gooseStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{Product: ProductGoose, Status: status, Message: "already configured"}, nil
	}

	if _, err := m.createBackup(ProductGoose, status.TargetPath); err != nil {
		return Result{}, err
	}
	preview, err := m.previewGoose(cfg)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(status.TargetPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare Goose provider config path: %w", err)
	}
	if err := os.WriteFile(status.TargetPath, []byte(preview.PlannedContent), 0o600); err != nil {
		return Result{}, fmt.Errorf("write Goose provider config: %w", err)
	}

	status, err = m.gooseStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: ProductGoose,
		Status:  status,
		Message: "Goose custom provider configured for Clipal",
	}, nil
}
