package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/selfupdate"
)

func TestUpdateProgressRendererNonInteractive(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	renderer := newUpdateProgressRenderer(&output, true, false)
	renderer.Handle(selfupdate.ProgressEvent{Phase: selfupdate.ProgressCheckingRelease, Message: "Checking latest release"})
	renderer.Handle(selfupdate.ProgressEvent{Phase: selfupdate.ProgressDownloadingBinary, Message: "Downloading clipal", TotalBytes: 10})
	renderer.Handle(selfupdate.ProgressEvent{Phase: selfupdate.ProgressDownloadingBinary, Message: "Downloading clipal", CompletedBytes: 5, TotalBytes: 10})
	renderer.Finish(true)

	got := output.String()
	if strings.Count(got, "checking latest release") != 1 || strings.Count(got, "downloading clipal") != 1 {
		t.Fatalf("unexpected phase output: %q", got)
	}
	if strings.Contains(got, "\r") {
		t.Fatalf("non-interactive output contains carriage return: %q", got)
	}
}

func TestUpdateProgressRendererInteractive(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	renderer := newUpdateProgressRenderer(&output, true, true)
	renderer.Handle(selfupdate.ProgressEvent{Phase: selfupdate.ProgressDownloadingBinary, Message: "Downloading clipal", CompletedBytes: 5, TotalBytes: 10})
	renderer.Finish(true)

	got := output.String()
	if !strings.Contains(got, "50%") || !strings.Contains(got, "OK Downloading clipal") || !strings.Contains(got, "\r") {
		t.Fatalf("unexpected interactive output: %q", got)
	}
}

func TestUpdateProgressRendererDisabled(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	renderer := newUpdateProgressRenderer(&output, false, true)
	renderer.Handle(selfupdate.ProgressEvent{Phase: selfupdate.ProgressCheckingRelease, Message: "Checking latest release"})
	renderer.Finish(false)
	if output.Len() != 0 {
		t.Fatalf("disabled renderer output = %q", output.String())
	}
}

func TestFormatProgressEvent(t *testing.T) {
	t.Parallel()

	got := formatProgressEvent(selfupdate.ProgressEvent{
		Message:        "Downloading clipal",
		CompletedBytes: 512,
		TotalBytes:     1024,
	}, time.Second)
	for _, want := range []string{"50%", "512 B/1.0 KiB", "512 B/s", "ETA 1s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatProgressEvent() = %q, missing %q", got, want)
		}
	}
}
