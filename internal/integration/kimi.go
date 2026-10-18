package integration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// Kimi Code (not the archived kimi-cli): MoonshotAI/kimi-code at
// https://github.com/MoonshotAI/kimi-code/tree/4a82a07f08d0b135c4349c20aae53a2d51e5c3fa
// docs/en/configuration/config-files.md
// and packages/agent-core-v2/src/llm-adapter/model/catalog-service.ts.
// A usable model requires configured context metadata; do not invent a window.
func (m Manager) planKimi(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductKimiCLI)
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
			return nil, fmt.Errorf("read Kimi Code config: %w", err)
		}
	}
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
			if _, ok := entry.(map[string]any); !ok {
				return nil, fmt.Errorf("kimi %s.%s must be a table", name, key)
			}
			table, _ := entry.(map[string]any)
			fields := []string{"type", "base_url", "api_key", "api_key_env", "default_model", "model_source"}
			if name == "models" {
				fields = []string{"provider", "provider_id", "model", "name", "base_url", "protocol", "api_key"}
			}
			// ProviderConfigSchema and ModelBaseSchema declare these scalars
			// as optional strings, including credentials retained verbatim.
			for _, field := range fields {
				if _, err := stringField(table, field); err != nil {
					return nil, fmt.Errorf("kimi %s.%s: %w", name, key, err)
				}
			}
			if name == "models" {
				if err := kimiValidateOverrides(table); err != nil {
					return nil, err
				}
			}
		}
	}
	existing, err := stringField(doc, "default_model")
	if err != nil {
		return nil, err
	}
	selection, err := m.selectedModel(ProductKimiCLI, path, existing)
	if err != nil {
		return nil, err
	}
	alias := selection
	source, ok := models[alias].(map[string]any)
	if !ok {
		// A gateway selection may be the wire model ID rather than Kimi's alias.
		alias = ""
		for key, value := range models {
			entry, valid := value.(map[string]any)
			if !valid {
				return nil, fmt.Errorf("kimi models.%s must be a table", key)
			}
			if entry["model"] == selection {
				if alias != "" {
					return nil, fmt.Errorf("select a unique configured Kimi model alias for %q", selection)
				}
				alias, source = key, entry
			}
		}
	}
	target, err := m.targetOptions(ProductKimiCLI)
	if err != nil {
		return nil, err
	}
	if source == nil {
		if target.ContextWindow <= 0 {
			return nil, fmt.Errorf("set the Kimi context window in Configuration location or configure a model with positive max_context_size in %s", path)
		}
		source = map[string]any{"model": selection, "max_context_size": target.ContextWindow}
	}
	wire, err := stringField(source, "model")
	if name, nameErr := stringField(source, "name"); nameErr != nil {
		return nil, nameErr
	} else if name != "" {
		wire = name
	}
	if err != nil || strings.TrimSpace(wire) == "" {
		return nil, fmt.Errorf("kimi models.%s requires a model string", alias)
	}
	context := source["max_context_size"]
	if overrides, exists := source["overrides"]; exists {
		table, valid := overrides.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("kimi model overrides must be a table")
		}
		if value, exists := table["max_context_size"]; exists {
			context = value
		}
	}
	if target.ContextWindow > 0 {
		context = target.ContextWindow
	}
	if size, valid := context.(int64); !valid || size < 1 {
		return nil, fmt.Errorf("kimi models.%s requires a positive max_context_size in %s", alias, path)
	}
	// The source entry and its credentials remain intact. The managed model uses
	// a separate provider so OAuth refreshes cannot reset its gateway routing.
	managed, err := objectField(models, "clipal")
	if err != nil {
		return nil, err
	}
	for key, value := range source {
		switch key {
		case "provider", "provider_id", "base_url", "protocol", "api_key", "oauth":
			continue
		}
		if _, exists := managed[key]; !exists {
			managed[key] = value
		}
	}
	managed["max_context_size"] = context
	if target.ContextWindow > 0 {
		if existing, ok := managed["overrides"].(map[string]any); ok {
			copied := map[string]any{}
			for key, value := range existing {
				copied[key] = value
			}
			copied["max_context_size"] = target.ContextWindow
			managed["overrides"] = copied
		}
	}
	managed["provider"], managed["model"] = "clipal", wire
	if _, exists := managed["name"]; exists {
		managed["name"] = wire
	}
	if _, exists := managed["provider_id"]; exists {
		managed["provider_id"] = "clipal"
	}
	managed["protocol"], managed["base_url"] = "openai", m.advertisedBaseURL(cfg)+"/v1"
	provider, err := objectField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	provider["type"], provider["base_url"] = "openai", m.advertisedBaseURL(cfg)+"/v1"
	if provider["api_key"] == nil && provider["api_key_env"] == nil && provider["oauth"] == nil && provider["env"] == nil {
		provider["api_key"] = "clipal"
	}
	models["clipal"] = managed
	providers["clipal"] = provider
	doc["models"], doc["providers"] = models, providers
	doc["default_model"] = "clipal"
	planned, err := marshalTOMLMap(doc)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: planned, Format: managedTOML}}, nil
}

func kimiValidateOverrides(model map[string]any) error {
	value, exists := model["overrides"]
	if !exists {
		return nil
	}
	table, valid := value.(map[string]any)
	if !valid {
		return fmt.Errorf("kimi model overrides must be a table")
	}
	// The upstream ModelOverrideSchema excludes routing and credential
	// fields. Reject these instead of letting effectiveModelConfig's
	// override spread undo the chosen gateway route.
	for _, key := range []string{"provider", "provider_id", "model", "name", "base_url", "protocol", "api_key", "oauth", "beta_api"} {
		if _, exists := table[key]; exists {
			return fmt.Errorf("kimi model overrides cannot contain %s", key)
		}
	}

	return nil
}
