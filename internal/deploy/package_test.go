package deploy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestConfigFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func readPackageJSON(t *testing.T, packagePath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatalf("ReadFile package: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal package JSON: %v\n%s", err, data)
	}
	return out
}

func TestExportImportPackagePreservesConfigFiles(t *testing.T) {
	src := t.TempDir()
	writeTestConfigFile(t, src, "config.yaml", `
listen_addr: 127.0.0.1
port: 3333
`)
	writeTestConfigFile(t, src, "openai.yaml", `
mode: auto
providers:
  - name: primary
    base_url: https://api.example.com
    api_key: sk-secret
    priority: 1
`)
	writeTestConfigFile(t, src, "gemini.yaml", `
mode: auto
providers: []
`)

	packagePath := filepath.Join(t.TempDir(), "prod.json")
	if err := ExportPackage(ExportOptions{
		ConfigDir: src,
		Output:    packagePath,
	}); err != nil {
		t.Fatalf("ExportPackage: %v", err)
	}

	payload := readPackageJSON(t, packagePath)
	if payload["contains_secrets"] != true {
		t.Fatalf("package does not mark secrets: %#v", payload["contains_secrets"])
	}
	files, ok := payload["config_files"].(map[string]any)
	if !ok {
		t.Fatalf("config_files is not an object: %#v", payload["config_files"])
	}
	openAIYAML, _ := files["openai.yaml"].(string)
	if !strings.Contains(openAIYAML, "api_key: sk-secret") {
		t.Fatalf("openai config did not preserve secret: %s", openAIYAML)
	}

	dst := t.TempDir()
	if err := ImportPackage(ImportOptions{
		Package:   packagePath,
		ConfigDir: dst,
	}); err != nil {
		t.Fatalf("ImportPackage: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "openai.yaml"))
	if err != nil {
		t.Fatalf("ReadFile openai.yaml: %v", err)
	}
	if string(got) != openAIYAML {
		t.Fatalf("imported openai.yaml mismatch\ngot:\n%s\nwant:\n%s", got, openAIYAML)
	}
	if _, err := os.Stat(filepath.Join(dst, "claude.yaml")); !os.IsNotExist(err) {
		t.Fatalf("missing source claude.yaml should not be created, err=%v", err)
	}
}

func TestImportPackageRejectsUnsafePaths(t *testing.T) {
	packagePath := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(packagePath, []byte(`{
  "version": 1,
  "created_by": "clipal",
  "contains_secrets": true,
  "config_files": {
    "../evil.yaml": "api_key: sk-secret\n"
  }
}`), 0o600); err != nil {
		t.Fatalf("WriteFile package: %v", err)
	}

	dst := t.TempDir()
	err := ImportPackage(ImportOptions{
		Package:   packagePath,
		ConfigDir: dst,
	})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("ImportPackage err = %v, want unsafe path error", err)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "evil.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe file should not be written, stat err=%v", statErr)
	}
}
