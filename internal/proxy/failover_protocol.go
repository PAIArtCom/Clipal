package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/logger"
)

type deliveryStatus string

const (
	deliveryCommittedComplete deliveryStatus = "committed_complete"
	deliveryCommittedPartial  deliveryStatus = "committed_partial"
	deliveryClientCanceled    deliveryStatus = "client_canceled"
	deliveryRetryNext         deliveryStatus = "retry_next"
)

type protocolStatus string

const (
	protocolNotApplicable protocolStatus = "not_applicable"
	protocolCompleted     protocolStatus = "completed"
	protocolFailed        protocolStatus = "failed"
	protocolIncomplete    protocolStatus = "incomplete"
	protocolInProgress    protocolStatus = "in_progress_only"
)

type streamProtocolKind string

const (
	streamProtocolNone            streamProtocolKind = "none"
	streamProtocolOpenAI          streamProtocolKind = "openai_sse"
	streamProtocolOpenAIResponses streamProtocolKind = "openai_responses_sse"
	streamProtocolOpenAIChat      streamProtocolKind = "openai_chat_sse"
	streamProtocolOpenAIImages    streamProtocolKind = "openai_images_sse"
	streamProtocolClaude          streamProtocolKind = "claude_sse"
)

const protocolScanWindow = 64 * 1024

type terminalEventKind uint8

const (
	terminalEventNone terminalEventKind = iota
	terminalEventSuccess
	terminalEventFailed
	terminalEventIncomplete
)

const maxProtocolJSONTokenBytes = 256

type jsonStringRole uint8

const (
	jsonStringOther jsonStringRole = iota
	jsonStringKey
	jsonStringTypeValue
)

// topLevelJSONTypeScanner extracts a top-level string field named "type"
// while consuming JSON incrementally. It keeps only the current key/type token;
// large string values such as Base64 image data are scanned but never retained.
type topLevelJSONTypeScanner struct {
	depth      int
	expectKey  bool
	currentKey string
	sawColon   bool

	inString      bool
	escaped       bool
	stringRole    jsonStringRole
	stringBuf     [maxProtocolJSONTokenBytes]byte
	stringLen     int
	stringTooLong bool

	eventType string
}

func (s *topLevelJSONTypeScanner) appendByte(b byte) {
	if s.eventType != "" {
		return
	}
	if s.inString {
		s.appendStringByte(b)
		return
	}
	switch b {
	case '"':
		s.inString = true
		s.escaped = false
		s.stringLen = 0
		s.stringTooLong = false
		s.stringRole = jsonStringOther
		if s.depth == 1 {
			switch {
			case s.expectKey:
				s.stringRole = jsonStringKey
			case s.sawColon && s.currentKey == "type":
				s.stringRole = jsonStringTypeValue
			}
		}
	case '{':
		s.depth++
		if s.depth == 1 {
			s.expectKey = true
			s.currentKey = ""
			s.sawColon = false
		}
	case '[':
		s.depth++
	case '}', ']':
		if s.depth > 0 {
			s.depth--
		}
	case ':':
		if s.depth == 1 && !s.expectKey && s.currentKey != "" {
			s.sawColon = true
		}
	case ',':
		if s.depth == 1 {
			s.expectKey = true
			s.currentKey = ""
			s.sawColon = false
		}
	}
}

func (s *topLevelJSONTypeScanner) appendStringByte(b byte) {
	if s.escaped {
		s.captureStringByte(b)
		s.escaped = false
		return
	}
	if b == '\\' {
		s.captureStringByte(b)
		s.escaped = true
		return
	}
	if b != '"' {
		s.captureStringByte(b)
		return
	}

	s.inString = false
	decoded, ok := s.decodedString()
	if !ok {
		return
	}
	switch s.stringRole {
	case jsonStringKey:
		s.currentKey = decoded
		s.expectKey = false
		s.sawColon = false
	case jsonStringTypeValue:
		s.eventType = decoded
	}
}

