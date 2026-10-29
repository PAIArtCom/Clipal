package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Plan struct {
	Notes       []string
	Mkdirs      []string
	Writes      []FileWrite
	Copies      []FileCopy
	Removes     []string
	PreCommands []Command
	Commands    []Command
}

type FileCopy struct {
	Source string
	Dest   string
	Perm   fs.FileMode
}

type FileWrite struct {
	Path    string
	Content []byte
	Perm    fs.FileMode
}

type Command struct {
	Path        string
	Args        []string
	IgnoreError bool
}

func ExecutePlan(ctx context.Context, plan *Plan, dryRun bool) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("nil plan")
	}

	var out bytes.Buffer
	for _, note := range plan.Notes {
		fmt.Fprintf(&out, "%s\n", note)
	}

	for _, dir := range plan.Mkdirs {
		if dryRun {
			fmt.Fprintf(&out, "mkdir -p %s\n", dir)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return out.String(), err
		}
	}

	for _, w := range plan.Writes {
		if dryRun {
			fmt.Fprintf(&out, "write %s\n", w.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(w.Path), 0o755); err != nil {
			return out.String(), err
		}
		perm := w.Perm
		if perm == 0 {
			perm = 0o644
		}
		if err := os.WriteFile(w.Path, w.Content, perm); err != nil {
			return out.String(), err
		}
	}

	for _, c := range plan.PreCommands {
		if dryRun {
			fmt.Fprintf(&out, "%s %s\n", c.Path, joinArgs(c.Args))
			continue
		}
		cmd := exec.CommandContext(ctx, c.Path, c.Args...)
		if runtime.GOOS == "windows" {
			cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		} else {
			cmd.Stdout, cmd.Stderr = &out, &out
		}
		if err := cmd.Run(); err != nil && !c.IgnoreError {
			return out.String(), err
		}
	}

	for _, c := range plan.Copies {
		if dryRun {
			fmt.Fprintf(&out, "cp %s %s\n", c.Source, c.Dest)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(c.Dest), 0o755); err != nil {
			return out.String(), err
		}
		in, err := os.Open(c.Source)
		if err != nil {
			return out.String(), err
		}
		perm := c.Perm
		if perm == 0 {
			perm = 0o700
		}
		outFile, err := os.OpenFile(c.Dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
		if err != nil {
			_ = in.Close()
			return out.String(), err
		}
		_, copyErr := io.Copy(outFile, in)
		closeOutErr := outFile.Close()
		closeInErr := in.Close()
		if copyErr != nil {
			return out.String(), copyErr
		}
		if closeOutErr != nil {
			return out.String(), closeOutErr
		}
		if closeInErr != nil {
			return out.String(), closeInErr
		}
	}

	for _, p := range plan.Removes {
		if dryRun {
			fmt.Fprintf(&out, "rm %s\n", p)
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return out.String(), err
		}
	}

	for _, c := range plan.Commands {
		if dryRun {
			fmt.Fprintf(&out, "%s %s\n", c.Path, joinArgs(c.Args))
			continue
		}
		cmd := exec.CommandContext(ctx, c.Path, c.Args...)

		// On Windows, prefer inheriting the parent console handles so tools like
		// schtasks.exe can render localized output correctly (avoids mojibake
		// caused by capturing + re-printing bytes in a different code page).
		if runtime.GOOS == "windows" {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil && !c.IgnoreError {
				return out.String(), err
			}
			continue
		}

		b, err := cmd.CombinedOutput()
		// If the caller marked this command as best-effort, suppress both its error
		// and any emitted output. Tools like launchctl may print scary messages for
		// idempotent operations (e.g., bootstrap when already loaded) even though
		// the overall action succeeds.
		if err != nil && c.IgnoreError {
			continue
		}
		if len(b) > 0 {
			_, _ = out.Write(b)
			if b[len(b)-1] != '\n' {
				_ = out.WriteByte('\n')
			}
		}
		if err != nil {
			return out.String(), err
		}
	}

	return out.String(), nil
}

func joinArgs(args []string) string {
	var b bytes.Buffer
	for i, a := range args {
		if i > 0 {
			_ = b.WriteByte(' ')
		}
		_, _ = b.WriteString(shellEscape(a))
	}
	return b.String()
}

func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '"', '\'', '\\', '$', '`', '!', '(', ')', '{', '}', '[', ']', ';', '&', '|', '<', '>', '*', '?', '#':
			return "'" + strings.ReplaceAll(s, "'", `'\'\''`) + "'"
		}
	}
	return s
}
