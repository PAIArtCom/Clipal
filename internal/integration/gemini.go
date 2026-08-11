package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const geminiAPIBaseKey = "GEMINI_API_BASE"

func (m Manager) geminiTargetPath() (string, error) {
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gemini", ".env"), nil
}

func (m Manager) geminiStatus(cfg *config.Config) (Status, error) {
	targetPath, err := m.geminiTargetPath()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Product:         ProductGeminiCLI,
		State:           StateNotConfigured,
		TargetPath:      targetPath,
		BackupAvailable: m.hasLatestBackup(ProductGeminiCLI),
		Warning:         "Project-local .env files or exported environment variables may still override the user-level Gemini CLI .env.",
	}

	raw, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return Status{}, fmt.Errorf("read Gemini CLI .env: %w", err)
	}

	if geminiEnvValue(string(raw), geminiAPIBaseKey) == m.advertisedBaseURL(cfg) {
		status.State = StateConfigured
	}
	return status, nil
}

func (m Manager) previewGemini(cfg *config.Config) (Preview, error) {
	targetPath, err := m.geminiTargetPath()
	if err != nil {
		return Preview{}, err
	}

	current := ""
	if raw, err := os.ReadFile(targetPath); err == nil {
		current = string(raw)
	} else if !os.IsNotExist(err) {
		return Preview{}, fmt.Errorf("read Gemini CLI .env for preview: %w", err)
	}

	planned := upsertEnvLine(current, geminiAPIBaseKey, m.advertisedBaseURL(cfg))
	return Preview{
		Product:        ProductGeminiCLI,
		CurrentContent: current,
		PlannedContent: planned,
	}, nil
}

func (m Manager) applyGemini(cfg *config.Config) (Result, error) {
	status, err := m.geminiStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{
			Product: ProductGeminiCLI,
			Status:  status,
			Message: "already configured",
		}, nil
	}

	if _, err := m.createBackup(ProductGeminiCLI, status.TargetPath); err != nil {
		return Result{}, err
	}

	preview, err := m.previewGemini(cfg)
	if err != nil {
		return Result{}, err
	}

	if err := os.MkdirAll(filepath.Dir(status.TargetPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("prepare Gemini CLI .env path: %w", err)
	}
	if err := os.WriteFile(status.TargetPath, []byte(preview.PlannedContent), 0o600); err != nil {
		return Result{}, fmt.Errorf("write Gemini CLI .env: %w", err)
	}

	status, err = m.geminiStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: ProductGeminiCLI,
		Status:  status,
		Message: "Gemini CLI configured to use Clipal",
	}, nil
}

func geminiEnvValue(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		name, value, ok := parseEnvLine(line)
		if ok && name == key {
			return value
		}
	}
	return ""
}

func upsertEnvLine(content, key, value string) string {
	lines := strings.Split(content, "\n")
	output := make([]string, 0, len(lines)+1)
	replaced := false

	for _, line := range lines {
		name, _, ok := parseEnvLine(line)
		if ok && name == key {
			if !replaced {
				output = append(output, formatEnvLine(key, value))
				replaced = true
			}
			continue
		}
		output = append(output, line)
	}

	if !replaced {
		if len(output) > 0 && strings.TrimSpace(output[len(output)-1]) != "" {
			output = append(output, formatEnvLine(key, value))
		} else {
			insertAt := len(output)
			for insertAt > 0 && strings.TrimSpace(output[insertAt-1]) == "" {
				insertAt--
			}
			output = append(output[:insertAt], append([]string{formatEnvLine(key, value)}, output[insertAt:]...)...)
		}
	}

	result := strings.Join(output, "\n")
	if strings.TrimSpace(result) == "" {
		return formatEnvLine(key, value) + "\n"
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

func parseEnvLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if strings.HasPrefix(trimmed, "export ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
	}

	name, rawValue, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", "", false
	}
	name = strings.TrimSpace(name)
	rawValue = strings.TrimSpace(rawValue)
	if name == "" {
		return "", "", false
	}

	if len(rawValue) >= 2 {
		if strings.HasPrefix(rawValue, `"`) && strings.HasSuffix(rawValue, `"`) {
			rawValue = strings.Trim(rawValue, `"`)
		} else if strings.HasPrefix(rawValue, `'`) && strings.HasSuffix(rawValue, `'`) {
			rawValue = strings.Trim(rawValue, `'`)
		}
	}
	return name, rawValue, true
}

func formatEnvLine(key, value string) string {
	return key + "=" + value
}
