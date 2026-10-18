package integration

import (
	"bytes"
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
	configDir           string
	homeDir             string
	beforeTakeoverWrite func(int, string) error
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
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	return m.status(product, cfg)
}

func (m Manager) status(product ProductID, cfg *config.Config) (Status, error) {
	return m.takeoverStatus(product, cfg)
}

func (m Manager) Apply(product ProductID, cfg *config.Config) (Result, error) {
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	return m.applyTakeover(product, cfg)
}

func (m Manager) Preview(product ProductID, cfg *config.Config) (Preview, error) {
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	backupContent, backupTargetExisted, err := m.latestBackupPreview(product)
	if err != nil {
		return Preview{}, err
	}

	files, err := m.planTakeover(product, cfg)
	if err != nil {
		return Preview{}, err
	}
	preview := previewTakeoverFiles(product, files)
	preview.BackupContent = backupContent
	preview.BackupTargetExisted = backupTargetExisted
	return preview, nil
}

func (m Manager) Rollback(product ProductID, cfg *config.Config) (Result, error) {
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	return m.rollbackTakeover(product, cfg)
}

func (m Manager) latestBackupPreview(product ProductID) (string, bool, error) {
	snap, err := m.loadTakeoverBackup(product)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("load latest backup preview: %w", err)
	}
	if len(snap.AdditionalFiles) > 0 {
		files := append([]backupFile{{TargetPath: snap.TargetPath, Existed: snap.TargetExisted, Original: snap.Original}}, snap.AdditionalFiles...)
		return formatTakeoverFiles(files), true, nil
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
	return parseJSONCMap(data)
}

func parseJSONCMap(data []byte) (map[string]any, error) {
	standardized, err := hujson.Standardize(bytes.Clone(data))
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

// BackupAvailable remains true even when a malformed target cannot be inspected.
func (m Manager) BackupAvailable(product ProductID) bool {
	takeoverMu.Lock()
	defer takeoverMu.Unlock()
	return m.hasLatestBackup(product)
}
