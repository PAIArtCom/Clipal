package proxy

import (
	"fmt"
	"runtime"
	"strings"
)

const geminiOAuthProjectMetadata = "project_id"

func geminiModelFromPath(path string) (string, error) {
	path = normalizeUpstreamPath(path)
	modelPrefix := ""
	switch {
	case strings.HasPrefix(path, "/v1beta/models/"):
		modelPrefix = "/v1beta/models/"
	case strings.HasPrefix(path, "/v1/models/"):
		modelPrefix = "/v1/models/"
	default:
		return "", fmt.Errorf("gemini model request does not support path %q", path)
	}

	rest := strings.TrimPrefix(path, modelPrefix)
	modelName, _, ok := strings.Cut(rest, ":")
	modelName = strings.TrimSpace(strings.TrimSuffix(modelName, "/"))
	if !ok || modelName == "" {
		return "", fmt.Errorf("gemini model request does not support path %q", path)
	}
	return modelName, nil
}

func geminiOAuthOS() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
