package integration

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
	"gopkg.in/yaml.v3"

	"github.com/PAIArtCom/Clipal/internal/config"
)

type Manager struct {
	configDir string
	homeDir   string
}

func NewManager(configDir string) *Manager {
	return &Manager{configDir: configDir}
}

func (m Manager) advertisedBaseURL(cfg *config.Config) string {
	host := strings.TrimSpace(cfg.Global.ListenAddr)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	default:
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
		}
	}
	return fmt.Sprintf("http://%s:%d/clipal", host, cfg.Global.Port)
}

func (m Manager) backupProductRoot(product ProductID) string {
	return filepath.Join(m.configDir, "backups", "integrations", string(product))
}

func (m Manager) Status(product ProductID, cfg *config.Config) (Status, error) {
	switch product {
	case ProductClaudeCode:
		return m.claudeStatus(cfg)
	case ProductCodexCLI:
		return m.codexStatus(cfg)
	case ProductOpenCode:
		return m.opencodeStatus(cfg)
	case ProductGeminiCLI:
		return m.geminiStatus(cfg)
	case ProductContinue:
		return m.continueStatus(cfg)
	case ProductAider:
		return m.aiderStatus(cfg)
	case ProductGoose:
		return m.gooseStatus(cfg)
	default:
		return Status{}, fmt.Errorf("unknown product: %s", product)
	}
}

func (m Manager) Apply(product ProductID, cfg *config.Config) (Result, error) {
	switch product {
	case ProductClaudeCode:
		return m.applyClaude(cfg)
	case ProductCodexCLI:
		return m.applyCodex(cfg)
	case ProductOpenCode:
		return m.applyOpenCode(cfg)
	case ProductGeminiCLI:
		return m.applyGemini(cfg)
	case ProductContinue:
		return m.applyContinue(cfg)
	case ProductAider:
		return m.applyAider(cfg)
	case ProductGoose:
		return m.applyGoose(cfg)
	default:
		return Result{}, fmt.Errorf("unknown product: %s", product)
	}
}

func (m Manager) Preview(product ProductID, cfg *config.Config) (Preview, error) {
	backupContent, backupTargetExisted, err := m.latestBackupPreview(product)
	if err != nil {
		return Preview{}, err
	}

	var preview Preview
	switch product {
	case ProductClaudeCode:
		preview, err = m.previewClaude(cfg)
	case ProductCodexCLI:
		preview, err = m.previewCodex(cfg)
	case ProductOpenCode:
		preview, err = m.previewOpenCode(cfg)
	case ProductGeminiCLI:
		preview, err = m.previewGemini(cfg)
	case ProductContinue:
		preview, err = m.previewContinue(cfg)
	case ProductAider:
		preview, err = m.previewAider(cfg)
	case ProductGoose:
		preview, err = m.previewGoose(cfg)
	default:
		return Preview{}, fmt.Errorf("unknown product: %s", product)
	}
	if err != nil {
		return Preview{}, err
	}
	preview.BackupContent = backupContent
	preview.BackupTargetExisted = backupTargetExisted
	return preview, nil
}

func (m Manager) Rollback(product ProductID, cfg *config.Config) (Result, error) {
	if product == ProductClaudeCode {
		return m.rollbackClaude(cfg)
	}

	snap, err := m.loadLatestBackup(product)
	if err != nil {
		return Result{}, fmt.Errorf("load latest backup: %w", err)
	}
	if snap.TargetExisted {
		if err := os.MkdirAll(filepath.Dir(snap.TargetPath), 0o755); err != nil {
			return Result{}, fmt.Errorf("prepare rollback target: %w", err)
		}
		if err := os.WriteFile(snap.TargetPath, snap.Original, 0o600); err != nil {
			return Result{}, fmt.Errorf("restore backup: %w", err)
		}
	} else if err := os.Remove(snap.TargetPath); err != nil && !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("remove target during rollback: %w", err)
	}

	status, err := m.Status(product, cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Product: product,
		Status:  status,
		Message: "rollback completed",
	}, nil
}

func (m Manager) latestBackupPreview(product ProductID) (string, bool, error) {
	snap, err := m.loadLatestBackup(product)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load latest backup preview: %w", err)
	}
	return string(snap.Original), snap.TargetExisted, nil
}

func (m Manager) resolveHomeDir() (string, error) {
	if strings.TrimSpace(m.homeDir) != "" {
		return m.homeDir, nil
	}
	return os.UserHomeDir()
}

func readJSONMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func marshalJSONMap(body map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readJSONCMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	standardized, err := hujson.Standardize(data)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(standardized, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func readYAMLMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func marshalYAMLMap(body map[string]any) ([]byte, error) {
	data, err := yaml.Marshal(body)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return data, nil
}
