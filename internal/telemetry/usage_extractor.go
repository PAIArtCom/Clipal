package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	maxJSONCaptureBytes     = 512 * 1024
	maxSSEEventCaptureBytes = 256 * 1024
)

type usageMode int

const (
	usageModeUnsupported usageMode = iota
	usageModeOpenAIJSON
	usageModeClaudeJSON
	usageModeGeminiJSON
	usageModeOpenAISSE
	usageModeClaudeSSE
	usageModeGeminiSSE
)

type UsageExtractor struct {
	mode usageMode

	jsonCapture []byte
	jsonFile    *os.File

	lineBuf        []byte
	lineOverflow   bool
	pendingCR      bool
	eventName      string
	eventData      []byte
	eventOversized bool

	completed bool
	snapshot  UsageSnapshot
	found     bool
}

func NewUsageExtractor(family string, capability string, contentType string) *UsageExtractor {
	mode := detectUsageMode(strings.TrimSpace(family), strings.TrimSpace(capability), strings.TrimSpace(contentType))
	if mode == usageModeUnsupported {
		return nil
	}
	return &UsageExtractor{mode: mode}
}

func detectUsageMode(family string, capability string, contentType string) usageMode {
	isSSE := strings.Contains(strings.ToLower(contentType), "text/event-stream")
	switch capability {
	case "openai_compatible", "openai_chat_completions", "openai_completions", "openai_responses", "openai_images":
		if isSSE {
			return usageModeOpenAISSE
		}
		return usageModeOpenAIJSON
	case "claude_compatible", "claude_messages":
		if isSSE {
			return usageModeClaudeSSE
		}
		return usageModeClaudeJSON
	case "gemini_compatible", "gemini_generate_content", "gemini_stream_generate_content":
		if isSSE {
			return usageModeGeminiSSE
		}
		if family == "gemini" {
			return usageModeGeminiJSON
		}
	}

	switch family {
	case "openai":
		if isSSE {
			return usageModeOpenAISSE
		}
		return usageModeOpenAIJSON
	case "claude":
		if isSSE {
			return usageModeClaudeSSE
		}
		return usageModeClaudeJSON
	case "gemini":
		if isSSE {
			return usageModeGeminiSSE
		}
		return usageModeGeminiJSON
	default:
		return usageModeUnsupported
	}
}

func (e *UsageExtractor) Append(chunk []byte) {
	if e == nil || len(chunk) == 0 {
		return
	}
	switch e.mode {
	case usageModeOpenAISSE, usageModeClaudeSSE, usageModeGeminiSSE:
		e.appendSSE(chunk)
	default:
		e.appendJSON(chunk)
	}
}

func (e *UsageExtractor) Finalize() (UsageSnapshot, bool) {
	if e == nil {
		return UsageSnapshot{}, false
	}
	switch e.mode {
	case usageModeOpenAISSE, usageModeClaudeSSE, usageModeGeminiSSE:
		if e.pendingCR {
			e.pendingCR = false
			e.completeSSELine()
		}
		if len(e.lineBuf) > 0 {
			line := bytes.TrimSuffix(e.lineBuf, []byte{'\r'})
			overflow := e.lineOverflow
			e.lineBuf = nil
			e.lineOverflow = false
			if len(line) > 0 {
				e.processSSELine(line, overflow)
			}
		}
		e.flushPendingEvent()
		if !e.completed {
			return UsageSnapshot{}, false
		}
		return e.snapshot, e.found
	case usageModeOpenAIJSON, usageModeClaudeJSON, usageModeGeminiJSON:
		return e.extractFromJSON()
	default:
		return UsageSnapshot{}, false
	}
}

