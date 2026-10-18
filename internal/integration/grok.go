package integration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// https://github.com/xai-org/grok-build/tree/2bdd1d6a6369de0e8c68132ea4539e9abd9e14a8:
// crates/codegen/xai-grok-pager/docs/user-guide/{05-configuration,11-custom-models}.md.
// GROK_HOME/config.toml defines [models].default and [model.<alias>].
func (m Manager) planGrok(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductGrokBuild)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.toml")
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if file.Existed {
		doc, err = parseTOMLMap(file.Original)
		if err != nil {
			return nil, fmt.Errorf("read Grok Build config: %w", err)
		}
	}
	defaults, err := objectField(doc, "models")
	if err != nil {
		return nil, err
	}
	models, err := objectField(doc, "model")
	if err != nil {
		return nil, err
	}
	for key, value := range models {
		if _, valid := value.(map[string]any); !valid {
			return nil, fmt.Errorf("grok model.%s must be a table", key)
		}
		entry, _ := value.(map[string]any)
		for _, field := range []string{"model", "name", "base_url", "api_backend", "api_key", "model_provider"} {
			if _, err := stringField(entry, field); err != nil {
				return nil, fmt.Errorf("grok model.%s: %w", key, err)
			}
		}
		// Official custom-model credentials accept one env name or an array
		// of names; reject malformed values before preserving them in a plan.
		if value, exists := entry["env_key"]; exists {
			switch typed := value.(type) {
			case string:
			case []any:
				for _, name := range typed {
					if _, ok := name.(string); !ok {
						return nil, fmt.Errorf("grok model.%s env_key entries must be strings", key)
					}
				}
			default:
				return nil, fmt.Errorf("grok model.%s env_key must be a string or array of strings", key)
			}
		}
	}
	existing, err := stringField(defaults, "default")
	if err != nil {
		return nil, err
	}
	// Resolve a saved custom alias to its wire ID before reusing it.
	if value, exists := models[existing]; exists {
		entry, valid := value.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("grok model.%s must be a table", existing)
		}
		if wire, err := stringField(entry, "model"); err != nil {
			return nil, err
		} else if strings.TrimSpace(wire) != "" {
			existing = wire
		}
	}
	selection, err := m.selectedModel(ProductGrokBuild, path, existing)
	if err != nil {
		return nil, err
	}
	managed, err := objectField(models, "clipal")
	if err != nil {
		return nil, err
	}
	managed["model"], managed["base_url"] = selection, m.advertisedBaseURL(cfg)+"/v1"
	managed["api_backend"] = "chat_completions"
	if managed["api_key"] == nil && managed["env_key"] == nil {
		managed["api_key"] = "clipal"
	}
	models["clipal"] = managed
	doc["models"], doc["model"] = defaults, models
	defaults["default"] = "clipal"
	planned, err := marshalTOMLMap(doc)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: planned, Format: managedTOML}}, nil
}
