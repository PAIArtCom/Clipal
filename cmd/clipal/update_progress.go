package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PAIArtCom/Clipal/internal/selfupdate"
)

type updateProgressRenderer struct {
	writer      io.Writer
	interactive bool

	mu            sync.Mutex
	event         selfupdate.ProgressEvent
	hasEvent      bool
	lastPhase     selfupdate.ProgressPhase
	phaseStarted  time.Time
	spinnerIndex  int
	lastLineWidth int
	ticker        *time.Ticker
	done          chan struct{}
	wg            sync.WaitGroup
}

func newUpdateProgressRenderer(writer io.Writer, enabled bool, interactive bool) *updateProgressRenderer {
	r := &updateProgressRenderer{
		writer:      writer,
		interactive: enabled && interactive,
	}
	if !enabled {
		r.writer = nil
		return r
	}
	if r.interactive {
		r.ticker = time.NewTicker(100 * time.Millisecond)
		r.done = make(chan struct{})
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			for {
				select {
				case <-r.ticker.C:
					r.mu.Lock()
					r.spinnerIndex++
					r.renderCurrentLocked()
					r.mu.Unlock()
				case <-r.done:
					return
				}
			}
		}()
	}
	return r
}

func (r *updateProgressRenderer) Handle(event selfupdate.ProgressEvent) {
	if r.writer == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.interactive {
		if event.Phase != r.lastPhase {
			_, _ = fmt.Fprintf(r.writer, "[update] %s\n", lowerFirst(event.Message))
			r.lastPhase = event.Phase
		}
		return
	}

	phaseChanged := !r.hasEvent || event.Phase != r.event.Phase
	if r.hasEvent && phaseChanged {
		r.renderFinishedLocked("OK", r.event)
	}
	if phaseChanged {
		r.phaseStarted = time.Now()
		r.spinnerIndex = 0
	}
	r.event = event
	r.hasEvent = true
	if phaseChanged || event.TotalBytes > 0 && event.CompletedBytes >= event.TotalBytes {
		r.renderCurrentLocked()
	}
}

func (r *updateProgressRenderer) Finish(success bool) {
	if r.ticker != nil {
		r.ticker.Stop()
		close(r.done)
		r.wg.Wait()
	}
	if !r.interactive {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasEvent {
		return
	}
	status := "OK"
	if !success {
		status = "ERROR"
	}
	r.renderFinishedLocked(status, r.event)
	r.hasEvent = false
}

func (r *updateProgressRenderer) renderCurrentLocked() {
	if !r.hasEvent {
		return
	}
	frames := "|/-\\"
	prefix := string(frames[r.spinnerIndex%len(frames)])
	r.renderLineLocked(prefix+" "+formatProgressEvent(r.event, time.Since(r.phaseStarted)), false)
}

func (r *updateProgressRenderer) renderFinishedLocked(status string, event selfupdate.ProgressEvent) {
	r.renderLineLocked(status+" "+formatProgressEvent(event, time.Since(r.phaseStarted)), true)
}

func (r *updateProgressRenderer) renderLineLocked(line string, newline bool) {
	padding := ""
	if len(line) < r.lastLineWidth {
		padding = strings.Repeat(" ", r.lastLineWidth-len(line))
	}
	_, _ = fmt.Fprintf(r.writer, "\r%s%s", line, padding)
	if newline {
		_, _ = fmt.Fprintln(r.writer)
		r.lastLineWidth = 0
		return
	}
	r.lastLineWidth = len(line)
}

func formatProgressEvent(event selfupdate.ProgressEvent, elapsed time.Duration) string {
	line := event.Message
	if event.TotalBytes > 0 {
		percent := event.CompletedBytes * 100 / event.TotalBytes
		if percent > 100 {
			percent = 100
		}
		line += fmt.Sprintf(" %d%% (%s/%s", percent, formatByteSize(event.CompletedBytes), formatByteSize(event.TotalBytes))
		if elapsed >= 500*time.Millisecond && event.CompletedBytes > 0 {
			bytesPerSecond := float64(event.CompletedBytes) / elapsed.Seconds()
			line += ", " + formatByteSize(int64(bytesPerSecond)) + "/s"
			if event.CompletedBytes < event.TotalBytes && bytesPerSecond > 0 {
				remainingSeconds := float64(event.TotalBytes-event.CompletedBytes) / bytesPerSecond
				remaining := time.Duration(remainingSeconds * float64(time.Second))
				line += ", ETA " + remaining.Round(time.Second).String()
			}
		}
		line += ")"
	} else if event.CompletedBytes > 0 {
		line += " (" + formatByteSize(event.CompletedBytes) + ")"
	}
	return line
}

func formatByteSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	divisor := int64(unit)
	unitName := "KiB"
	for _, candidate := range []string{"MiB", "GiB", "TiB"} {
		if size < divisor*unit {
			break
		}
		divisor *= unit
		unitName = candidate
	}
	return fmt.Sprintf("%.1f %s", float64(size)/float64(divisor), unitName)
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	return strings.ToLower(value[:1]) + value[1:]
}

func isInteractiveTerminal(file *os.File) bool {
	if os.Getenv("CI") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