func (e *UsageExtractor) extractFromJSON() (UsageSnapshot, bool) {
	reader, ok := e.jsonReader()
	if !ok {
		return UsageSnapshot{}, false
	}
	switch e.mode {
	case usageModeOpenAIJSON:
		var payload struct {
			Usage map[string]any `json:"usage"`
		}
		if err := json.NewDecoder(reader).Decode(&payload); err != nil {
			return UsageSnapshot{}, false
		}
		return snapshotFromKnownUsageObject(payload.Usage, normalizeOpenAIUsage)
	case usageModeClaudeJSON:
		var payload struct {
			Usage   map[string]any `json:"usage"`
			Message struct {
				Usage map[string]any `json:"usage"`
			} `json:"message"`
		}
		if err := json.NewDecoder(reader).Decode(&payload); err != nil {
			return UsageSnapshot{}, false
		}
		if payload.Usage != nil {
			return snapshotFromKnownUsageObject(payload.Usage, normalizeClaudeUsage)
		}
		return snapshotFromKnownUsageObject(payload.Message.Usage, normalizeClaudeUsage)
	case usageModeGeminiJSON:
		var payload struct {
			UsageMetadata map[string]any `json:"usageMetadata"`
		}
		if err := json.NewDecoder(reader).Decode(&payload); err != nil {
			return UsageSnapshot{}, false
		}
		return snapshotFromKnownUsageObject(payload.UsageMetadata, normalizeGeminiUsage)
	default:
		return UsageSnapshot{}, false
	}
}

func (e *UsageExtractor) Cleanup() {
	if e == nil || e.jsonFile == nil {
		return
	}
	name := e.jsonFile.Name()
	_ = e.jsonFile.Close()
	e.jsonFile = nil
	if name != "" {
		_ = os.Remove(name)
	}
}

func (e *UsageExtractor) appendJSON(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if e.jsonFile == nil && len(e.jsonCapture)+len(chunk) <= maxJSONCaptureBytes {
		e.jsonCapture = append(e.jsonCapture, chunk...)
		return
	}
	if e.jsonFile == nil {
		file, err := os.CreateTemp("", "clipal-usage-*")
		if err != nil {
			return
		}
		if len(e.jsonCapture) > 0 {
			if _, err := file.Write(e.jsonCapture); err != nil {
				_ = file.Close()
				_ = os.Remove(file.Name())
				return
			}
			e.jsonCapture = nil
		}
		e.jsonFile = file
	}
	_, _ = e.jsonFile.Write(chunk)
}

func (e *UsageExtractor) jsonReader() (io.Reader, bool) {
	if e == nil {
		return nil, false
	}
	if e.jsonFile != nil {
		if _, err := e.jsonFile.Seek(0, io.SeekStart); err != nil {
			return nil, false
		}
		return e.jsonFile, true
	}
	if len(e.jsonCapture) == 0 {
		return nil, false
	}
	return bytes.NewReader(e.jsonCapture), true
}

func (e *UsageExtractor) appendSSE(chunk []byte) {
	consumed := 0
	if e.pendingCR {
		e.pendingCR = false
		if len(chunk) > 0 && chunk[0] == '\n' {
			consumed++
		}
		e.completeSSELine()
	}
	for consumed < len(chunk) {
		lineEnd := bytes.IndexAny(chunk[consumed:], "\r\n")
		if lineEnd < 0 {
			e.appendSSELineFragment(chunk[consumed:])
			return
		}
		lineEnd += consumed
		e.appendSSELineFragment(chunk[consumed:lineEnd])
		lineEndingBytes := 1
		if chunk[lineEnd] == '\r' {
			if lineEnd+1 < len(chunk) && chunk[lineEnd+1] == '\n' {
				lineEndingBytes = 2
			} else if lineEnd+1 == len(chunk) {
				e.pendingCR = true
				return
			}
		}
		consumed = lineEnd + lineEndingBytes
		e.completeSSELine()
	}
}

func (e *UsageExtractor) completeSSELine() {
	line := e.lineBuf
	e.processSSELine(line, e.lineOverflow)
	e.lineBuf = e.lineBuf[:0]
	e.lineOverflow = false
}

