package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const aiderFallbackModel = "openai/gpt-5.4"

func (m Manager) aiderTargetPath() (string, error) {
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aider.conf.yml"), nil
}

func (m Manager) aiderStatus(cfg *config.Config) (Status, error) {
	targetPath, err := m.aiderTargetPath()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Product:         ProductAider,
		State:           StateNotConfigured,
		TargetPath:      targetPath,
		BackupAvailable: m.hasLatestBackup(ProductAider),
		Warning:         "Repo-local .aider.conf.yml, current-directory config, .env, or CLI flags can still override the home-level Aider config.",
	}

	doc, err := readYAMLMap(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return Status{}, fmt.Errorf("read Aider config: %w", err)
	}

	baseURL, _ := doc["openai-api-base"].(string)
	model, _ := doc["model"].(string)
	if baseURL == m.advertisedBaseURL(cfg) && strings.HasPrefix(strings.TrimSpace(model), "openai/") {
		status.State = StateConfigured
	}
	return status, nil
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
	doc["model"] = inferAiderModel(doc)
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

func (m Manager) applyAider(cfg *config.Config) (Result, error) {
	status, err := m.aiderStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{Product: ProductAider, Status: status, Message: "already configured"}, nil
	}

	if _, err := m.createBackup(ProductAider, status.TargetPath); err != nil {
		return Result{}, err
	}
	preview, err := m.previewAider(cfg)
	if err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(status.TargetPath, []byte(preview.PlannedContent), 0o600); err != nil {
		return Result{}, fmt.Errorf("write Aider config: %w", err)
	}

	status, err = m.aiderStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: ProductAider,
		Status:  status,
		Message: "Aider configured to use Clipal",
	}, nil
}

func inferAiderModel(doc map[string]any) string {
	model, _ := doc["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return aiderFallbackModel
	}
	if strings.HasPrefix(model, "openai/") {
		return model
	}
	if _, modelID, ok := strings.Cut(model, "/"); ok && strings.TrimSpace(modelID) != "" {
		return "openai/" + strings.TrimSpace(modelID)
	}
	return "openai/" + model
}
