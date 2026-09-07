//go:build !windows

package main

import (
	"fmt"
	"os"
)

func runApplyUpdate(_ []string) {
	fmt.Fprintln(os.Stderr, "clipal: apply update failed: windows self-update not supported on this platform")
	os.Exit(1)
}