func (e *UsageExtractor) appendSSELineFragment(fragment []byte) {
	if len(fragment) == 0 || e.lineOverflow {
		return
	}
	remaining := maxSSEEventCaptureBytes - len(e.lineBuf)
	if remaining <= 0 {
		e.lineOverflow = true
		return
	}
	if len(fragment) > remaining {
		e.lineBuf = append(e.lineBuf, fragment[:remaining]...)
		e.lineOverflow = true
		return
	}
	e.lineBuf = append(e.lineBuf, fragment...)
}

func (e *UsageExtractor) processSSELine(line []byte, overflow bool) {
	if len(line) == 0 {
		e.flushPendingEvent()
		return
	}
	switch {
	case bytes.HasPrefix(line, []byte("event:")):
		e.eventName = strings.TrimSpace(string(bytes.TrimPrefix(line, []byte("event:"))))
	case bytes.HasPrefix(line, []byte("data:")):
		if overflow {
			e.eventOversized = true
			e.eventData = nil
			return
		}
		data := bytes.TrimPrefix(line, []byte("data:"))
		data = bytes.TrimPrefix(data, []byte{' '})
		extra := len(data)
		if len(e.eventData) > 0 {
			extra++
		}
		if e.eventOversized || len(e.eventData)+extra > maxSSEEventCaptureBytes {
			e.eventOversized = true
			e.eventData = nil
			return
		}
		if len(e.eventData) > 0 {
			e.eventData = append(e.eventData, '\n')
		}
		e.eventData = append(e.eventData, data...)
	}
}

func (e *UsageExtractor) flushPendingEvent() {
	e.markCompletedEventName(e.eventName)
	if e.eventOversized {
		e.eventName = ""
		e.eventData = nil
		e.eventOversized = false
		return
	}
	if len(e.eventData) == 0 {
		e.eventName = ""
		return
	}

	data := e.eventData
	e.eventData = nil
	eventName := e.eventName
	e.eventName = ""

	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return
	}
	payload, ok := decodeJSONObject(data)
	if !ok {
		return
	}

	switch e.mode {
	case usageModeOpenAISSE:
		e.handleOpenAISSEEvent(eventName, payload)
	case usageModeClaudeSSE:
		e.handleClaudeSSEEvent(eventName, payload)
	case usageModeGeminiSSE:
		e.handleGeminiSSEEvent(payload)
	}
}

func (e *UsageExtractor) markCompletedEventName(eventName string) {
	switch e.mode {
	case usageModeOpenAISSE:
		if eventName == "response.completed" || eventName == "image_generation.completed" {
			e.completed = true
		}
	case usageModeClaudeSSE:
		if eventName == "message_stop" {
			e.completed = true
		}
	}
}

func (e *UsageExtractor) handleOpenAISSEEvent(eventName string, payload map[string]any) {
	eventType := strings.TrimSpace(stringValue(payload["type"]))
	if eventName == "response.completed" || eventType == "response.completed" {
		e.completed = true
		if snapshot, ok := snapshotFromKnownUsageObject(nestedMap(payload, "response", "usage"), normalizeOpenAIUsage); ok {
			e.snapshot = snapshot
			e.found = true
			return
		}
		if snapshot, ok := snapshotFromKnownUsageObject(nestedMap(payload, "usage"), normalizeOpenAIUsage); ok {
			e.snapshot = snapshot
			e.found = true
		}
		return
	}
	if snapshot, ok := snapshotFromKnownUsageObject(nestedMap(payload, "usage"), normalizeOpenAIUsage); ok {
		// Chat/completions streams with include_usage emit a final chunk carrying
		// top-level usage just before [DONE].
		e.snapshot = snapshot
		e.found = true
		e.completed = true
	}
}

func (e *UsageExtractor) handleClaudeSSEEvent(eventName string, payload map[string]any) {
	eventType := strings.TrimSpace(stringValue(payload["type"]))
	name := eventName
	if name == "" {
		name = eventType
	}

	switch name {
	case "message_start":
		if raw := nestedMap(payload, "message", "usage"); raw != nil {
			e.snapshot = mergeUsageObject(e.snapshot, raw, normalizeClaudeUsage)
			e.found = true
		}
	case "message_delta":
		if raw := nestedMap(payload, "usage"); raw != nil {
			e.snapshot = mergeUsageObject(e.snapshot, raw, normalizeClaudeUsage)
			e.found = true
		}
	case "message_stop":
		e.completed = true
	}
}

