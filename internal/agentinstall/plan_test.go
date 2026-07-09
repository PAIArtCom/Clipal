package agentinstall

import (
	"reflect"
	"testing"
)

func TestPlanForAgentUsesOfficialInstallCommands(t *testing.T) {
	tests := []struct {
		name      string
		agent     AgentID
		goos      string
		wantCheck string
		wantCmd   []string
	}{
		{
			name:      "CodexUnix",
			agent:     AgentCodex,
			goos:      "linux",
			wantCheck: "codex",
			wantCmd:   []string{"sh", "-c", "curl -fsSL https://chatgpt.com/codex/install.sh | sh"},
		},
		{
			name:      "CodexWindows",
			agent:     AgentCodex,
			goos:      "windows",
			wantCheck: "codex",
			wantCmd:   []string{"powershell", "-ExecutionPolicy", "ByPass", "-c", "irm https://chatgpt.com/codex/install.ps1 | iex"},
		},
		{
			name:      "Claude",
			agent:     AgentClaude,
			goos:      "linux",
			wantCheck: "claude",
			wantCmd:   []string{"bash", "-c", "curl -fsSL https://claude.ai/install.sh | bash"},
		},
		{
			name:      "ClaudeWindows",
			agent:     AgentClaude,
			goos:      "windows",
			wantCheck: "claude",
			wantCmd:   []string{"powershell", "-Command", "irm https://claude.ai/install.ps1 | iex"},
		},
		{
			name:      "Gemini",
			agent:     AgentGemini,
			goos:      "linux",
			wantCheck: "gemini",
			wantCmd:   []string{"npm", "install", "-g", "@google/gemini-cli"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := PlanForAgent(tt.agent, tt.goos)
			if err != nil {
				t.Fatalf("PlanForAgent: %v", err)
			}
			if got.CheckCommand != tt.wantCheck {
				t.Fatalf("CheckCommand = %q, want %q", got.CheckCommand, tt.wantCheck)
			}
			if !reflect.DeepEqual(got.InstallCommand, tt.wantCmd) {
				t.Fatalf("InstallCommand = %#v, want %#v", got.InstallCommand, tt.wantCmd)
			}
		})
	}
}

func TestParseAgents(t *testing.T) {
	got, err := ParseAgents("codex,claude-code,gemini-cli")
	if err != nil {
		t.Fatalf("ParseAgents: %v", err)
	}
	want := []AgentID{AgentCodex, AgentClaude, AgentGemini}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agents = %#v, want %#v", got, want)
	}

	if _, err := ParseAgents("codex,nope"); err == nil {
		t.Fatalf("expected unknown agent error")
	}
}
