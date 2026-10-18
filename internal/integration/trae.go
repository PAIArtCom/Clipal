package integration

import (
	"fmt"
	"path/filepath"

	"github.com/PAIArtCom/Clipal/internal/config"
)

// TRAE Agent uses a working-directory file, not an invented global config:
// https://github.com/bytedance/trae-agent/blob/e839e559ac61bdd0e057c375dd1dee391fee797d/trae_agent/cli.py
// https://github.com/bytedance/trae-agent/blob/e839e559ac61bdd0e057c375dd1dee391fee797d/trae_agent/utils/config.py
func (m Manager) planTRAE(cfg *config.Config) ([]takeoverFile, error) {
	target, err := m.targetOptions(ProductTRAEAgent)
	if err != nil {
		return nil, err
	}
	path := target.ConfigFile
	if path == "" {
		if target.ConfigDir == "" {
			return nil, fmt.Errorf("select the TRAE Agent configuration file used with --config-file before applying takeover")
		}
		dir, e := m.clientConfigDir(ProductTRAEAgent)
		if e != nil {
			return nil, e
		}
		path = filepath.Join(dir, "trae_config.yaml")
	}
	if ext := filepath.Ext(path); ext != ".yaml" && ext != ".yml" {
		return nil, fmt.Errorf("TRAE Agent takeover requires its current YAML configuration, not legacy JSON")
	}
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := parseYAMLObject(file)
	if err != nil {
		return nil, err
	}
	agents, err := objectField(doc, "agents")
	if err != nil {
		return nil, err
	}
	agent, err := objectField(agents, "trae_agent")
	if err != nil {
		return nil, err
	}
	models, err := objectField(doc, "models")
	if err != nil {
		return nil, err
	}
	providers, err := objectField(doc, "model_providers")
	if err != nil {
		return nil, err
	}
	modelKey, err := stringField(agent, "model")
	if err != nil {
		return nil, err
	}
	if modelKey == "" {
		modelKey = "clipal"
	}
	selected, err := objectField(models, modelKey)
	if err != nil {
		return nil, err
	}
	existing, err := stringField(selected, "model")
	if err != nil {
		return nil, err
	}
	if _, err = stringField(selected, "model_provider"); err != nil {
		return nil, err
	}
	model, err := m.selectedModel(ProductTRAEAgent, path, existing)
	if err != nil {
		return nil, err
	}
	// A dedicated route leaves authentication and other models' provider settings intact.
	provider, err := objectField(providers, "clipal")
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"provider", "base_url", "api_key"} {
		if _, err = stringField(provider, key); err != nil {
			return nil, err
		}
	}
	provider["provider"] = "openai"
	provider["base_url"] = m.advertisedBaseURL(cfg)
	if key, ok := provider["api_key"]; !ok || key == "" {
		provider["api_key"] = "clipal"
	}
	selected["model"] = model
	selected["model_provider"] = "clipal"
	// ModelConfig's constructor requires these fields when creating a new model.
	for key, value := range map[string]any{"temperature": 0.5, "top_p": 1, "top_k": 0, "parallel_tool_calls": true, "max_retries": 10} {
		if _, ok := selected[key]; !ok {
			selected[key] = value
		}
	}
	models[modelKey] = selected
	providers["clipal"] = provider
	agents["trae_agent"] = agent
	doc["agents"] = agents
	doc["models"] = models
	doc["model_providers"] = providers
	agent["model"] = modelKey
	if _, ok := agent["max_steps"]; !ok {
		agent["max_steps"] = 200
	}
	if _, enabled := agent["enable_lakeview"]; !enabled && doc["lakeview"] == nil {
		agent["enable_lakeview"] = false
	}
	body, err := marshalYAMLMap(doc)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: body, Format: managedYAML}}, nil
}
