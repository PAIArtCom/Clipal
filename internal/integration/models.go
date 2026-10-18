package integration

import (
	"fmt"
	"strings"
)

type modelSelectionError struct{ path string }

func (e *modelSelectionError) Error() string {
	return "Select a model supported by the gateway in Configuration location before applying takeover."
}

func (m Manager) selectedModel(product ProductID, path, existing string) (string, error) {
	target, err := m.targetOptions(product)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(target.Model)
	if value == "" {
		value = strings.TrimSpace(existing)
	}
	if value == "" {
		return "", &modelSelectionError{path: path}
	}
	return value, nil
}

func managedOpenAIModel(value string, explicit bool) (string, error) {
	value = strings.TrimSpace(value)
	// An explicit selection is an opaque gateway ID. Aider's routing prefix
	// is separate even when that ID itself starts with "openai/".
	if explicit {
		return "openai/" + value, nil
	}
	if strings.HasPrefix(value, "openai/") {
		return value, nil
	}
	// Provider-qualified IDs cannot be safely renamed without an explicit choice.
	if strings.Contains(value, "/") {
		return "", fmt.Errorf("select an explicit gateway model before moving an Aider provider-qualified model")
	}
	return "openai/" + value, nil
}