func (e *UsageExtractor) handleGeminiSSEEvent(payload map[string]any) {
	if snapshot, ok := snapshotFromKnownUsageObject(nestedMap(payload, "usageMetadata"), normalizeGeminiUsage); ok {
		e.snapshot = snapshot
		e.found = true
	}
	if hasGeminiFinishReason(payload) {
		e.completed = true
	}
}

func hasGeminiFinishReason(payload map[string]any) bool {
	candidates, ok := payload["candidates"].([]any)
	if !ok {
		return false
	}
	for _, candidate := range candidates {
		candidateMap, ok := candidate.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringValue(candidateMap["finishReason"])) != "" {
			return true
		}
	}
	return false
}

func mergeUsageObject(current UsageSnapshot, raw map[string]any, normalize func(map[string]any) UsageDelta) UsageSnapshot {
	if current.Usage == nil {
		current.Usage = map[string]any{}
	}
	for key, value := range raw {
		current.Usage[key] = value
	}
	current.UsageDelta = normalize(current.Usage).normalized()
	return current
}

func snapshotFromKnownUsageObject(raw map[string]any, normalize func(map[string]any) UsageDelta) (UsageSnapshot, bool) {
	if raw == nil {
		return UsageSnapshot{}, false
	}
	usage := cloneMap(raw)
	if usage == nil {
		return UsageSnapshot{}, false
	}
	outputTokenDetails := nestedMap(usage, "output_tokens_details")
	if outputTokenDetails == nil {
		outputTokenDetails = nestedMap(usage, "completion_tokens_details")
	}
	return UsageSnapshot{
		UsageDelta:      normalize(usage).normalized(),
		Usage:           usage,
		ReasoningTokens: int64Value(outputTokenDetails["reasoning_tokens"]),
		ThoughtsTokens:  int64Value(usage["thoughtsTokenCount"]),
	}, true
}

func normalizeOpenAIUsage(raw map[string]any) UsageDelta {
	return UsageDelta{
		InputTokens:  int64Value(raw["prompt_tokens"], raw["input_tokens"]),
		OutputTokens: int64Value(raw["completion_tokens"], raw["output_tokens"]),
		TotalTokens:  int64Value(raw["total_tokens"]),
	}.normalized()
}

func normalizeClaudeUsage(raw map[string]any) UsageDelta {
	inputTokens := int64Value(raw["input_tokens"])
	cacheCreationTokens := int64Value(raw["cache_creation_input_tokens"])
	cacheReadTokens := int64Value(raw["cache_read_input_tokens"])
	return UsageDelta{
		InputTokens:  inputTokens + cacheCreationTokens + cacheReadTokens,
		OutputTokens: int64Value(raw["output_tokens"]),
		TotalTokens:  int64Value(raw["total_tokens"]),
	}.normalized()
}

func normalizeGeminiUsage(raw map[string]any) UsageDelta {
	outputTokens := int64Value(raw["candidatesTokenCount"], raw["responseTokenCount"])
	return UsageDelta{
		InputTokens:  int64Value(raw["promptTokenCount"]) + int64Value(raw["toolUsePromptTokenCount"]),
		OutputTokens: outputTokens,
		TotalTokens:  int64Value(raw["totalTokenCount"]),
	}.normalized()
}

func decodeJSONObject(data []byte) (map[string]any, bool) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, false
	}
	obj, ok := decoded.(map[string]any)
	return obj, ok
}

func nestedMap(root map[string]any, path ...string) map[string]any {
	current := root
	for _, part := range path {
		if current == nil {
			return nil
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func int64Value(values ...any) int64 {
	for _, value := range values {
		switch n := value.(type) {
		case float64:
			return int64(n)
		case float32:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		case int32:
			return int64(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return i
			}
		case string:
			if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
				return i
			}
		}
	}
	return 0
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
