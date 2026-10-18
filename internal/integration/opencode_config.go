package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type openCodeLayer struct {
	file backupFile
	body map[string]any
}
type openCodeConfig struct {
	target       backupFile
	layers       []openCodeLayer
	dependencies []backupFile
}
type openCodeConflictError struct{ path, reason string }

func (e *openCodeConflictError) Error() string { return e.reason }

// Read only the known user layers in the order used by OpenCode. Project,
// remote, and managed configuration requires the client's launch context.
func (m Manager) readOpenCodeCompatibleConfig(product ProductID, targetPath string) (openCodeConfig, error) {
	target, err := m.targetOptions(product)
	if err != nil {
		return openCodeConfig{}, err
	}
	home, err := m.resolveHomeDir()
	if err != nil {
		return openCodeConfig{}, err
	}
	name, prefix, globalNames, dirNames := openCodeLayout(product)
	global := filepath.Join(clientXDGRoot(home), name)
	var paths []string
	for _, filename := range globalNames {
		paths = append(paths, filepath.Join(global, filename))
	}
	customFile := target.ConfigFile
	if customFile == "" {
		customFile = os.Getenv(prefix + "_CONFIG")
	}
	if customFile != "" {
		paths = append(paths, customFile)
	}
	customDir := os.Getenv(prefix + "_CONFIG_DIR")
	if target.ConfigFile == "" && target.ConfigDir != "" {
		customDir = target.ConfigDir
	}
	if customDir != "" {
		for _, filename := range dirNames {
			paths = append(paths, filepath.Join(customDir, filename))
		}
	}
	file, err := readBackupFile(targetPath)
	if err != nil {
		return openCodeConfig{}, err
	}
	state := openCodeConfig{target: file}
	cache := map[string]backupFile{filepath.Clean(targetPath): file}
	guarded := map[string]bool{}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return state, fmt.Errorf("client configuration paths must be absolute")
		}
		path = filepath.Clean(path)
		snapshot, ok := cache[path]
		if !ok {
			snapshot, err = readBackupFile(path)
			if err != nil {
				return state, err
			}
			cache[path] = snapshot
		}
		body := map[string]any{}
		if snapshot.Existed {
			body, err = parseJSONCMap(snapshot.Original)
			if err != nil {
				return state, fmt.Errorf("read client layer %s: %w", path, err)
			}
		}
		state.layers = append(state.layers, openCodeLayer{file: snapshot, body: body})
		if snapshot.TargetPath != file.TargetPath && !guarded[snapshot.TargetPath] {
			state.dependencies = append(state.dependencies, snapshot)
			guarded[snapshot.TargetPath] = true
		}
	}
	if inline := os.Getenv(prefix + "_CONFIG_CONTENT"); inline != "" {
		body, e := parseJSONCMap([]byte(inline))
		if e != nil {
			return state, fmt.Errorf("read %s_CONFIG_CONTENT: %w", prefix, e)
		}
		state.layers = append(state.layers, openCodeLayer{body: body})
	}
	return state, nil
}

func (s openCodeConfig) effective(replacement map[string]any) map[string]any {
	merged := map[string]any{}
	for _, layer := range s.layers {
		body := layer.body
		if replacement != nil && layer.file.TargetPath == s.target.TargetPath {
			body = replacement
		}
		mergeOpenCodeRouting(merged, body)
	}
	return merged
}

// Routing fields deep-merge objects and replace arrays. Instructions/plugins
// have separate client rules and are never copied from this view into a target.
func mergeOpenCodeRouting(dst, src map[string]any) {
	for key, value := range src {
		if object, ok := value.(map[string]any); ok {
			current, _ := dst[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
			}
			mergeOpenCodeRouting(current, object)
			dst[key] = current
		} else {
			dst[key] = value
		}
	}
}

func openCodeRestriction(body map[string]any) string {
	disabled, _ := body["disabled_providers"].([]any)
	if stringListContains(disabled, "clipal") {
		return "disabled_providers disables clipal in the merged OpenCode configuration"
	}
	enabled, _ := body["enabled_providers"].([]any)
	if _, specified := body["enabled_providers"]; specified && !stringListContains(enabled, "clipal") {
		return "enabled_providers excludes clipal in the merged OpenCode configuration"
	}
	return ""
}

func openCodeProvider(doc map[string]any) map[string]any {
	providers, _ := doc["provider"].(map[string]any)
	provider, _ := providers["clipal"].(map[string]any)
	return provider
}

func openCodeRouteShadow(planned, effective map[string]any) bool {
	for _, field := range []string{"model", "small_model"} {
		if value, ok := planned[field]; ok && !reflect.DeepEqual(value, effective[field]) {
			return true
		}
	}
	expected, actual := openCodeProvider(planned), openCodeProvider(effective)
	for _, field := range []string{"npm", "options", "models"} {
		// Compare only the route fields owned by the plan; unrelated inherited
		// metadata and provider models remain outside this check.
		switch field {
		case "npm":
			if !reflect.DeepEqual(expected[field], actual[field]) {
				return true
			}
		case "options":
			e, _ := expected[field].(map[string]any)
			a, _ := actual[field].(map[string]any)
			for _, key := range []string{"baseURL", "apiKey"} {
				if value, ok := e[key]; ok && !reflect.DeepEqual(value, a[key]) {
					return true
				}
			}
		case "models":
			e, _ := expected[field].(map[string]any)
			a, _ := actual[field].(map[string]any)
			for _, selection := range []string{"model", "small_model"} {
				value, _ := planned[selection].(string)
				_, id, _ := strings.Cut(value, "/")
				wanted, _ := e[id].(map[string]any)
				got, _ := a[id].(map[string]any)
				if wire, ok := wanted["id"]; ok && !reflect.DeepEqual(wire, got["id"]) {
					return true
				}
			}
		}
	}
	return false
}

// Kilo and OpenCode share the provider schema but have distinct ordered loaders.
func openCodeLayout(product ProductID) (name, prefix string, globalNames, dirNames []string) {
	if product == ProductKiloCLI {
		return "kilo", "KILO", []string{"config.json", "kilo.json", "kilo.jsonc", "opencode.json", "opencode.jsonc"}, []string{"kilo.jsonc", "kilo.json", "opencode.jsonc", "opencode.json"}
	}
	return "opencode", "OPENCODE", []string{"config.json", "opencode.json", "opencode.jsonc"}, []string{"opencode.json", "opencode.jsonc"}
}
func (m Manager) openCodeCompatiblePath(product ProductID) (string, error) {
	if product == ProductOpenCode {
		return m.opencodeTargetPath()
	}
	target, err := m.targetOptions(product)
	if err != nil {
		return "", err
	}
	if target.ConfigFile != "" {
		return target.ConfigFile, nil
	}
	_, prefix, globalNames, dirNames := openCodeLayout(product)
	if target.ConfigDir == "" && os.Getenv(prefix+"_CONFIG") != "" {
		return m.customClientPath(product, "kilo.json")
	}
	dir, err := m.clientConfigDir(product)
	if err != nil {
		return "", err
	}
	names := globalNames
	if target.ConfigDir != "" || os.Getenv(prefix+"_CONFIG_DIR") != "" {
		names = dirNames
	}
	selected := filepath.Join(dir, "kilo.json")
	for _, name := range names {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			selected = path
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return selected, nil
}
