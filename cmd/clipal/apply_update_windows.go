//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/PAIArtCom/Clipal/internal/selfupdate"
)

func runApplyUpdate(args []string) {
	fs := flag.NewFlagSet("__apply-update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pid := fs.Int("pid", 0, "PID to wait for before replacing")
	src := fs.String("src", "", "Downloaded update binary path")
	dst := fs.String("dst", "", "Target executable path to replace")
	helper := fs.String("helper", "", "Helper executable path to delete after update")
	relaunch := fs.Bool("relaunch", false, "Relaunch updated binary after replacing")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if err := selfupdate.ApplyUpdateWindows(selfupdate.ApplyUpdateOptions{
		PID:      *pid,
		Src:      *src,
		Dst:      *dst,
		Helper:   *helper,
		Relaunch: *relaunch,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "clipal: apply update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "clipal: update applied")
}
