package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/logger"
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
	protocolIncomplete    protocolStatus = "incomplete"
	protocolInProgress    protocolStatus = "in_progress_only"
)

type streamProtocolKind string

const (
	streamProtocolNone   streamProtocolKind = "none"
	streamProtocolOpenAI streamProtocolKind = "openai_sse"
	streamProtocolClaude streamProtocolKind = "claude_sse"
)

const protocolScanWindow = 64 * 1024

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

	sawAnyChunk      bool
	sawCompleteEvent bool
	tail             []byte
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
			return &protocolTracker{kind: streamProtocolOpenAI}
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

func (pt *protocolTracker) append(chunk []byte) {
	if pt == nil || pt.kind == streamProtocolNone || len(chunk) == 0 {
		return
	}

	pt.sawAnyChunk = true
	pt.tail = append(pt.tail, chunk...)
	if len(pt.tail) > protocolScanWindow {
		pt.tail = pt.tail[len(pt.tail)-protocolScanWindow:]
	}

	lower := bytes.ToLower(pt.tail)
	switch pt.kind {
	case streamProtocolOpenAI:
		pt.sawCompleteEvent = bytes.Contains(lower, []byte("data: [done]")) ||
			bytes.Contains(lower, []byte("event: response.completed")) ||
			bytes.Contains(lower, []byte(`"type":"response.completed"`)) ||
			bytes.Contains(lower, []byte(`"type": "response.completed"`))
	case streamProtocolClaude:
		pt.sawCompleteEvent = bytes.Contains(lower, []byte("event: message_stop")) ||
			bytes.Contains(lower, []byte(`"type":"message_stop"`)) ||
			bytes.Contains(lower, []byte(`"type": "message_stop"`))
	}
}

func (pt *protocolTracker) finalStatus() protocolStatus {
	if pt == nil || pt.kind == streamProtocolNone {
		return protocolNotApplicable
	}
	if pt.sawCompleteEvent {
		return protocolCompleted
	}
	return protocolIncomplete
}

func (pt *protocolTracker) abortedStatus() protocolStatus {
	if pt == nil || pt.kind == streamProtocolNone {
		return protocolNotApplicable
	}
	if pt.sawCompleteEvent {
		return protocolCompleted
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
