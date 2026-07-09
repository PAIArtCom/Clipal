package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

const (
	ManifestName   = "manifest.json"
	PackageVersion = 1
)

type Manifest struct {
	Version         int               `json:"version"`
	CreatedBy       string            `json:"created_by"`
	CreatedAt       string            `json:"created_at,omitempty"`
	ContainsSecrets bool              `json:"contains_secrets"`
	ConfigFiles     map[string]string `json:"config_files"`
	TakeoverTargets []string          `json:"takeover_targets,omitempty"`
}

type ExportOptions struct {
	ConfigDir       string
	Output          string
	TakeoverTargets []string
}

type ImportOptions struct {
	Package   string
	ConfigDir string
}

func ExportPackage(opts ExportOptions) error {
	if strings.TrimSpace(opts.ConfigDir) == "" {
		return fmt.Errorf("config dir is required")
	}
	if strings.TrimSpace(opts.Output) == "" {
		return fmt.Errorf("output path is required")
	}

	configFiles := make(map[string]string, len(config.WatchedConfigFilenames()))
	for _, name := range config.WatchedConfigFilenames() {
		src := filepath.Join(opts.ConfigDir, name)
		info, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("stat %s: %w", name, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", name)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		configFiles[name] = string(data)
	}

	manifest := Manifest{
		Version:         PackageVersion,
		CreatedBy:       "clipal",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		ContainsSecrets: true,
		ConfigFiles:     configFiles,
		TakeoverTargets: append([]string(nil), opts.TakeoverTargets...),
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.MkdirAll(filepath.Dir(opts.Output), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(opts.Output, manifestData, 0o600); err != nil {
		return fmt.Errorf("write package: %w", err)
	}
	return nil
}

func ImportPackage(opts ImportOptions) error {
	if strings.TrimSpace(opts.Package) == "" {
		return fmt.Errorf("package path is required")
	}
	if strings.TrimSpace(opts.ConfigDir) == "" {
		return fmt.Errorf("config dir is required")
	}

	data, err := os.ReadFile(opts.Package)
	if err != nil {
		return fmt.Errorf("read package: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode deploy package: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}

	if err := os.MkdirAll(opts.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	for name, content := range manifest.ConfigFiles {
		if err := validateConfigFilename(name); err != nil {
			return err
		}
		dst := filepath.Join(opts.ConfigDir, name)
		if err := os.WriteFile(dst, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != PackageVersion {
		return fmt.Errorf("unsupported deploy package version %d", manifest.Version)
	}
	if strings.TrimSpace(manifest.CreatedBy) != "clipal" {
		return fmt.Errorf("unsupported deploy package creator %q", manifest.CreatedBy)
	}
	if manifest.ConfigFiles == nil {
		return fmt.Errorf("package missing config_files")
	}
	for name := range manifest.ConfigFiles {
		if err := validateConfigFilename(name); err != nil {
			return err
		}
	}
	return nil
}

func validateConfigFilename(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("unsafe config filename %q", name)
	}
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) || filepath.Clean(name) != name {
		return fmt.Errorf("unsafe config filename %q", name)
	}
	for _, allowed := range config.WatchedConfigFilenames() {
		if name == allowed {
			return nil
		}
	}
	return fmt.Errorf("unsafe config filename %q", name)
}