func (s *topLevelJSONTypeScanner) captureStringByte(b byte) {
	if s.stringRole == jsonStringOther || s.stringTooLong {
		return
	}
	if s.stringLen >= len(s.stringBuf) {
		s.stringTooLong = true
		return
	}
	s.stringBuf[s.stringLen] = b
	s.stringLen++
}

func (s *topLevelJSONTypeScanner) decodedString() (string, bool) {
	if s.stringRole == jsonStringOther || s.stringTooLong {
		return "", false
	}
	raw := make([]byte, s.stringLen+2)
	raw[0] = '"'
	copy(raw[1:], s.stringBuf[:s.stringLen])
	raw[len(raw)-1] = '"'
	var decoded string
	if json.Unmarshal(raw, &decoded) != nil {
		return "", false
	}
	return decoded, true
}

type sseDataTypeScanner struct {
	prefixPos     int
	invalid       bool
	optionalSpace bool
	json          topLevelJSONTypeScanner
}

func (s *sseDataTypeScanner) append(fragment []byte) {
	const prefix = "data:"
	for _, b := range fragment {
		if s.invalid || s.json.eventType != "" {
			return
		}
		if s.prefixPos < len(prefix) {
			if b != prefix[s.prefixPos] {
				s.invalid = true
				return
			}
			s.prefixPos++
			if s.prefixPos == len(prefix) {
				s.optionalSpace = true
			}
			continue
		}
		if s.optionalSpace {
			s.optionalSpace = false
			if b == ' ' {
				continue
			}
		}
		s.json.appendByte(b)
	}
}

func (s *sseDataTypeScanner) eventType() string {
	return s.json.eventType
}

func isEventStreamContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream")
}

func hasNonIdentityContentEncoding(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	encoding := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	return encoding != "" && encoding != "identity"
}

func looksLikeSSEPrelude(chunk []byte) bool {
	trimmed := bytes.TrimLeft(chunk, " \t\r\n")
	if len(trimmed) == 0 {
		return false
	}
	lower := bytes.ToLower(trimmed)
	return bytes.HasPrefix(lower, []byte("event:")) || bytes.HasPrefix(lower, []byte("data:"))
}

