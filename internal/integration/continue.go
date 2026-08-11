package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const continueFallbackModel = "gpt-5.4"

func (m Manager) continueTargetPath() (string, error) {
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".continue", "config.yaml"), nil
}

func (m Manager) continueStatus(cfg *config.Config) (Status, error) {
	targetPath, err := m.continueTargetPath()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Product:         ProductContinue,
		State:           StateNotConfigured,
		TargetPath:      targetPath,
		BackupAvailable: m.hasLatestBackup(ProductContinue),
		Warning:         "Continue workspace config or the currently selected model in the app may still override this user-level config.",
	}

	doc, err := readYAMLMap(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return Status{}, fmt.Errorf("read Continue config: %w", err)
	}

	if model := findContinueClipalModel(doc); model != nil {
		baseURL, _ := model["apiBase"].(string)
		provider, _ := model["provider"].(string)
		apiKey, _ := model["apiKey"].(string)
		if provider == "openai" && baseURL == m.advertisedBaseURL(cfg) && apiKey == "clipal" {
			status.State = StateConfigured
		}
	}
	return status, nil
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

	upsertContinueClipalModel(doc, m.advertisedBaseURL(cfg))

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

func (m Manager) applyContinue(cfg *config.Config) (Result, error) {
	status, err := m.continueStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{Product: ProductContinue, Status: status, Message: "already configured"}, nil
	}

	if _, err := m.createBackup(ProductContinue, status.TargetPath); err != nil {
		return Result{}, err
	}
	preview, err := m.previewContinue(cfg)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(status.TargetPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare Continue config path: %w", err)
	}
	if err := os.WriteFile(status.TargetPath, []byte(preview.PlannedContent), 0o600); err != nil {
		return Result{}, fmt.Errorf("write Continue config: %w", err)
	}

	status, err = m.continueStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: ProductContinue,
		Status:  status,
		Message: "Continue configured to use Clipal",
	}, nil
}

func upsertContinueClipalModel(doc map[string]any, baseURL string) {
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
	primaryModel := inferContinuePrimaryModel(models)
	roles := inferContinueClipalRoles(models)

	clipal := map[string]any{
		"name":     "Clipal",
		"provider": "openai",
		"model":    primaryModel,
		"apiBase":  baseURL,
		"apiKey":   "clipal",
		"roles":    roles,
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

func inferContinuePrimaryModel(models []any) string {
	for _, item := range models {
		model, _ := item.(map[string]any)
		if model == nil || isContinueClipalModel(model) {
			continue
		}
		modelID, _ := model["model"].(string)
		modelID = strings.TrimSpace(modelID)
		if modelID != "" {
			return modelID
		}
	}
	return continueFallbackModel
}

func inferContinueClipalRoles(models []any) []any {
	seen := map[string]bool{}
	add := func(role string) {
		role = strings.TrimSpace(role)
		if role != "" {
			seen[role] = true
		}
	}

	add("chat")
	add("edit")
	add("apply")
	for _, item := range models {
		model, _ := item.(map[string]any)
		if model == nil || isContinueClipalModel(model) {
			continue
		}
		roles, _ := model["roles"].([]any)
		for _, role := range roles {
			text, _ := role.(string)
			if text == "autocomplete" {
				add(text)
			}
		}
	}

	ordered := []string{"chat", "edit", "apply", "autocomplete"}
	out := make([]any, 0, len(ordered))
	for _, role := range ordered {
		if seen[role] {
			out = append(out, role)
		}
	}
	return out
}
