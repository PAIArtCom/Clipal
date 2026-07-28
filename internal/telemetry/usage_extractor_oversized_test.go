package telemetry

import (
	"strings"
	"testing"
)

func TestUsageExtractorOversizedSSEEventIsBoundedAndSkipped(t *testing.T) {
	extractor := NewUsageExtractor("openai", "openai_responses", "text/event-stream")
	event := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"output":[{"result":"` +
		strings.Repeat("A", 4*maxSSEEventCaptureBytes) + `"}]}}` + "\n\n"

	const chunkSize = 4096
	for offset := 0; offset < len(event); offset += chunkSize {
		end := offset + chunkSize
		if end > len(event) {
			end = len(event)
		}
		extractor.Append([]byte(event[offset:end]))
		if len(extractor.lineBuf) > maxSSEEventCaptureBytes {
			t.Fatalf("line buffer grew to %d bytes", len(extractor.lineBuf))
		}
		if len(extractor.eventData) > maxSSEEventCaptureBytes {
			t.Fatalf("event buffer grew to %d bytes", len(extractor.eventData))
		}
	}

	if !extractor.completed {
		t.Fatal("oversized response.completed event did not preserve completion metadata")
	}
	if len(extractor.lineBuf) != 0 || len(extractor.eventData) != 0 {
		t.Fatalf("oversized event buffers were retained: line=%d event=%d", len(extractor.lineBuf), len(extractor.eventData))
	}
	if usage, ok := extractor.Finalize(); ok {
		t.Fatalf("oversized event should skip best-effort usage parsing, got %#v", usage)
	}
}

func TestUsageExtractorOversizedEventResetsBeforeNextEvent(t *testing.T) {
	extractor := NewUsageExtractor("openai", "openai_responses", "text/event-stream")
	extractor.Append([]byte("event: response.output_text.delta\n"))
	extractor.Append([]byte("data: " + strings.Repeat("X", maxSSEEventCaptureBytes+1) + "\n\n"))
	extractor.Append([]byte("event: response.completed\n"))
	extractor.Append([]byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}}` + "\n\n"))

	usage, ok := extractor.Finalize()
	if !ok {
		t.Fatal("normal event after oversized event was not parsed")
	}
	if usage.InputTokens != 3 || usage.OutputTokens != 5 || usage.TotalTokens != 8 {
		t.Fatalf("usage=%#v", usage)
	}
}

func TestUsageExtractorSupportsAllSSELineEndings(t *testing.T) {
	for _, lineEnding := range []string{"\n", "\r\n", "\r"} {
		name := strings.ReplaceAll(strings.ReplaceAll(lineEnding, "\r", "CR"), "\n", "LF")
		t.Run(name, func(t *testing.T) {
			extractor := NewUsageExtractor("openai", "openai_responses", "text/event-stream")
			event := "event: response.completed" + lineEnding +
				`data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":4,"total_tokens":6}}}` +
				lineEnding + lineEnding
			for i := range len(event) {
				extractor.Append([]byte(event[i : i+1]))
			}
			usage, ok := extractor.Finalize()
			if !ok || usage.TotalTokens != 6 {
				t.Fatalf("usage=%#v ok=%v", usage, ok)
			}
		})
	}
}
