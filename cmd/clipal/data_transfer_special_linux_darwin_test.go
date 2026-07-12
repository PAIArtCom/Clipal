//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadImportFileRejectsNamedPipeAndAllowsFileSymlink(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "import.pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readImportFile(pipe); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("named pipe error=%v", err)
	}

	target := filepath.Join(dir, "backup.json")
	if err := os.WriteFile(target, []byte(`{"schema":"clipal.data"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "backup-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	data, err := readImportFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"schema":"clipal.data"}` {
		t.Fatalf("symlink data=%q", data)
	}
}
