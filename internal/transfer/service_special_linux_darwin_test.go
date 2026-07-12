//go:build linux || darwin

package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCopyDirectoryRejectsNamedPipeWithoutReadingIt(t *testing.T) {
	source := t.TempDir()
	pipe := filepath.Join(source, "credential.pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	err := copyDirectory(source, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "unsupported credential storage entry type") {
		t.Fatalf("copyDirectory error=%v", err)
	}
	if info, statErr := os.Lstat(pipe); statErr != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("named pipe changed: info=%v err=%v", info, statErr)
	}
}
