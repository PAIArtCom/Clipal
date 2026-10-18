package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const antigravityBlockStart = "# >>> Clipal Antigravity CLI >>>"
const antigravityBlockEnd = "# <<< Clipal Antigravity CLI <<<"

func (m Manager) antigravityPaths() (string, string, error) {
	if runtime.GOOS == "windows" {
		return "", "", fmt.Errorf("antigravity CLI takeover requires a Unix shell profile; configure its environment variables manually on Windows")
	}
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", "", err
	}
	target, err := m.targetOptions(ProductAntigravityCLI)
	if err != nil {
		return "", "", err
	}
	shell := target.Shell
	if shell == "" {
		shell = filepath.Base(os.Getenv("SHELL"))
	}
	if shell == "." || shell == "" {
		return "", "", fmt.Errorf("shell is unknown in this service; select bash, zsh, or sh in Configuration location")
	}
	profile := ".profile"
	switch shell {
	case "zsh":
		profile = ".zshrc"
	case "bash":
		profile = ".bashrc"
	case "sh", "dash":
	default:
		return "", "", fmt.Errorf("antigravity CLI takeover supports bash, zsh, and POSIX sh profiles")
	}
	directory, err := m.clientConfigDir(ProductAntigravityCLI)
	if err != nil {
		return "", "", err
	}
	shellDir := home
	if target.ShellDir != "" {
		shellDir = target.ShellDir
	} else if profile == ".zshrc" && strings.TrimSpace(os.Getenv("ZDOTDIR")) != "" {
		shellDir = os.Getenv("ZDOTDIR")
		if !filepath.IsAbs(shellDir) {
			return "", "", fmt.Errorf("ZDOTDIR must be absolute")
		}
	}
	return filepath.Join(directory, "settings.json"), filepath.Join(shellDir, profile), nil
}

func managedAntigravityShell(content, baseURL string) (string, error) {
	start, end := strings.Index(content, antigravityBlockStart), strings.Index(content, antigravityBlockEnd)
	if start >= 0 || end >= 0 {
		if start < 0 || end < start || strings.Count(content, antigravityBlockStart) != 1 || strings.Count(content, antigravityBlockEnd) != 1 {
			return "", fmt.Errorf("invalid Clipal Antigravity CLI shell block; inspect the profile before applying")
		}
		end += len(antigravityBlockEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		content = content[:start] + content[end:]
	}
	prefix := strings.TrimRight(content, "\n")
	if prefix != "" {
		prefix += "\n\n"
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	return prefix + antigravityBlockStart + "\nexport GEMINI_API_KEY='clipal'\nexport GOOGLE_GEMINI_BASE_URL=" + quote(baseURL) + "\n" + antigravityBlockEnd + "\n", nil
}

func (m Manager) planAntigravity(cfg *config.Config) ([]takeoverFile, error) {
	settings, profile, err := m.antigravityPaths()
	if err != nil {
		return nil, err
	}
	current, err := readBackupFile(settings)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if current.Existed {
		body, err = readJSONCMap(settings)
		if err != nil {
			return nil, fmt.Errorf("read Antigravity CLI settings: %w", err)
		}
	}
	body["modelProvider"] = "gemini"
	planned, err := marshalJSONMap(body)
	if err != nil {
		return nil, err
	}
	files := []takeoverFile{{backupFile: current, Planned: planned}}
	profiles := []string{profile}
	if filepath.Base(profile) == ".bashrc" {
		login := filepath.Join(filepath.Dir(profile), ".profile")
		for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
			path := filepath.Join(filepath.Dir(profile), name)
			if _, err := os.Stat(path); err == nil {
				login = path
				break
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		profiles = append(profiles, login)
	}
	for _, path := range profiles {
		shell, err := readBackupFile(path)
		if err != nil {
			return nil, err
		}
		shellPlan, err := managedAntigravityShell(string(shell.Original), m.advertisedBaseURL(cfg))
		if err != nil {
			return nil, err
		}
		files = append(files, takeoverFile{backupFile: shell, Planned: []byte(shellPlan)})
	}
	return files, nil
}

func formatTakeoverFiles(files []backupFile) string {
	var out strings.Builder
	for _, file := range files {
		fmt.Fprintf(&out, "# %s\n%s\n", file.TargetPath, file.Original)
	}
	return out.String()
}
