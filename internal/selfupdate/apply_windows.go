//go:build windows

package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func applyWindows(exePath string, newBinPath string, relaunch bool, quiet bool) error {
	helperPath := filepath.Join(os.TempDir(), fmt.Sprintf("clipal-updater-%d.exe", time.Now().UnixNano()))
	if err := copyFile(helperPath, exePath, 0o700); err != nil {
		return err
	}

	args := []string{
		"__apply-update",
		"--pid", strconv.Itoa(os.Getpid()),
		"--src", newBinPath,
		"--dst", exePath,
		"--helper", helperPath,
	}
	if relaunch {
		args = append(args, "--relaunch")
	}
	if quiet {
		args = append(args, "--quiet")
	}

	cmd := exec.Command(helperPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(helperPath)
		return err
	}
	// Don't wait: the helper will replace exePath once this process exits.
	return nil
}

type ApplyUpdateOptions struct {
	PID      int
	Src      string
	Dst      string
	Helper   string
	Relaunch bool
	Progress func(message string)
}

func ApplyUpdateWindows(opts ApplyUpdateOptions) error {
	if opts.PID <= 0 {
		return fmt.Errorf("invalid pid")
	}
	if strings.TrimSpace(opts.Src) == "" || strings.TrimSpace(opts.Dst) == "" {
		return fmt.Errorf("missing src/dst")
	}

	emitApplyProgress(opts.Progress, "Waiting for Clipal to exit")
	if err := waitForPID(opts.PID, 5*time.Minute); err != nil {
		return err
	}

	emitApplyProgress(opts.Progress, "Replacing executable")
	dstOld := opts.Dst + ".old"
	_ = os.Remove(dstOld)

	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		// Move current -> .old (best-effort rollback).
		_ = windows.MoveFileEx(windows.StringToUTF16Ptr(opts.Dst), windows.StringToUTF16Ptr(dstOld), windows.MOVEFILE_REPLACE_EXISTING)

		// Move new -> current.
		if err := windows.MoveFileEx(windows.StringToUTF16Ptr(opts.Src), windows.StringToUTF16Ptr(opts.Dst), windows.MOVEFILE_REPLACE_EXISTING); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			// Try to restore .old back to dst (best-effort).
			_ = windows.MoveFileEx(windows.StringToUTF16Ptr(dstOld), windows.StringToUTF16Ptr(opts.Dst), windows.MOVEFILE_REPLACE_EXISTING)
		}
		time.Sleep(250 * time.Millisecond)
	}

	if lastErr != nil {
		return fmt.Errorf("replace failed: %w", lastErr)
	}

	if opts.Helper != "" {
		scheduleDeleteFile(opts.Helper)
	}

	if opts.Relaunch {
		// Relaunch in background; do not block.
		cmd := exec.Command(opts.Dst)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
		}
		_ = cmd.Start()
	}
	emitApplyProgress(opts.Progress, "Update replacement completed")
	return nil
}

func emitApplyProgress(progress func(string), message string) {
	if progress != nil {
		progress(message)
	}
}

func waitForPID(pid int, timeout time.Duration) error {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// If we can't open it, assume it's already gone.
		return nil
	}
	defer windows.CloseHandle(h)

	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	s, err := windows.WaitForSingleObject(h, ms)
	if err != nil {
		return err
	}
	switch uint32(s) {
	case uint32(windows.WAIT_OBJECT_0):
		return nil
	case uint32(windows.WAIT_TIMEOUT):
		return errors.New("timed out waiting for clipal to exit")
	default:
		return fmt.Errorf("wait status %d", uint32(s))
	}
}

func scheduleDeleteFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	// Best-effort self-delete. Use a short delay so the helper exits before deletion.
	// cmd /C "ping 127.0.0.1 -n 2 > NUL & del /f /q \"path\""
	quoted := strings.ReplaceAll(path, `"`, `\"`)
	cmdline := fmt.Sprintf(`ping 127.0.0.1 -n 2 > NUL & del /f /q "%s"`, quoted)
	_ = exec.Command("cmd", "/C", cmdline).Start()
}
