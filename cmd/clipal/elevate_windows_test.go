//go:build windows

package main

import "testing"

func TestServiceDryRunEnabled(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{"long", []string{"install", "--dry-run"}, true},
		{"short", []string{"install", "-dry-run"}, true},
		{"explicit true", []string{"install", "--dry-run=true"}, true},
		{"explicit false", []string{"install", "--dry-run=false"}, false},
		{"explicit zero", []string{"install", "--dry-run=0"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := serviceDryRunEnabled(tt.args); got != tt.want {
				t.Fatalf("serviceDryRunEnabled(%#v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
