package web

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func (a *API) loadConfigOrWriteError(w http.ResponseWriter) *config.Config {
	cfg, err := config.Load(a.configDir)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, fmt.Sprintf("failed to load config: %v", err), err))
		return nil
	}
	return cfg
}

func (a *API) saveGlobalConfigOrWriteError(w http.ResponseWriter, cfg *config.Config) bool {
	if err := cfg.Validate(); err != nil {
		writeAPIError(w, newAPIError(http.StatusBadRequest, fmt.Sprintf("invalid configuration: %v", err), err))
		return false
	}
	restore, err := a.saveGlobalConfigWithRollback(cfg.Global)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, fmt.Sprintf("failed to save config: %v", err), err))
		return false
	}
	if err := a.reloadRuntimeProviderConfigs(); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			err = fmt.Errorf("failed to apply saved config: %w (rollback failed: %v)", err, restoreErr)
		} else {
			err = fmt.Errorf("failed to apply saved config: %w", err)
		}
		writeAPIError(w, newAPIError(http.StatusInternalServerError, err.Error(), err))
		return false
	}
	return true
}

func (a *API) saveClientConfigOrWriteError(w http.ResponseWriter, clientType string, cfg *config.Config) bool {
	if err := cfg.Validate(); err != nil {
		writeAPIError(w, newAPIError(http.StatusBadRequest, fmt.Sprintf("invalid configuration: %v", err), err))
		return false
	}
	cc, err := getClientConfigRef(cfg, clientType)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusBadRequest, err.Error(), err))
		return false
	}
	restore, err := a.saveClientConfigWithRollback(clientType, *cc)
	if err != nil {
		writeAPIError(w, newAPIError(http.StatusInternalServerError, fmt.Sprintf("failed to save config: %v", err), err))
		return false
	}
	if err := a.reloadRuntimeProviderConfigs(); err != nil {
		if restoreErr := restore(); restoreErr != nil {
			err = fmt.Errorf("failed to apply saved config: %w (rollback failed: %v)", err, restoreErr)
		} else {
			err = fmt.Errorf("failed to apply saved config: %w", err)
		}
		writeAPIError(w, newAPIError(http.StatusInternalServerError, err.Error(), err))
		return false
	}
	return true
}

type configFileBackup struct {
	path    string
	data    []byte
	existed bool
	perm    fs.FileMode
}

//nolint:gosec // path is derived from Clipal-managed config file locations under the configured data directory.
func snapshotConfigFile(path string) (configFileBackup, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configFileBackup{path: path, perm: 0o600}, nil
		}
		return configFileBackup{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return configFileBackup{}, err
	}
	return configFileBackup{
		path:    path,
		data:    data,
		existed: true,
		perm:    fi.Mode().Perm(),
	}, nil
}

func (b configFileBackup) restore() error {
	if !b.existed {
		if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicWriteFile(b.path, b.data, b.perm)
}

func saveConfigFileWithRollback(path string, data []byte, perm fs.FileMode) (func() error, error) {
	backup, err := snapshotConfigFile(path)
	if err != nil {
		return nil, err
	}
	if err := atomicWriteFile(path, data, perm); err != nil {
		return nil, err
	}
	return backup.restore, nil
}

func (a *API) saveGlobalConfigWithRollback(global config.GlobalConfig) (func() error, error) {
	path := filepath.Join(a.configDir, "config.yaml")
	data := formatGlobalConfigYAML(global)
	return saveConfigFileWithRollback(path, data, 0o600)
}

func (a *API) saveClientConfigWithRollback(clientType string, clientCfg config.ClientConfig) (func() error, error) {
	filename, err := config.ClientConfigFilename(clientType)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(a.configDir, filename)
	data := formatClientConfigYAML(clientType, clientCfg)
	return saveConfigFileWithRollback(path, data, 0o600)
}
