package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func (m Manager) shellProfilePath(product ProductID) (string, error) {
	if runtime.GOOS == "windows" {
		return "", fmt.Errorf("%s takeover requires a Unix shell profile; configure its environment variables manually on Windows", ProductName(product))
	}
	home, err := m.resolveHomeDir()
	if err != nil {
		return "", err
	}
	target, err := m.targetOptions(product)
	if err != nil {
		return "", err
	}
	shell := target.Shell
	if shell == "" {
		shell = filepath.Base(os.Getenv("SHELL"))
	}
	profile := ".profile"
	switch shell {
	case "zsh":
		profile = ".zshrc"
	case "bash":
		profile = ".bashrc"
	case "sh", "dash":
	case "", ".":
		return "", fmt.Errorf("shell is unknown in this service; select bash, zsh, or sh in Configuration location")
	default:
		return "", fmt.Errorf("%s takeover supports bash, zsh, and POSIX sh profiles", ProductName(product))
	}
	dir := home
	if target.ShellDir != "" {
		dir = target.ShellDir
	} else if profile == ".zshrc" && strings.TrimSpace(os.Getenv("ZDOTDIR")) != "" {
		dir = os.Getenv("ZDOTDIR")
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("ZDOTDIR must be absolute")
		}
	}
	return filepath.Join(dir, profile), nil
}
func managedShellBlock(content, label string, exports [][2]string, unset []string) (string, error) {
	startMarker, endMarker := "# >>> Clipal "+label+" >>>", "# <<< Clipal "+label+" <<<"
	start, end := strings.Index(content, startMarker), strings.Index(content, endMarker)
	if start >= 0 || end >= 0 {
		if start < 0 || end < start || strings.Count(content, startMarker) != 1 || strings.Count(content, endMarker) != 1 {
			return "", fmt.Errorf("invalid Clipal %s shell block; inspect the profile before applying", label)
		}
		end += len(endMarker)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		content = content[:start] + content[end:]
	}
	prefix := strings.TrimRight(content, "\n")
	if prefix != "" {
		prefix += "\n\n"
	}
	var block strings.Builder
	_, _ = block.WriteString(prefix + startMarker + "\n")
	valid := func(key string) bool {
		if key == "" {
			return false
		}
		for i, r := range key {
			if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
				continue
			}
			return false
		}
		return true
	}
	for _, key := range unset {
		if !valid(key) {
			return "", fmt.Errorf("invalid managed environment variable")
		}
		_, _ = block.WriteString("unset " + key + "\n")
	}
	for _, pair := range exports {
		if !valid(pair[0]) {
			return "", fmt.Errorf("invalid managed environment variable")
		}
		_, _ = block.WriteString("export " + pair[0] + "='" + strings.ReplaceAll(pair[1], "'", "'\\''") + "'\n")
	}
	_, _ = block.WriteString(endMarker + "\n")
	return block.String(), nil
}
func (m Manager) planShellProfiles(product ProductID, label string, exports [][2]string, unset ...string) ([]takeoverFile, error) {
	profile, err := m.shellProfilePath(product)
	if err != nil {
		return nil, err
	}
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
	var files []takeoverFile
	for _, path := range profiles {
		file, err := readBackupFile(path)
		if err != nil {
			return nil, err
		}
		planned, err := managedShellBlock(string(file.Original), label, exports, unset)
		if err != nil {
			return nil, err
		}
		files = append(files, takeoverFile{Format: managedBytes, backupFile: file, Planned: []byte(planned)})
	}
	return files, nil
}
