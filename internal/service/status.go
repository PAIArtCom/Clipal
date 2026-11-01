package service

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Status is a best-effort summary of the OS service state.
// It is intentionally small and stable for human- and machine-readable output.
type Status struct {
	Manager   string `json:"manager"`
	Name      string `json:"name"`  // unit name / launchd label / task name
	Scope     string `json:"scope"` // e.g. "user", "system", "gui/501"
	Installed bool   `json:"installed"`
	Loaded    bool   `json:"loaded"` // manager-level loaded/enabled
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`

	// Optional details when available (may be empty).
	BinaryPath string `json:"binary_path,omitempty"`
	ConfigDir  string `json:"config_dir,omitempty"`
	StdoutPath string `json:"stdout_path,omitempty"`
	StderrPath string `json:"stderr_path,omitempty"`
	LastExit   string `json:"last_exit,omitempty"`
	Detail     string `json:"detail,omitempty"` // small note (e.g. ActiveState/SubState)
}

// GetStatus returns a best-effort status summary and the raw manager output (if any).
// The raw output is suitable for "--raw" / troubleshooting.
func GetStatus(ctx context.Context, opts Options) (Status, string, error) {
	return getStatus(ctx, opts)
}

func (s Status) MarshalJSON() ([]byte, error) {
	// Ensure a stable shape even if new fields are added later.
	type alias Status
	return json.Marshal(alias(s))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func parseLaunchdStatus(st Status, raw string) Status {
	st.Loaded = true

	if m := regexp.MustCompile(`(?m)^\s*state\s*=\s*(\S+)`).FindStringSubmatch(raw); len(m) == 2 {
		st.Detail = "state=" + m[1]
		if m[1] == "running" {
			st.Running = true
		}
	}
	if m := regexp.MustCompile(`(?m)^\s*pid\s*=\s*(\d+)`).FindStringSubmatch(raw); len(m) == 2 {
		if pid, err := strconv.Atoi(m[1]); err == nil {
			st.PID = pid
			if pid > 0 {
				st.Running = true
			}
		}
	}
	if m := regexp.MustCompile(`(?m)^\s*program\s*=\s*(.+)$`).FindStringSubmatch(raw); len(m) == 2 {
		st.BinaryPath = strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`(?m)^\s*stdout path\s*=\s*(.+)$`).FindStringSubmatch(raw); len(m) == 2 {
		st.StdoutPath = strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`(?m)^\s*stderr path\s*=\s*(.+)$`).FindStringSubmatch(raw); len(m) == 2 {
		st.StderrPath = strings.TrimSpace(m[1])
	}
	if m := regexp.MustCompile(`(?m)^\s*last exit reason\s*=\s*(.+)$`).FindStringSubmatch(raw); len(m) == 2 {
		st.LastExit = strings.TrimSpace(m[1])
	}

	return st
}

func parseSystemdShowStatus(st Status, raw string) Status {
	var (
		loadState   string
		activeState string
		subState    string
		mainPID     int
	)
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "LoadState":
			loadState = strings.TrimSpace(v)
		case "ActiveState":
			activeState = strings.TrimSpace(v)
		case "SubState":
			subState = strings.TrimSpace(v)
		case "MainPID":
			if pid, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				mainPID = pid
			}
		}
	}

	st.Loaded = (loadState != "" && loadState != "not-found")
	st.Running = (activeState == "active")
	st.PID = mainPID
	if activeState != "" || subState != "" {
		st.Detail = "active=" + activeState + " sub=" + subState
	}
	return st
}

func parseWindowsTaskStatus(st Status, raw string) Status {
	st.Loaded = true

	// schtasks localizes the field name and value (for example, Chinese
	// Windows reports "模式: 运行中"), so accept the common localized form.
	re := regexp.MustCompile(`(?mi)^\s*(?:Status|模式):\s*(.+?)\s*$`)
	if m := re.FindStringSubmatch(raw); len(m) == 2 {
		status := strings.TrimSpace(m[1])
		st.Detail = "status=" + status
		if strings.EqualFold(status, "Running") || status == "运行中" || status == "正在运行" {
			st.Running = true
		}
	}

	return st
}
