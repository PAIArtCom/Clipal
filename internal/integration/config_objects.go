package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
)

// Parse the immutable snapshot used for the plan, never reread its path.
func parseJSONObject(file backupFile) (map[string]any, error) {
	body := map[string]any{}
	if !file.Existed {
		return body, nil
	}
	if err := json.Unmarshal(file.Original, &body); err != nil {
		return nil, fmt.Errorf("read %s: %w", file.TargetPath, err)
	}
	if body == nil {
		return nil, fmt.Errorf("%s must contain an object", file.TargetPath)
	}
	return body, nil
}
func objectField(body map[string]any, key string) (map[string]any, error) {
	value, exists := body[key]
	if !exists {
		return map[string]any{}, nil
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return object, nil
}
func objectArrayField(body map[string]any, key string) ([]any, error) {
	value, exists := body[key]
	if !exists {
		return []any{}, nil
	}
	entries, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", key)
	}
	for _, entry := range entries {
		if item, ok := entry.(map[string]any); !ok || item == nil {
			return nil, fmt.Errorf("%s entries must be objects", key)
		}
	}
	return entries, nil
}
func stringField(body map[string]any, key string) (string, error) {
	value, exists := body[key]
	if !exists {
		return "", nil
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return result, nil
}

// Reject additional YAML documents instead of silently discarding user data.
func decodePlannerYAML(raw []byte, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("multiple YAML documents are unsupported")
	}
	return nil
}

func parseYAMLObject(file backupFile) (map[string]any, error) {
	doc := map[string]any{}
	if !file.Existed {
		return doc, nil
	}
	if err := decodePlannerYAML(file.Original, &doc); err != nil {
		return nil, fmt.Errorf("read YAML configuration: %w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("YAML configuration must be an object")
	}
	return doc, nil
}
