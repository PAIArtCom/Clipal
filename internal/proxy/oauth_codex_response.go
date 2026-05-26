package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func shouldSynthesizeCodexOAuthNonStreamingResponse(original *http.Request, provider config.Provider, path string, payload *requestPayload) bool {
	if original == nil || provider.NormalizedOAuthProvider() != config.OAuthProviderCodex {
		return false
	}
	requestCtx, ok := requestContextFromRequest(original)
	if !ok || requestCtx.Capability != CapabilityOpenAIResponses {
		return false
	}
	if !matchesExactPath(normalizeUpstreamPath(path), "/v1/responses") && !matchesExactPath(normalizeUpstreamPath(requestCtx.UpstreamPath), "/v1/responses") {
		return false
	}
	if payload == nil {
		return true
	}
	root := payload.jsonRoot()
	if root == nil {
		return true
	}
	stream, _ := root["stream"].(bool)
	return !stream
}

func (cp *ClientProxy) synthesizeCodexOAuthNonStreamingResponseToClient(w http.ResponseWriter, resp *http.Response, originalReq *http.Request, attemptCtx context.Context, cancelAttempt context.CancelCauseFunc, index int, allow circuitAllowResult, onCommit func(), onSuccess func(streamSuccess)) streamResult {
	if resp == nil || resp.Body == nil {
		err := errors.New("upstream response missing body")
		return streamResult{
			kind:     streamRetryNext,
			delivery: deliveryRetryNext,
			protocol: protocolNotApplicable,
			proto:    streamProtocolOpenAI,
			cause:    "network",
			err:      err,
		}
	}

	body, readErr := cp.readCodexOAuthSSEBody(resp.Body, cancelAttempt)
	_ = resp.Body.Close()
	if readErr != nil {
		if originalReq != nil && originalReq.Context().Err() != nil {
			cp.releaseCircuitPermit(index, allow.usedProbe)
			return streamResult{
				kind:     streamFinal,
				delivery: deliveryClientCanceled,
				protocol: protocolInProgress,
				proto:    streamProtocolOpenAI,
				cause:    "client_canceled",
				err:      originalReq.Context().Err(),
			}
		}
		cause := "network"
		if isUpstreamIdleTimeout(attemptCtx, readErr) || isUpstreamIdleTimeout(attemptCtx, attemptCtx.Err()) {
			cause = "idle_timeout"
		}
		return streamResult{
			kind:     streamRetryNext,
			delivery: deliveryRetryNext,
			protocol: protocolInProgress,
			proto:    streamProtocolOpenAI,
			cause:    cause,
			err:      readErr,
		}
	}

	responseBody, completed, err := synthesizeCodexOAuthResponsesJSON(body)
	if err != nil || !completed {
		if err == nil {
			err = errors.New("codex oauth response stream ended before completion")
		}
		return streamResult{
			kind:     streamRetryNext,
			delivery: deliveryRetryNext,
			protocol: protocolIncomplete,
			proto:    streamProtocolOpenAI,
			cause:    "protocol_incomplete",
			err:      err,
		}
	}

	if onCommit != nil {
		onCommit()
	}
	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Del("Cache-Control")
	w.Header().Del("Content-Encoding")
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	n, writeErr := w.Write(responseBody)
	if writeErr != nil {
		cp.releaseCircuitPermit(index, allow.usedProbe)
		return streamResult{
			kind:     streamFinal,
			delivery: deliveryClientCanceled,
			protocol: protocolCompleted,
			proto:    streamProtocolOpenAI,
			cause:    "client_canceled",
			bytes:    n,
			err:      writeErr,
		}
	}

	if onSuccess != nil {
		usageExtractor := usageExtractorFromRequestWithContentType(originalReq, "application/json")
		if usageExtractor != nil {
			usageExtractor.Append(responseBody)
			onSuccess(buildStreamSuccess(responseBody, usageExtractor))
			usageExtractor.Cleanup()
		} else {
			onSuccess(streamSuccess{responseBody: responseBody})
		}
	}
	cp.recordCircuitSuccess(time.Now(), index, allow.usedProbe)
	return streamResult{
		kind:     streamFinal,
		delivery: deliveryCommittedComplete,
		protocol: protocolCompleted,
		proto:    streamProtocolOpenAI,
		bytes:    n,
	}
}

func (cp *ClientProxy) readCodexOAuthSSEBody(body io.Reader, cancelAttempt context.CancelCauseFunc) ([]byte, error) {
	var idleTimer *time.Timer
	if cp != nil && cp.upstreamIdle > 0 {
		idleTimer = time.AfterFunc(cp.upstreamIdle, func() { cancelAttempt(errUpstreamIdleTimeout) })
		defer stopTimer(idleTimer)
	}

	var out bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if idleTimer != nil {
				idleTimer.Reset(cp.upstreamIdle)
			}
			_, _ = out.Write(buf[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out.Bytes(), nil
			}
			return out.Bytes(), err
		}
	}
}

