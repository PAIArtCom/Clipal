package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// Crush's primary crushrc format is executable Bash. This planner manages only
// the still-supported legacy user JSON when no sibling shell configuration is
// present, and guards higher-priority machine state without editing it.
// Verified against charmbracelet/crush 14ac9bc49eb9702c7618b9e199fae05f23b788f9:
// docs/config/README.md, internal/config/load.go and schema.json.
func (m Manager) planCrush(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductCrush)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "crush.json")
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := parseJSONObject(file)
	if err != nil {
		return nil, err
	}
	var dependencies []backupFile
	for _, name := range []string{"crushrc", ".crushrc"} {
		shell, err := readBackupFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if shell.Existed {
			return nil, fmt.Errorf("crush uses executable %s; configure its gateway provider manually before using JSON takeover", shell.TargetPath)
		}
		dependencies = append(dependencies, shell)
	}
	statePath, err := m.crushStatePath()
	if err != nil {
		return nil, err
	}
	if filepath.Clean(path) == filepath.Clean(statePath) {
		return nil, fmt.Errorf("crush machine state cannot be selected as user configuration")
	}
	state, err := readBackupFile(statePath)
	if err != nil {
		return nil, err
	}
	if state.Existed {
		stateDoc, err := parseJSONObject(state)
		if err != nil {
			return nil, fmt.Errorf("read Crush machine state: %w", err)
		}
		stateModels, err := objectField(stateDoc, "models")
		if err != nil {
			return nil, err
		}
		stateProviders, err := objectField(stateDoc, "providers")
		if err != nil {
			return nil, err
		}
		if len(stateModels) != 0 || stateProviders["clipal"] != nil {
			return nil, fmt.Errorf("crush machine state overrides model or Clipal provider routing; clear that override in Crush before applying takeover")
		}
	}
	dependencies = append(dependencies, state)
	models, err := objectField(doc, "models")
	if err != nil {
		return nil, err
	}
	providers, err := objectField(doc, "providers")
	if err != nil {
		return nil, err
	}
	for name, entries := range map[string]map[string]any{"models": models, "providers": providers} {
		for key, entry := range entries {
			object, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("crush %s.%s must be an object", name, key)
			}
			if name == "providers" {
				if _, err := objectArrayField(object, "models"); err != nil {
					return nil, fmt.Errorf("crush providers.%s: %w", key, err)
				}
			}
		}
	}
	clipal, err := objectField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	large, err := objectField(models, "large")
	if err != nil {
		return nil, err
	}
	existing, ok := large["model"].(string)
	if large["model"] != nil && !ok {
		return nil, fmt.Errorf("crush large model must be a string")
	}
	chosen, err := m.selectedModel(ProductCrush, path, existing)
	if err != nil {
		return nil, err
	}
	catalog, err := objectArrayField(clipal, "models")
	if err != nil {
		return nil, err
	}
	for _, slot := range []string{"large", "small"} {
		selection, err := objectField(models, slot)
		if err != nil {
			return nil, err
		}
		id, ok := selection["model"].(string)
		if selection["model"] != nil && !ok {
			return nil, fmt.Errorf("crush %s model must be a string", slot)
		}
		if slot == "large" || strings.TrimSpace(id) == "" {
			id = chosen
		}
		found := false
		for _, entry := range catalog {
			item, _ := entry.(map[string]any)
			if item["id"] == id {
				found = true
			}
		}
		if !found {
			metadata := map[string]any{"id": id, "name": id}
			if source, _ := selection["provider"].(string); source != "" {
				if provider, ok := providers[source].(map[string]any); ok {
					if entries, ok := provider["models"].([]any); ok {
						for _, entry := range entries {
							if model, ok := entry.(map[string]any); ok && model["id"] == id {
								mergeOpenCodeRouting(metadata, model)
							}
						}
					}
				}
			}
			catalog = append(catalog, metadata)
		}
		selection["provider"], selection["model"] = "clipal", id
		models[slot] = selection
	}
	clipal["type"], clipal["base_url"], clipal["models"], clipal["disable"] = "openai-compat", m.advertisedBaseURL(cfg), catalog, false
	key, validKey := clipal["api_key"].(string)
	if _, exists := clipal["api_key"]; exists && !validKey {
		return nil, fmt.Errorf("crush provider api_key must be a string")
	}
	if strings.TrimSpace(key) == "" && clipal["oauth"] == nil {
		clipal["api_key"] = "clipal"
	}
	providers["clipal"] = clipal
	doc["providers"], doc["models"] = providers, models
	if doc["$schema"] == nil {
		doc["$schema"] = "https://charm.land/crush.json"
	}
	planned, err := marshalJSONMap(doc)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: planned, Dependencies: dependencies, Format: managedJSON}}, nil
}

func (m Manager) crushStatePath() (string, error) {
	root := os.Getenv("CRUSH_GLOBAL_DATA")
	if root == "" {
		if root = os.Getenv("XDG_DATA_HOME"); root != "" {
			root = filepath.Join(root, "crush")
		}
	}
	if root == "" {
		home, err := m.resolveHomeDir()
		if err != nil {
			return "", err
		}
		if runtime.GOOS == "windows" {
			root = os.Getenv("LOCALAPPDATA")
			if root == "" {
				root = filepath.Join(home, "AppData", "Local")
			}
			root = filepath.Join(root, "crush")
		} else {
			root = filepath.Join(home, ".local", "share", "crush")
		}
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("crush data directory must be absolute")
	}
	return filepath.Join(root, "crush.json"), nil
}
