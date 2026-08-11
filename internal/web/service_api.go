package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/logger"
	"github.com/PAIArtCom/Clipal/internal/service"
)

const serviceActionTimeout = 30 * time.Second

var serviceGetStatusFunc = service.GetStatus

// HandleServiceStatus returns best-effort service status.
//
// This endpoint is designed to be UI-friendly: it always returns 200 with a
// structured payload, even when the service is not installed or not running.
func (a *API) HandleServiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	osName := runtime.GOOS

	opts, err := a.serviceOptions()
	if err != nil {
		writeJSON(w, ServiceStatusResponse{
			OS:        osName,
			Supported: true,
			Installed: false,
			OK:        false,
			Error:     err.Error(),
		})
		return
	}

	installCmd, installHint := buildInstallGuidance(opts.BinaryPath, opts.ConfigDir)

	ctx, cancel := context.WithTimeout(context.Background(), serviceActionTimeout)
	defer cancel()

	st, out, statusErr := serviceGetStatusFunc(ctx, opts)
	supported := st.Manager != "unsupported"
	resp := ServiceStatusResponse{
		OS:        osName,
		Supported: supported,
		Installed: st.Installed,
		Loaded:    st.Loaded,
		Running:   st.Running,
		OK:        st.Loaded && st.Running,
		Detail:    st.Detail,
		Output:    out,
	}

	if !supported {
		writeJSON(w, resp)
		return
	}
	if !st.Installed {
		resp.InstallCommand = installCmd
		resp.InstallHint = installHint
	}
	if statusErr != nil {
		resp.Error = statusErr.Error()
		resp.OK = false
	}
	writeJSON(w, resp)
}

// HandleServiceAction triggers a service action by spawning a separate
// "clipal service <action>" process.
//
// We intentionally do not execute service actions inline in the HTTP handler:
// some actions can stop/restart the current process (which would interrupt the
// request before we can respond). The UI is expected to poll status.
func (a *API) HandleServiceAction(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	act, err := service.ParseAction(action)
	if err != nil || act == service.ActionStatus {
		writeError(w, "invalid action", http.StatusBadRequest)
		return
	}

	var req ServiceActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // optional body

	// Respond first; the spawned helper may stop this process.
	writeJSON(w, SuccessResponse{Message: fmt.Sprintf("service action %q requested", action)})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	if err := a.spawnServiceHelper(act, req); err != nil {
		// We already responded to the client; log for troubleshooting.
		logger.Warn("failed to spawn service helper for %s: %v", action, err)
	}
}

func (a *API) serviceOptions() (service.Options, error) {
	exe, err := os.Executable()
	if err != nil {
		return service.Options{}, fmt.Errorf("failed to determine executable path: %v", err)
	}
	if resolved, resolvedErr := filepath.EvalSymlinks(exe); resolvedErr == nil {
		exe = resolved
	}
	if strings.TrimSpace(a.configDir) == "" {
		return service.Options{}, errors.New("config dir is empty")
	}
	return service.Options{
		ConfigDir:  a.configDir,
		BinaryPath: exe,
	}, nil
}

func (a *API) spawnServiceHelper(action service.Action, req ServiceActionRequest) error {
	opts, err := a.serviceOptions()
	if err != nil {
		return err
	}

	args := []string{
		"service",
		string(action),
		"--config-dir", opts.ConfigDir,
	}
	if req.Force {
		args = append(args, "--force")
	}
	if s := strings.TrimSpace(req.StdoutPath); s != "" {
		args = append(args, "--stdout", s)
	}
	if s := strings.TrimSpace(req.StderrPath); s != "" {
		args = append(args, "--stderr", s)
	}

	//nolint:gosec // BinaryPath comes from os.Executable and args are passed directly without shell interpolation.
	cmd := exec.Command(opts.BinaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Start()
}

func buildInstallGuidance(binaryPath, configDir string) (installCommand, installHint string) {
	bin := strings.TrimSpace(binaryPath)
	cfg := strings.TrimSpace(configDir)
	if bin == "" || cfg == "" {
		return "", ""
	}

	if runtime.GOOS == "windows" {
		// In some Windows environments, creating a scheduled task may require an
		// elevated console. WebUI can't elevate a browser action, so provide a
		// copy/paste command for an Administrator PowerShell.
		installCommand = fmt.Sprintf(`"%s" service install --config-dir "%s"`, bin, cfg)
		installHint = "Windows: if install is blocked by policy/UAC, run the command above in an Administrator PowerShell (or create the task manually in Task Scheduler)."
		return installCommand, installHint
	}

	// Non-Windows can typically install from the current user session.
	installCommand = fmt.Sprintf(`%s service install --config-dir %s`, bin, cfg)
	return installCommand, ""
}
