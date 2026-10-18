package integration

import (
	"github.com/PAIArtCom/Clipal/internal/config"
)

// iFlow's official settings contract, revision 4642808afbc6580ac117d930f6c64ac0d84955c7:
// https://github.com/iflow-ai/iflow-cli/blob/4642808afbc6580ac117d930f6c64ac0d84955c7/docs_en/configuration/settings.md
func (m Manager) planIFlow(cfg *config.Config) ([]takeoverFile, error) {
	path, err := m.customClientPath(ProductIFlow, "settings.json")
	if err != nil {
		return nil, err
	}
	file, err := readBackupFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := parseJSONObject(file)
	if err != nil {
		return nil, err
	}
	existing, err := stringField(doc, "modelName")
	if err != nil {
		return nil, err
	}
	if _, err = stringField(doc, "apiKey"); err != nil {
		return nil, err
	}
	if _, err = stringField(doc, "baseUrl"); err != nil {
		return nil, err
	}
	if _, err = stringField(doc, "selectedAuthType"); err != nil {
		return nil, err
	}
	model, err := m.selectedModel(ProductIFlow, path, existing)
	if err != nil {
		return nil, err
	}
	doc["baseUrl"] = m.advertisedBaseURL(cfg)
	doc["modelName"] = model
	doc["selectedAuthType"] = "openai-compatible"
	if key, ok := doc["apiKey"]; !ok || key == "" {
		doc["apiKey"] = "clipal"
	}
	body, err := marshalJSONMap(doc)
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: file, Planned: body, Format: managedJSON}}, nil
}
