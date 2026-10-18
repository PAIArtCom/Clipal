package integration

import (
	"fmt"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// Qwen modelProviders arrays replace across scopes; preserve the complete catalog.
// https://github.com/QwenLM/qwen-code/blob/2f2e7256fb134513490eeefbf7743e8564bba459/docs/users/configuration/model-providers.md
func (m Manager) planQwen(cfg *config.Config) ([]takeoverFile, error) {
	path, err := m.customClientPath(ProductQwenCode, "settings.json")
	if err != nil {
		return nil, err
	}
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	doc := map[string]any{}
	if file.Existed {
		doc, err = parseJSONCMap(file.Original)
		if err != nil {
			return nil, err
		}
	}
	providers, err := objectField(doc, "modelProviders")
	if err != nil {
		return nil, err
	}
	// Validate every array: silently replacing a wrapped/invalid entry loses data.
	for name := range providers {
		if _, err = objectArrayField(providers, name); err != nil {
			return nil, err
		}
	}
	entries, err := objectArrayField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	protocols, err := objectField(doc, "providerProtocol")
	if err != nil {
		return nil, err
	}
	modelConfig, err := objectField(doc, "model")
	if err != nil {
		return nil, err
	}
	security, err := objectField(doc, "security")
	if err != nil {
		return nil, err
	}
	auth, err := objectField(security, "auth")
	if err != nil {
		return nil, err
	}
	env, err := objectField(doc, "env")
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"selectedType", "enforcedType", "apiKey", "baseUrl"} {
		if _, err := stringField(auth, key); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"name", "baseUrl"} {
		if _, err := stringField(modelConfig, key); err != nil {
			return nil, err
		}
	}
	if _, err := stringField(protocols, "clipal"); err != nil {
		return nil, err
	}
	if _, err := stringField(env, "CLIPAL_API_KEY"); err != nil {
		return nil, err
	}
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		for _, key := range []string{"id", "baseUrl", "wireApi", "envKey"} {
			if _, err := stringField(item, key); err != nil {
				return nil, err
			}
		}
	}
	if enforced, _ := stringField(auth, "enforcedType"); enforced != "" && enforced != "openai" {
		return nil, fmt.Errorf("qwen enforced auth type %q prevents selecting the Clipal provider", enforced)
	}
	existing, _ := stringField(modelConfig, "name")
	model, err := m.selectedModel(ProductQwenCode, path, existing)
	if err != nil {
		return nil, err
	}
	var selected map[string]any
	for _, entry := range entries {
		item, _ := entry.(map[string]any)
		if item["id"] == model {
			selected = item
			break
		}
	}
	if selected == nil {
		selected = map[string]any{"id": model}
		entries = append(entries, selected)
	}
	selected["baseUrl"] = m.advertisedBaseURL(cfg)
	selected["wireApi"] = "chat-completions"
	key, _ := stringField(selected, "envKey")
	if strings.TrimSpace(key) == "" {
		selected["envKey"] = "CLIPAL_API_KEY"
		if value, present := env["CLIPAL_API_KEY"]; !present || value == "" {
			env["CLIPAL_API_KEY"] = "clipal"
		}
	}
	providers["clipal"] = entries
	protocols["clipal"] = "openai"
	modelConfig["name"] = model
	modelConfig["baseUrl"] = m.advertisedBaseURL(cfg)
	auth["selectedType"] = "openai"
	security["auth"] = auth
	doc["modelProviders"], doc["providerProtocol"], doc["model"] = providers, protocols, modelConfig
	doc["security"], doc["env"] = security, env
	planned, err := marshalJSONMap(doc)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: planned, Format: managedJSONC}}, nil
}
