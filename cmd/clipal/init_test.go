package main

import (
	"strings"
	"testing"

	"github.com/lansespirit/Clipal/internal/bootstrap"
	"github.com/lansespirit/Clipal/internal/integration"
)

func TestInitIntegrationProduct(t *testing.T) {
	tests := []struct {
		tool bootstrap.Tool
		want integration.ProductID
		ok   bool
	}{
		{bootstrap.ToolCodex, integration.ProductCodexCLI, true},
		{bootstrap.ToolClaude, integration.ProductClaudeCode, true},
		{bootstrap.ToolGemini, integration.ProductGeminiCLI, true},
		{bootstrap.ToolAntigravity, "", false},
	}
	for _, tt := range tests {
		got, ok := initIntegrationProduct(tt.tool)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("initIntegrationProduct(%q) = (%q, %v), want (%q, %v)", tt.tool, got, ok, tt.want, tt.ok)
		}
	}
}

func TestInitHelpMentionsBackupImport(t *testing.T) {
	out, code := runMainHelper(t, "init", "--help")
	if code != 0 || !strings.Contains(out, "--import") {
		t.Fatalf("init help code=%d out=%s", code, out)
	}
}
