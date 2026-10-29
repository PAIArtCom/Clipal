//go:build !windows

package main

import "github.com/PAIArtCom/Clipal/internal/service"

func maybeElevateService(_ []string, _ service.Action) (bool, error) {
	return false, nil
}
