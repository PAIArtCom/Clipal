//go:build !windows

package selfupdate

import "fmt"

func applyWindows(exePath string, newBinPath string, relaunch bool, quiet bool) error {
	return fmt.Errorf("windows self-update not supported on this platform")
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
	return fmt.Errorf("windows self-update not supported on this platform")
}
