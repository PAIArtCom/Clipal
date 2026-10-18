package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const (
	claudeHomeConfigBackupMetaName = "claude-home-config-metadata.json"
	claudeHomeConfigBackupBodyName = "claude-home-config-original.bin"
	claudeClipalAPIKey             = "clipal"
)

type claudeHomeConfigBackup struct {
	TargetPath string `json:"target_path"`
	Existed    bool   `json:"existed"`
}

func (m Manager) claudeTargetPath() (string, error) {
	dir, err := m.clientConfigDir(ProductClaudeCode)
	return filepath.Join(dir, "settings.json"), err
}
func (m Manager) claudeHomeConfigPath() (string, error) {
	target, err := m.targetOptions(ProductClaudeCode)
	if err != nil {
		return "", err
	}
	if target.ConfigDir != "" || strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")) != "" {
		dir, err := m.clientConfigDir(ProductClaudeCode)
		return filepath.Join(dir, ".claude.json"), err
	}
	home, err := m.resolveHomeDir()
	return filepath.Join(home, ".claude.json"), err
}

func (m Manager) planClaude(cfg *config.Config) ([]takeoverFile, error) {
	settings, err := m.claudeTargetPath()
	if err != nil {
		return nil, err
	}
	home, err := m.claudeHomeConfigPath()
	if err != nil {
		return nil, err
	}
	var files []takeoverFile
	for _, path := range []string{settings, home} {
		file, err := readBackupFile(path)
		if err != nil {
			return nil, err
		}
		body := map[string]any{}
		if file.Existed {
			body, err = readJSONMap(path)
			if err != nil {
				return nil, fmt.Errorf("read Claude config %s: %w", path, err)
			}
		}
		if path == settings {
			env, _ := body["env"].(map[string]any)
			if env == nil {
				env = map[string]any{}
			}
			env["ANTHROPIC_BASE_URL"] = m.advertisedBaseURL(cfg)
			if !claudeEnvHasClientAuth(env) {
				env["ANTHROPIC_API_KEY"] = claudeClipalAPIKey
			}
			body["env"] = env
		} else {
			body["hasCompletedOnboarding"] = true
		}
		planned, err := marshalJSONMap(body)
		if err != nil {
			return nil, err
		}
		files = append(files, takeoverFile{backupFile: file, Planned: planned})
	}
	return files, nil
}
func claudeEnvHasClientAuth(env map[string]any) bool {
	return claudeEnvValue(env, "ANTHROPIC_API_KEY") != "" || claudeEnvValue(env, "ANTHROPIC_AUTH_TOKEN") != ""
}
func claudeEnvValue(env map[string]any, key string) string {
	value, _ := env[key].(string)
	return strings.TrimSpace(value)
}
