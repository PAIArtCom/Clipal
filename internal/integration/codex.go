package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/lansespirit/Clipal/internal/config"
)

func (m Manager) codexTargetPath() (string, error) {
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func (m Manager) codexStatus(cfg *config.Config) (Status, error) {
	targetPath, err := m.codexTargetPath()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Product:         ProductCodexCLI,
		State:           StateNotConfigured,
		TargetPath:      targetPath,
		BackupAvailable: m.hasLatestBackup(ProductCodexCLI),
		Warning:         "Codex ignores model provider settings in project-local .codex/config.toml; keep Clipal provider settings in user-level ~/.codex/config.toml.",
	}

	doc, err := readTOMLMap(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return Status{}, fmt.Errorf("read Codex config: %w", err)
	}

	modelProvider, _ := doc["model_provider"].(string)
	modelProviders, _ := doc["model_providers"].(map[string]any)
	clipalProvider, _ := modelProviders["clipal"].(map[string]any)
	baseURL, _ := clipalProvider["base_url"].(string)
	wireAPI, _ := clipalProvider["wire_api"].(string)

	if modelProvider == "clipal" && baseURL == m.advertisedBaseURL(cfg) && wireAPI == "responses" {
		status.State = StateConfigured
	}
	return status, nil
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

func (m Manager) applyCodex(cfg *config.Config) (Result, error) {
	status, err := m.codexStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{
			Product: ProductCodexCLI,
			Status:  status,
			Message: "already configured",
		}, nil
	}

	if _, err := m.createBackup(ProductCodexCLI, status.TargetPath); err != nil {
		return Result{}, err
	}

	preview, err := m.previewCodex(cfg)
	if err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(filepath.Dir(status.TargetPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare Codex config path: %w", err)
	}
	if err := os.WriteFile(status.TargetPath, []byte(preview.PlannedContent), 0o600); err != nil {
		return Result{}, fmt.Errorf("write Codex config: %w", err)
	}

	status, err = m.codexStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: ProductCodexCLI,
		Status:  status,
		Message: "Codex configured to use Clipal",
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
