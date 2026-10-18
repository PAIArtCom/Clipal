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

type takeoverFile struct {
	backupFile
	Planned []byte
}

func (m Manager) antigravityPaths() (string, string, error) {
	if runtime.GOOS == "windows" {
		return "", "", fmt.Errorf("antigravity CLI takeover requires a Unix shell profile; configure its environment variables manually on Windows")
	}
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", "", err
	}
	profile := ".profile"
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		profile = ".zshrc"
	case "bash":
		profile = ".bashrc"
	case "sh", "dash", ".", "":
	default:
		return "", "", fmt.Errorf("antigravity CLI takeover supports bash, zsh, and POSIX sh profiles")
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), filepath.Join(home, profile), nil
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
	shell, err := readBackupFile(profile)
	if err != nil {
		return nil, err
	}
	shellPlan, err := managedAntigravityShell(string(shell.Original), m.advertisedBaseURL(cfg))
	if err != nil {
		return nil, err
	}
	return []takeoverFile{{backupFile: current, Planned: planned}, {backupFile: shell, Planned: []byte(shellPlan)}}, nil
}

func (m Manager) antigravityStatus(cfg *config.Config) (Status, error) {
	files, err := m.planAntigravity(cfg)
	if err != nil {
		return Status{}, err
	}
	status := Status{Product: ProductAntigravityCLI, State: StateNotConfigured, TargetPath: files[0].TargetPath, BackupAvailable: m.hasLatestBackup(ProductAntigravityCLI), Warning: fmt.Sprintf("Also updates %s. Open a new terminal or source this profile after applying or rolling back; exported environment variables can override it.", files[1].TargetPath)}
	// Compare parsed settings, so formatting changes do not imply a broken takeover.
	body := map[string]any{}
	if files[0].Existed {
		body, err = readJSONCMap(files[0].TargetPath)
		if err != nil {
			return Status{}, err
		}
	}
	if body["modelProvider"] == "gemini" && string(files[1].Original) == string(files[1].Planned) {
		status.State = StateConfigured
	}
	return status, nil
}

func formatTakeoverFiles(files []backupFile) string {
	var out strings.Builder
	for _, file := range files {
		fmt.Fprintf(&out, "# %s\n%s\n", file.TargetPath, file.Original)
	}
	return out.String()
}

func (m Manager) previewAntigravity(cfg *config.Config) (Preview, error) {
	files, err := m.planAntigravity(cfg)
	if err != nil {
		return Preview{}, err
	}
	var current, planned []backupFile
	for _, file := range files {
		current = append(current, file.backupFile)
		next := file.backupFile
		next.Original = file.Planned
		planned = append(planned, next)
	}
	return Preview{Product: ProductAntigravityCLI, CurrentContent: formatTakeoverFiles(current), PlannedContent: formatTakeoverFiles(planned)}, nil
}

func (m Manager) applyAntigravity(cfg *config.Config) (Result, error) {
	files, err := m.planAntigravity(cfg)
	if err != nil {
		return Result{}, err
	}
	status, err := m.antigravityStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	if status.State == StateConfigured {
		return Result{Product: ProductAntigravityCLI, Status: status, Message: "already configured"}, nil
	}
	if !m.hasLatestBackup(ProductAntigravityCLI) {
		if _, err = m.createBackup(ProductAntigravityCLI, files[0].TargetPath, files[1].TargetPath); err != nil {
			return Result{}, err
		}
	}
	for i, file := range files {
		err = os.MkdirAll(filepath.Dir(file.TargetPath), 0o755)
		if err == nil {
			err = os.WriteFile(file.TargetPath, file.Planned, 0o600)
		}
		if err != nil {
			for _, previous := range files[:i+1] {
				if restoreErr := restoreBackupFile(previous.backupFile); restoreErr != nil {
					return Result{}, fmt.Errorf("apply Antigravity CLI: %v; restore %s: %w", err, previous.TargetPath, restoreErr)
				}
			}
			return Result{}, fmt.Errorf("write Antigravity CLI configuration: %w", err)
		}
	}
	status, err = m.antigravityStatus(cfg)
	if err != nil {
		return Result{}, err
	}
	return Result{Product: ProductAntigravityCLI, Status: status, Message: "Antigravity CLI configured; open a new terminal to load its environment"}, nil
}
