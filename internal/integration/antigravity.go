package integration

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const antigravityBlockStart = "# >>> Clipal Antigravity CLI >>>"

func (m Manager) antigravityPaths() (string, string, error) {
	profile, err := m.shellProfilePath(ProductAntigravityCLI)
	if err != nil {
		return "", "", err
	}
	directory, err := m.clientConfigDir(ProductAntigravityCLI)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(directory, "settings.json"), profile, nil
}

func (m Manager) planAntigravity(cfg *config.Config) ([]takeoverFile, error) {
	settings, _, err := m.antigravityPaths()
	if err != nil {
		return nil, err
	}
	current, err := readBackupFile(settings)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	if current.Existed {
		body, err = parseJSONCMap(current.Original)
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
	shells, err := m.planShellProfiles(ProductAntigravityCLI, "Antigravity CLI", [][2]string{{"GEMINI_API_KEY", "clipal"}, {"GOOGLE_GEMINI_BASE_URL", m.advertisedBaseURL(cfg)}})
	if err != nil {
		return nil, err
	}
	files = append(files, shells...)
	return files, nil
}

func formatTakeoverFiles(files []backupFile) string {
	var out strings.Builder
	for _, file := range files {
		fmt.Fprintf(&out, "# %s\n%s\n", file.TargetPath, file.Original)
	}
	return out.String()
}
