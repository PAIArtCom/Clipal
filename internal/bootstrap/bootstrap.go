// Package bootstrap provides the bounded, one-time server setup flow used by
// `clipal init`. It deliberately does not manage CLI versions or sessions.
package bootstrap

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

type Tool string

const (
	ToolCodex       Tool = "codex"
	ToolClaude      Tool = "claude"
	ToolAntigravity Tool = "antigravity"
	ToolGemini      Tool = "gemini"
)

func SupportedTools() []Tool {
	return []Tool{ToolCodex, ToolClaude, ToolAntigravity, ToolGemini}
}

func ParseTools(raw string) ([]Tool, error) {
	if strings.TrimSpace(raw) == "" {
		return []Tool{ToolCodex, ToolClaude, ToolAntigravity}, nil
	}
	seen := make(map[Tool]bool)
	var tools []Tool
	for _, value := range strings.Split(raw, ",") {
		tool := Tool(strings.ToLower(strings.TrimSpace(value)))
		switch tool {
		case ToolCodex, ToolClaude, ToolAntigravity, ToolGemini:
		default:
			return nil, fmt.Errorf("unsupported tool %q", value)
		}
		if !seen[tool] {
			seen[tool] = true
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

type Runner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) error
}

type systemRunner struct{}

func (systemRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (systemRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run() // #nosec G204 -- commands are fixed below.
}

type Result struct {
	Tool      Tool
	Binary    string
	Installed bool
	Message   string
}

type Bootstrapper struct {
	Runner Runner
	GOOS   string
}

func New() Bootstrapper { return Bootstrapper{Runner: systemRunner{}, GOOS: runtime.GOOS} }

func (b Bootstrapper) Ensure(ctx context.Context, tool Tool, dryRun bool) (Result, error) {
	if b.Runner == nil {
		b.Runner = systemRunner{}
	}
	if b.GOOS == "" {
		b.GOOS = runtime.GOOS
	}
	binary := binaryName(tool)
	if path, err := b.Runner.LookPath(binary); err == nil {
		return Result{Tool: tool, Binary: path, Message: "already installed"}, nil
	}
	if dryRun {
		return Result{Tool: tool, Binary: binary, Message: installHint(tool)}, nil
	}
	name, args, err := installCommand(tool, b.GOOS)
	if err != nil {
		return Result{}, err
	}
	if err := b.Runner.Run(ctx, name, args...); err != nil {
		return Result{}, fmt.Errorf("install %s: %w", tool, err)
	}
	path, err := b.Runner.LookPath(binary)
	if err != nil {
		return Result{}, fmt.Errorf("%s installer completed but %q is not on PATH", tool, binary)
	}
	return Result{Tool: tool, Binary: path, Installed: true, Message: "installed"}, nil
}

func binaryName(tool Tool) string {
	switch tool {
	case ToolAntigravity:
		return "agy"
	default:
		return string(tool)
	}
}

func installHint(tool Tool) string {
	switch tool {
	case ToolCodex:
		return "npm install -g @openai/codex"
	case ToolClaude:
		return "npm install -g @anthropic-ai/claude-code"
	case ToolAntigravity:
		return "curl -fsSL https://antigravity.google/cli/install.sh | bash"
	case ToolGemini:
		return "npm install -g @google/gemini-cli"
	default:
		return ""
	}
}

func installCommand(tool Tool, goos string) (string, []string, error) {
	if goos == "windows" {
		return "", nil, fmt.Errorf("automatic installation of %s is not supported on Windows; run the official installer", tool)
	}
	switch tool {
	case ToolCodex:
		return "npm", []string{"install", "-g", "@openai/codex"}, nil
	case ToolClaude:
		return "npm", []string{"install", "-g", "@anthropic-ai/claude-code"}, nil
	case ToolGemini:
		return "npm", []string{"install", "-g", "@google/gemini-cli"}, nil
	case ToolAntigravity:
		return "sh", []string{"-c", "curl -fsSL https://antigravity.google/cli/install.sh | bash"}, nil
	default:
		return "", nil, fmt.Errorf("unsupported tool %q", tool)
	}
}
