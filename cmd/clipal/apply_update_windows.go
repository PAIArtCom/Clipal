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
	quiet := fs.Bool("quiet", false, "Suppress update progress output")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	var progress func(string)
	if !*quiet {
		progress = func(message string) {
			fmt.Fprintf(os.Stderr, "[update] %s\n", lowerFirst(message))
		}
	}
	if err := selfupdate.ApplyUpdateWindows(selfupdate.ApplyUpdateOptions{
		PID:      *pid,
		Src:      *src,
		Dst:      *dst,
		Helper:   *helper,
		Relaunch: *relaunch,
		Progress: progress,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "clipal: apply update failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "clipal: update applied")
}
