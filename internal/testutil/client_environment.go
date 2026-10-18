package testutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// clientEnvironment lists the inherited settings that can redirect client
// configuration or change takeover inspection. Tests of overrides set them
// explicitly with t.Setenv after TestMain has established this baseline.
func clientEnvironment(home string) map[string]string {
	values := map[string]string{
		"HOME": home, "USERPROFILE": home, "SHELL": "/bin/sh",
		"XDG_CONFIG_HOME": "", "CODEX_HOME": "", "CLAUDE_CONFIG_DIR": "",
		"OPENCODE_CONFIG": "", "OPENCODE_CONFIG_DIR": "", "OPENCODE_CONFIG_CONTENT": "",
		"CONTINUE_GLOBAL_DIR": "", "GOOSE_PATH_ROOT": "", "ZDOTDIR": "",
		"CLAUDE_CODE_USE_BEDROCK": "", "CLAUDE_CODE_USE_VERTEX": "",
		"CLAUDE_CODE_USE_FOUNDRY": "", "CLAUDE_CODE_USE_ANTHROPIC_AWS": "",
		"CLAUDE_CODE_USE_MANTLE": "", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST": "",
		"CLAUDE_CODE_OAUTH_TOKEN": "",
		"PI_CODING_AGENT_DIR":     "", "QWEN_HOME": "", "KIMI_CODE_HOME": "", "GROK_HOME": "", "GROK_CONFIG_PATH": "", "DSH_HOME": "",
		"KILO_CONFIG": "", "KILO_CONFIG_DIR": "", "KILO_CONFIG_CONTENT": "", "CODEBUDDY_CONFIG_DIR": "", "COPILOT_HOME": "", "COPILOT_PROVIDERS_CONFIG": "",
		"CRUSH_GLOBAL_CONFIG": "", "CRUSH_GLOBAL_DATA": "", "XDG_DATA_HOME": "", "LOCALAPPDATA": "",
		"OPENAI_API_KEY": "", "OPENAI_BASE_URL": "",
		"COPILOT_PROVIDER_BASE_URL": "", "COPILOT_PROVIDER_TYPE": "", "COPILOT_PROVIDER_WIRE_API": "", "COPILOT_PROVIDER_API_KEY": "", "COPILOT_MODEL": "",
		"COPILOT_PROVIDER_WIRE_MODEL": "", "COPILOT_PROVIDER_MODEL_ID": "", "COPILOT_PROVIDER_BEARER_TOKEN": "", "COPILOT_PROVIDER_API_KEY_COMMAND": "", "COPILOT_PROVIDER_TRANSPORT": "", "COPILOT_PROVIDER_HEADERS": "",
	}
	for _, prefix := range []string{"IFLOW_", "iflow_"} {
		for _, key := range []string{"baseUrl", "BASE_URL", "apiKey", "API_KEY", "modelName", "MODEL_NAME", "selectedAuthType", "SELECTED_AUTH_TYPE"} {
			values[prefix+key] = ""
		}
	}
	return values
}

// RunWithClientEnvironment is a TestMain entry point, called before any tests
// (including parallel tests) start. Keep production environment lookup intact.
func RunWithClientEnvironment(run func() int) (code int) {
	home, err := os.MkdirTemp("", "clipal-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(home); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}()
	for key, value := range clientEnvironment(home) {
		old, existed := os.LookupEnv(key)
		defer func() {
			if existed {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		}()
		if value == "" {
			err = os.Unsetenv(key)
		} else {
			err = os.Setenv(key, value)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return run()
}

// CheckClientEnvironmentIsolation reruns existing client tests in a process
// inheriting hostile settings. Names must not include the calling test.
func CheckClientEnvironmentIsolation(t *testing.T, names ...string) {
	t.Helper()
	patterns := make([]string, len(names))
	for i, name := range names {
		patterns[i] = regexp.QuoteMeta(name)
	}
	pattern := "^(" + strings.Join(patterns, "|") + ")$"
	foreign := t.TempDir()
	files := map[string][]byte{
		"sentinel":      []byte("host configuration must remain untouched\n"),
		"opencode.json": []byte(`{"disabled_providers":["clipal"]}`),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(foreign, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	for key := range clientEnvironment(foreign) {
		env[key] = foreign
		if strings.HasPrefix(key, "CLAUDE_CODE_") {
			env[key] = "1"
		}
	}
	env["SHELL"] = "/bin/unsupported-shell"
	env["OPENCODE_CONFIG"] = filepath.Join(foreign, "opencode.json")
	env["OPENCODE_CONFIG_CONTENT"] = string(files["opencode.json"])
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run", pattern, "-test.count=1", "-test.v")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("client tests inherited host configuration: %v\n%s", err, output)
	}
	for _, name := range names {
		if !strings.Contains(string(output), "--- PASS: "+name+" (") {
			t.Fatalf("client regression %s did not run and pass:\n%s", name, output)
		}
	}
	entries, err := os.ReadDir(foreign)
	if err != nil || len(entries) != len(files) {
		t.Fatalf("client tests modified host directory: %v, entries: %v", err, entries)
	}
	for name, want := range files {
		path := filepath.Join(foreign, name)
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("client tests modified host file %s: %v", name, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("client tests changed host file permissions: %s: %v", name, err)
		}
	}
}
