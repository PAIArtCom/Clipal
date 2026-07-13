package bootstrap

import (
	"context"
	"errors"
	"testing"
)

type fakeRunner struct {
	paths map[string]string
	calls []string
}

func (r *fakeRunner) LookPath(name string) (string, error) {
	if path := r.paths[name]; path != "" {
		return path, nil
	}
	return "", errors.New("missing")
}
func (r *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name)
	r.paths["agy"] = "/home/user/.local/bin/agy"
	return nil
}

func TestParseToolsDefaultsAndRejectsUnknown(t *testing.T) {
	tools, err := ParseTools("")
	if err != nil || len(tools) != 3 || tools[2] != ToolAntigravity {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	if _, err := ParseTools("codex,unknown"); err == nil {
		t.Fatal("expected unsupported tool error")
	}
}

func TestEnsureSkipsExistingAndInstallsAntigravity(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{"codex": "/bin/codex"}}
	b := Bootstrapper{Runner: runner, GOOS: "linux"}
	existing, err := b.Ensure(context.Background(), ToolCodex, false)
	if err != nil || existing.Installed || existing.Message != "already installed" {
		t.Fatalf("existing=%+v err=%v", existing, err)
	}
	installed, err := b.Ensure(context.Background(), ToolAntigravity, false)
	if err != nil || !installed.Installed || installed.Binary == "" {
		t.Fatalf("installed=%+v err=%v", installed, err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "sh" {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestEnsureDryRunDoesNotExecute(t *testing.T) {
	runner := &fakeRunner{paths: map[string]string{}}
	result, err := (Bootstrapper{Runner: runner, GOOS: "linux"}).Ensure(context.Background(), ToolClaude, true)
	if err != nil || result.Message != "npm install -g @anthropic-ai/claude-code" || len(runner.calls) != 0 {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, runner.calls)
	}
}