func inferredResponseContentType(req *http.Request, resp *http.Response, firstChunk []byte) string {
	if resp == nil {
		return ""
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if isEventStreamContentType(contentType) || !looksLikeSSEPrelude(firstChunk) {
		return contentType
	}
	requestCtx, ok := requestContextFromRequest(req)
	if !ok {
		return contentType
	}
	switch requestCtx.Family {
	case ProtocolFamilyOpenAI, ProtocolFamilyClaude, ProtocolFamilyGemini:
		return "text/event-stream; charset=utf-8"
	default:
		return contentType
	}
}

type protocolTracker struct {
	kind streamProtocolKind

	sawAnyChunk bool
	lineBuf     []byte
	lineTooLong bool
	pendingCR   bool
	dataType    sseDataTypeScanner
	eventName   string
	eventResult terminalEventKind
	terminal    terminalEventKind
	terminalEnd bool
}

func newProtocolTracker(clientType ClientType, req *http.Request, resp *http.Response) *protocolTracker {
	if resp == nil {
		return &protocolTracker{kind: streamProtocolNone}
	}
	if hasNonIdentityContentEncoding(resp) {
		return &protocolTracker{kind: streamProtocolNone}
	}
	return newProtocolTrackerWithContentType(clientType, req, resp.Header.Get("Content-Type"))
}

func newProtocolTrackerWithContentType(clientType ClientType, req *http.Request, contentType string) *protocolTracker {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !isEventStreamContentType(contentType) {
		return &protocolTracker{kind: streamProtocolNone}
	}

	if requestCtx, ok := requestContextFromRequest(req); ok {
		switch requestCtx.Family {
		case ProtocolFamilyOpenAI:
			switch requestCtx.Capability {
			case CapabilityOpenAIResponses:
				return &protocolTracker{kind: streamProtocolOpenAIResponses}
			case CapabilityOpenAIChatCompletions, CapabilityOpenAICompletions:
				return &protocolTracker{kind: streamProtocolOpenAIChat}
			case CapabilityOpenAIImages:
				return &protocolTracker{kind: streamProtocolOpenAIImages}
			case CapabilityOpenAICompatible:
				return &protocolTracker{kind: streamProtocolOpenAI}
			default:
				return &protocolTracker{kind: streamProtocolNone}
			}
		case ProtocolFamilyClaude:
			return &protocolTracker{kind: streamProtocolClaude}
		default:
			return &protocolTracker{kind: streamProtocolNone}
		}
	}

	switch clientType {
	case ClientOpenAI:
		return &protocolTracker{kind: streamProtocolOpenAI}
	case ClientClaude:
		return &protocolTracker{kind: streamProtocolClaude}
	default:
		// Gemini and other unknown streaming formats should not be forced into
		// a completion-marker contract we can't validate reliably yet.
		return &protocolTracker{kind: streamProtocolNone}
	}
}

// append consumes an SSE chunk and returns the prefix that belongs to the
// response. When it reports terminal=true, forward exactly chunk[:forward],
// flush it, and stop reading upstream. This waits for the blank line ending the
// terminal event, so large payloads in that event are never truncated.
func (pt *protocolTracker) append(chunk []byte) (forward int, terminal bool) {
	if len(chunk) == 0 {
		return 0, pt != nil && pt.terminalEnd
	}
	if pt == nil || pt.kind == streamProtocolNone {
		return len(chunk), false
	}
	if pt.terminalEnd {
		return 0, true
	}

	pt.sawAnyChunk = true
	consumed := 0
	if pt.pendingCR {
		pt.pendingCR = false
		if chunk[0] == '\n' {
			consumed++
		}
		if pt.completeLine() {
			return consumed, true
		}
	}
	for consumed < len(chunk) {
		lineEnd := bytes.IndexAny(chunk[consumed:], "\r\n")
		if lineEnd < 0 {
			pt.appendLineFragment(chunk[consumed:])
			return len(chunk), false
		}

		lineEnd += consumed
		pt.appendLineFragment(chunk[consumed:lineEnd])
		lineEndingBytes := 1
		if chunk[lineEnd] == '\r' {
			if lineEnd+1 < len(chunk) && chunk[lineEnd+1] == '\n' {
				lineEndingBytes = 2
			} else if lineEnd+1 == len(chunk) {
				pt.pendingCR = true
				return len(chunk), false
			}
		}
		consumed = lineEnd + lineEndingBytes
		if pt.completeLine() {
			return consumed, true
		}
	}
	return len(chunk), false
}

func (pt *protocolTracker) completeLine() bool {
	line := pt.lineBuf
	pt.processLine(line, pt.dataType.eventType())
	pt.lineBuf = pt.lineBuf[:0]
	pt.lineTooLong = false
	pt.dataType = sseDataTypeScanner{}
	if len(line) == 0 {
		pt.finishEvent()
	}
	return pt.terminalEnd
}

func (pt *protocolTracker) appendLineFragment(fragment []byte) {
	if len(fragment) == 0 {
		return
	}
	pt.dataType.append(fragment)
	if pt.lineTooLong {
		return
	}
	remaining := protocolScanWindow - len(pt.lineBuf)
	if remaining <= 0 {
		pt.lineTooLong = true
		return
	}
	if len(fragment) > remaining {
		pt.lineBuf = append(pt.lineBuf, fragment[:remaining]...)
		pt.lineTooLong = true
		return
	}
	pt.lineBuf = append(pt.lineBuf, fragment...)
}

func (pt *protocolTracker) processLine(line []byte, scannedEventType string) {
	if len(line) == 0 || line[0] == ':' {
		return
	}
	field, value, ok := bytes.Cut(line, []byte{':'})
	if !ok {
		return
	}
	value = bytes.TrimPrefix(value, []byte{' '})
	switch string(field) {
	case "event":
		pt.eventName = strings.TrimSpace(string(value))
		pt.recordEventType(pt.eventName)
	case "data":
		if bytes.Equal(bytes.TrimSpace(value), []byte("[DONE]")) {
			pt.recordEventType("[DONE]")
			return
		}
		pt.recordEventType(scannedEventType)
	}
}

func (pt *protocolTracker) recordEventType(eventType string) {
	var result terminalEventKind
	switch pt.kind {
	case streamProtocolOpenAIResponses:
		switch eventType {
		case "response.completed":
			result = terminalEventSuccess
		case "response.failed":
			result = terminalEventFailed
		case "response.incomplete":
			result = terminalEventIncomplete
		}
	case streamProtocolOpenAIChat:
		if eventType == "[DONE]" {
			result = terminalEventSuccess
		}
	case streamProtocolOpenAIImages:
		if eventType == "image_generation.completed" {
			result = terminalEventSuccess
		}
	case streamProtocolOpenAI:
		switch eventType {
		case "response.completed", "image_generation.completed", "[DONE]":
			result = terminalEventSuccess
		case "response.failed":
			result = terminalEventFailed
		case "response.incomplete":
			result = terminalEventIncomplete
		}
	case streamProtocolClaude:
		switch eventType {
		case "message_stop":
			result = terminalEventSuccess
		case "error":
			result = terminalEventFailed
		}
	}
	if result != terminalEventNone && pt.eventResult == terminalEventNone {
		pt.eventResult = result
	}
}

func (pt *protocolTracker) finishEvent() {
	if pt.terminal == terminalEventNone && pt.eventResult != terminalEventNone {
		pt.terminal = pt.eventResult
		pt.terminalEnd = true
	}
	pt.eventName = ""
	pt.eventResult = terminalEventNone
}

func (pt *protocolTracker) finishEOF() {
	if pt == nil || pt.kind == streamProtocolNone || pt.terminalEnd {
		return
	}
	if pt.pendingCR {
		pt.pendingCR = false
		pt.completeLine()
	}
	if len(pt.lineBuf) > 0 {
		line := pt.lineBuf
		pt.processLine(line, pt.dataType.eventType())
		pt.lineBuf = pt.lineBuf[:0]
		pt.lineTooLong = false
		pt.dataType = sseDataTypeScanner{}
	}
	// EOF is also a safe response boundary for providers that omit the final
	// blank SSE line. Preserve that common compatibility behavior.
	pt.finishEvent()
}

func (pt *protocolTracker) hasTerminalEvent() bool {
	return pt != nil && pt.terminalEnd
}

func (pt *protocolTracker) finalStatus() protocolStatus {
	if pt == nil || pt.kind == streamProtocolNone {
		return protocolNotApplicable
	}
	switch pt.terminal {
	case terminalEventSuccess:
		return protocolCompleted
	case terminalEventFailed:
		return protocolFailed
	case terminalEventIncomplete:
		return protocolIncomplete
	}
	return protocolIncomplete
}

func (pt *protocolTracker) abortedStatus() protocolStatus {
	if pt == nil || pt.kind == streamProtocolNone {
		return protocolNotApplicable
	}
	if pt.terminalEnd {
		return pt.finalStatus()
	}
	if pt.sawAnyChunk {
		return protocolInProgress
	}
	return protocolIncomplete
}

func (cp *ClientProxy) logRequestResult(req *http.Request, providerName string, statusCode int, result streamResult, manual bool) {
	now := time.Now()
	cp.recordLastRequest(now, req, providerName, statusCode, result)
	presentation := DescribeRequestOutcome(RequestOutcomeEvent{
		At:       now,
		Provider: providerName,
		Status:   statusCode,
		Delivery: string(result.delivery),
		Protocol: string(result.protocol),
		Cause:    result.cause,
		Bytes:    result.bytes,
	})

	suffix := ""
	if manual {
		suffix = " (manual)"
	}

	detail := presentation.Detail
	if result.bytes > 0 {
		detail = detail + fmt.Sprintf(" Bytes=%d.", result.bytes)
	}

	switch presentation.Result {
	case "completed":
		logger.Info("[%s] %s%s", cp.clientType, presentation.Label, suffix)
	case "client_canceled":
		logger.Debug("[%s] %s%s. %s", cp.clientType, presentation.Label, suffix, detail)
	default:
		logger.Warn("[%s] %s%s. %s", cp.clientType, presentation.Label, suffix, detail)
	}
}