func synthesizeCodexOAuthResponsesJSON(streamBody []byte) ([]byte, bool, error) {
	trimmedBody := bytes.TrimSpace(streamBody)
	if len(trimmedBody) > 0 && trimmedBody[0] == '{' {
		var root map[string]any
		if err := json.Unmarshal(trimmedBody, &root); err != nil {
			return nil, false, fmt.Errorf("decode codex oauth json response: %w", err)
		}
		return trimmedBody, true, nil
	}

	events := parseSSEEvents(streamBody)
	if len(events) == 0 {
		return nil, false, errors.New("codex oauth response stream contained no events")
	}

	var created map[string]any
	var completed map[string]any
	var terminal map[string]any
	var text strings.Builder
	outputItems := make([]any, 0)
	sawComplete := false

	for _, event := range events {
		data := strings.TrimSpace(event.data)
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			continue
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(data), &root); err != nil {
			return nil, false, fmt.Errorf("decode codex oauth response stream event: %w", err)
		}
		eventType := strings.TrimSpace(asString(root["type"]))
		if eventType == "" {
			eventType = event.name
		}
		if response, ok := root["response"].(map[string]any); ok {
			switch eventType {
			case "response.created":
				created = cloneStringAnyMap(response)
			case "response.completed":
				completed = cloneStringAnyMap(response)
				sawComplete = true
			case "response.failed", "response.incomplete":
				terminal = cloneStringAnyMap(response)
				sawComplete = true
			}
		}
		switch eventType {
		case "response.output_text.delta":
			_, _ = text.WriteString(asString(root["delta"]))
		case "response.output_text.done":
			if doneText := asString(root["text"]); doneText != "" {
				text.Reset()
				_, _ = text.WriteString(doneText)
			}
		case "response.output_item.done":
			if item, ok := root["item"]; ok {
				outputItems = append(outputItems, item)
			}
		case "response.failed", "response.incomplete":
			if terminal == nil {
				return nil, false, codexOAuthResponseStreamError(eventType, root)
			}
		case "response.completed":
			sawComplete = true
		}
	}

	response := completed
	if response == nil {
		response = terminal
	}
	if response == nil {
		response = created
	}
	if response == nil {
		response = map[string]any{
			"id":         "resp_" + strings.ReplaceAll(newCodexUUID(), "-", ""),
			"object":     "response",
			"created_at": time.Now().Unix(),
		}
	}
	if _, ok := response["status"]; !ok {
		response["status"] = "completed"
	}
	if codexOAuthResponseOutputEmpty(response) {
		if len(outputItems) > 0 {
			response["output"] = outputItems
		} else if text.Len() > 0 {
			response["output"] = []any{
				map[string]any{
					"id":     "msg_" + strings.ReplaceAll(newCodexUUID(), "-", ""),
					"type":   "message",
					"status": "completed",
					"role":   "assistant",
					"content": []any{
						map[string]any{
							"type":        "output_text",
							"text":        text.String(),
							"annotations": []any{},
						},
					},
				},
			}
		} else {
			response["output"] = []any{}
		}
	}
	if _, ok := response["object"]; !ok {
		response["object"] = "response"
	}

	body, err := json.Marshal(response)
	if err != nil {
		return nil, false, fmt.Errorf("marshal synthesized codex oauth response: %w", err)
	}
	return body, sawComplete, nil
}

func codexOAuthResponseOutputEmpty(response map[string]any) bool {
	if response == nil {
		return true
	}
	output, ok := response["output"]
	if !ok || output == nil {
		return true
	}
	items, ok := output.([]any)
	return ok && len(items) == 0
}

func codexOAuthResponseStreamError(eventType string, root map[string]any) error {
	message := strings.TrimSpace(eventType)
	response, _ := root["response"].(map[string]any)
	if response == nil {
		return errors.New(message)
	}
	if details, _ := response["incomplete_details"].(map[string]any); details != nil {
		if reason := strings.TrimSpace(asString(details["reason"])); reason != "" {
			return fmt.Errorf("%s: %s", message, reason)
		}
	}
	if upstreamErr, _ := response["error"].(map[string]any); upstreamErr != nil {
		code := strings.TrimSpace(asString(upstreamErr["code"]))
		text := strings.TrimSpace(asString(upstreamErr["message"]))
		switch {
		case code != "" && text != "":
			return fmt.Errorf("%s: %s: %s", message, code, text)
		case code != "":
			return fmt.Errorf("%s: %s", message, code)
		case text != "":
			return fmt.Errorf("%s: %s", message, text)
		}
	}
	return errors.New(message)
}

type sseEvent struct {
	name string
	data string
}

func parseSSEEvents(body []byte) []sseEvent {
	body = bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
	body = bytes.ReplaceAll(body, []byte("\r"), []byte("\n"))

	blocks := bytes.Split(body, []byte("\n\n"))
	events := make([]sseEvent, 0, len(blocks))
	for _, block := range blocks {
		lines := bytes.Split(block, []byte("\n"))
		var event sseEvent
		dataLines := make([]string, 0, len(lines))
		for _, line := range lines {
			line = bytes.TrimRight(line, " \t")
			if len(line) == 0 || bytes.HasPrefix(line, []byte(":")) {
				continue
			}
			field, value, ok := bytes.Cut(line, []byte(":"))
			if !ok {
				field = line
				value = nil
			} else if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			switch string(field) {
			case "event":
				event.name = string(value)
			case "data":
				dataLines = append(dataLines, string(value))
			}
		}
		if event.name == "" && len(dataLines) == 0 {
			continue
		}
		event.data = strings.Join(dataLines, "\n")
		events = append(events, event)
	}
	return events
}
