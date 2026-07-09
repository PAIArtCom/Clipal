package agentinstall

import (
	"fmt"
	"strings"
)

type AgentID string

const (
	AgentCodex  AgentID = "codex"
	AgentClaude AgentID = "claude"
	AgentGemini AgentID = "gemini"
)

type Plan struct {
	Agent          AgentID
	CheckCommand   string
	InstallCommand []string
}

func DefaultAgents() []AgentID {
	return []AgentID{AgentCodex, AgentClaude, AgentGemini}
}

func PlanForAgent(agent AgentID, goos string) (Plan, error) {
	switch agent {
	case AgentCodex:
		if goos == "windows" {
			return Plan{
				Agent:          AgentCodex,
				CheckCommand:   "codex",
				InstallCommand: []string{"powershell", "-ExecutionPolicy", "ByPass", "-c", "irm https://chatgpt.com/codex/install.ps1 | iex"},
			}, nil
		}
		return Plan{
			Agent:          AgentCodex,
			CheckCommand:   "codex",
			InstallCommand: []string{"sh", "-c", "curl -fsSL https://chatgpt.com/codex/install.sh | sh"},
		}, nil
	case AgentClaude:
		if goos == "windows" {
			return Plan{
				Agent:          AgentClaude,
				CheckCommand:   "claude",
				InstallCommand: []string{"powershell", "-Command", "irm https://claude.ai/install.ps1 | iex"},
			}, nil
		}
		return Plan{
			Agent:          AgentClaude,
			CheckCommand:   "claude",
			InstallCommand: []string{"bash", "-c", "curl -fsSL https://claude.ai/install.sh | bash"},
		}, nil
	case AgentGemini:
		return Plan{
			Agent:          AgentGemini,
			CheckCommand:   "gemini",
			InstallCommand: []string{"npm", "install", "-g", "@google/gemini-cli"},
		}, nil
	default:
		return Plan{}, fmt.Errorf("unknown agent %q", agent)
	}
}

func ParseAgents(raw string) ([]AgentID, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultAgents(), nil
	}
	parts := strings.Split(raw, ",")
	agents := make([]AgentID, 0, len(parts))
	for _, part := range parts {
		agent, err := ParseAgent(part)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, nil
}

func ParseAgent(raw string) (AgentID, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "codex", "codex-cli":
		return AgentCodex, nil
	case "claude", "claude-code", "claudecode":
		return AgentClaude, nil
	case "gemini", "gemini-cli":
		return AgentGemini, nil
	default:
		return "", fmt.Errorf("unknown agent %q", raw)
	}
}
