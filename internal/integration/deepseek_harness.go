package integration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
	"gopkg.in/yaml.v3"
)

// Official DeepSeek Harness master, revision 687f282e8625c6b8d11ff58211a90524be2e4317:
// https://github.com/deepseek-ai/deepseek-harness/blob/687f282e8625c6b8d11ff58211a90524be2e4317/apps/cli/src/profile-boot.ts
// https://github.com/deepseek-ai/deepseek-harness/blob/687f282e8625c6b8d11ff58211a90524be2e4317/packages/llm/llm-pi-ai/README.md
// The home patch overrides every profile. settings.yaml is a legacy migration
// input in this revision; current settings are Cordis patch entries.
func (m Manager) planDeepSeekHarness(cfg *config.Config) ([]takeoverFile, error) {
	dir, err := m.clientConfigDir(ProductDeepSeekHarness)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "cordis.patch.yml")
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	var entries []any
	if file.Existed {
		if err = decodePlannerYAML(file.Original, &entries); err != nil {
			return nil, fmt.Errorf("read DeepSeek Harness patch: %w", err)
		}
		if entries == nil {
			return nil, fmt.Errorf("DeepSeek Harness patch must be a YAML sequence")
		}
	}
	// Validate the complete entry list before editing any entry.
	seen := map[string]bool{}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("DeepSeek Harness patch entries must be objects")
		}
		id, fieldErr := stringField(entry, "id")
		if fieldErr != nil {
			return nil, fieldErr
		}
		if _, fieldErr = stringField(entry, "name"); fieldErr != nil {
			return nil, fieldErr
		}
		if id != "" && seen[id] {
			return nil, fmt.Errorf("duplicate DeepSeek Harness patch entry %q", id)
		}
		seen[id] = true
	}
	entryConfig := func(id string) (map[string]any, error) {
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			if entry["id"] == id {
				body, e := objectField(entry, "config")
				if e == nil {
					entry["config"] = body
				}
				return body, e
			}
		}
		body := map[string]any{}
		entry := map[string]any{"id": id, "config": body}
		entries = append(entries, entry)
		return body, nil
	}
	selection, err := entryConfig("agent-default-model")
	if err != nil {
		return nil, err
	}
	existing, err := stringField(selection, "model")
	if err != nil {
		return nil, err
	}
	if _, err = stringField(selection, "reasoningEffort"); err != nil {
		return nil, err
	}
	if _, err = stringField(selection, "provider"); err != nil {
		return nil, err
	}
	model, err := m.selectedModel(ProductDeepSeekHarness, path, existing)
	if err != nil {
		return nil, err
	}
	llm, err := entryConfig("llm-pi-ai")
	if err != nil {
		return nil, err
	}
	providers, err := objectField(llm, "providers")
	if err != nil {
		return nil, err
	}
	provider, err := objectField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"baseURL", "api", "apiKeyEnv"} {
		if _, err = stringField(provider, key); err != nil {
			return nil, err
		}
	}
	provider["baseURL"] = m.advertisedBaseURL(cfg)
	provider["api"] = "openai-completions"
	models, err := objectArrayField(provider, "models")
	if err != nil {
		return nil, err
	}
	found := false
	for _, raw := range models {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("DeepSeek Harness models must be objects")
		}
		if _, err = stringField(item, "id"); err != nil {
			return nil, err
		}
		if item["id"] == model {
			found = true
		}
	}
	if !found {
		models = append(models, map[string]any{"id": model})
	}
	provider["models"] = models
	// models replaces the catalog, so modelOverrides cannot accompany it.
	if _, ok := provider["modelOverrides"]; ok {
		return nil, fmt.Errorf("DeepSeek Harness clipal route has modelOverrides; select an explicit models catalog before takeover")
	}
	headers, err := objectField(provider, "headers")
	if err != nil {
		return nil, err
	}
	hasAuth := false
	for key, value := range headers {
		if _, ok := value.(string); !ok {
			return nil, fmt.Errorf("DeepSeek Harness provider header %q must be a string", key)
		}
		if strings.EqualFold(key, "Authorization") {
			hasAuth = true
		}
	}
	// The OpenAI protocol requires a key or Authorization even for a local server.
	// Existing credential references and headers remain authoritative.
	if _, ok := provider["apiKeyEnv"]; !ok && !hasAuth {
		headers["Authorization"] = "Bearer clipal"
	}
	provider["headers"] = headers
	providers["clipal"] = provider
	llm["providers"] = providers
	selection["provider"] = "clipal"
	selection["model"] = model
	body, err := yaml.Marshal(entries)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: body, Format: managedYAML}}, nil
}
