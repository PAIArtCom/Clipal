package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/lansespirit/Clipal/internal/config"
)

type geminiOAuthRewriteAction int

const (
	geminiOAuthRewritePass geminiOAuthRewriteAction = iota
	geminiOAuthRewriteApply
	geminiOAuthRewriteDrop
)

func prepareOAuthProviderResponse(original *http.Request, provider config.Provider, resp *http.Response) (*http.Response, error) {
	if original == nil || resp == nil || !provider.UsesOAuth() {
		return resp, nil
	}

	switch provider.NormalizedOAuthProvider() {
	case config.OAuthProviderGemini:
		return prepareGeminiOAuthResponseForProvider(original, resp)
	case config.OAuthProviderAntigravity:
		return prepareAntigravityOAuthResponse(original, resp)
	default:
		return resp, nil
	}
}

func prepareGeminiOAuthResponse(original *http.Request, resp *http.Response) (*http.Response, error) {
	return prepareGeminiOAuthResponseForProvider(original, resp)
}

func prepareGeminiOAuthResponseForProvider(original *http.Request, resp *http.Response) (*http.Response, error) {
	requestCtx, ok := requestContextFromRequest(original)
	if !ok {
		return resp, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return resp, nil
	}

	switch requestCtx.Capability {
	case CapabilityGeminiGenerateContent:
		if !geminiOAuthResponseShouldRewrite(resp, "application/json") {
			return resp, nil
		}
		return rewriteGeminiOAuthJSONResponse(resp)
	case CapabilityGeminiStreamGenerate:
		if !geminiOAuthResponseShouldRewrite(resp, "text/event-stream") {
			return resp, nil
		}
		return rewriteGeminiOAuthStreamResponse(resp), nil
	default:
		return resp, nil
	}
}

func geminiOAuthResponseShouldRewrite(resp *http.Response, want string) bool {
	if resp == nil {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	return contentType == "" || strings.Contains(contentType, strings.ToLower(want))
}

func rewriteGeminiOAuthJSONResponse(resp *http.Response) (*http.Response, error) {
	if resp == nil || resp.Body == nil {
		return resp, nil
	}

	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}

	rewritten, action, err := rewriteGeminiOAuthJSONPayload(body)
	if err != nil || action != geminiOAuthRewriteApply {
		setGeminiOAuthResponseBody(resp, body)
		return resp, nil
	}
	setGeminiOAuthResponseBody(resp, rewritten)
	return resp, nil
}

func setGeminiOAuthResponseBody(resp *http.Response, body []byte) {
	if resp == nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
}

func rewriteGeminiOAuthStreamResponse(resp *http.Response) *http.Response {
	if resp == nil || resp.Body == nil {
		return resp
	}

	originalBody := resp.Body
	reader := bufio.NewReader(originalBody)
	pipeReader, pipeWriter := io.Pipe()

	go func() {
		defer func() {
			_ = originalBody.Close()
		}()

		var eventLines []string
		flushEvent := func() error {
			if len(eventLines) == 0 {
				return nil
			}
			if err := writeGeminiOAuthSSEEvent(pipeWriter, eventLines); err != nil {
				return err
			}
			eventLines = eventLines[:0]
			return nil
		}

		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				trimmed := strings.TrimRight(line, "\r\n")
				if trimmed == "" {
					if err := flushEvent(); err != nil {
						_ = pipeWriter.CloseWithError(err)
						return
					}
				} else {
					eventLines = append(eventLines, trimmed)
				}
			}
			if err != nil {
				if err == io.EOF {
					if flushErr := flushEvent(); flushErr != nil {
						_ = pipeWriter.CloseWithError(flushErr)
						return
					}
					_ = pipeWriter.Close()
					return
				}
				_ = pipeWriter.CloseWithError(err)
				return
			}
		}
	}()

	resp.Body = pipeReader
	resp.ContentLength = -1
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Del("Content-Length")
	return resp
}

func writeGeminiOAuthSSEEvent(w io.Writer, lines []string) error {
	if len(lines) == 0 {
		return nil
	}

	dataLines := make([]string, 0, len(lines))
	otherLines := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		default:
			otherLines = append(otherLines, line)
		}
	}

	if len(dataLines) == 0 {
		return writeGeminiOAuthRawEvent(w, lines)
	}

	payload := strings.Join(dataLines, "\n")
	if payload == "" || payload == "[DONE]" {
		return writeGeminiOAuthRawEvent(w, lines)
	}

	rewritten, action, err := rewriteGeminiOAuthJSONPayload([]byte(payload))
	if err != nil || action == geminiOAuthRewritePass {
		return writeGeminiOAuthRawEvent(w, lines)
	}
	if action == geminiOAuthRewriteDrop {
		return nil
	}

	for _, line := range otherLines {
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(string(rewritten), "\n") {
		if _, err := io.WriteString(w, "data: "+line+"\n"); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "\n")
	return err
}

func writeGeminiOAuthRawEvent(w io.Writer, lines []string) error {
	for _, line := range lines {
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func rewriteGeminiOAuthJSONPayload(body []byte) ([]byte, geminiOAuthRewriteAction, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, geminiOAuthRewritePass, err
	}

	normalized, action := normalizeGeminiOAuthResponseEnvelope(root)
	if action != geminiOAuthRewriteApply {
		return body, action, nil
	}

	rewritten, err := json.Marshal(normalized)
	if err != nil {
		return body, geminiOAuthRewritePass, err
	}
	return rewritten, geminiOAuthRewriteApply, nil
}

func normalizeGeminiOAuthResponseEnvelope(root map[string]any) (map[string]any, geminiOAuthRewriteAction) {
	if len(root) == 0 {
		return nil, geminiOAuthRewritePass
	}
	if _, ok := root["error"]; ok {
		return nil, geminiOAuthRewritePass
	}

	traceID, hasTraceID := geminiOAuthStringValue(root["traceId"])
	if responseValue, ok := root["response"]; ok {
		responseRoot := map[string]any{
			"candidates": []any{},
		}
		if inner, ok := responseValue.(map[string]any); ok && inner != nil {
			responseRoot = cloneGeminiOAuthMap(inner)
			if _, exists := responseRoot["candidates"]; !exists {
				responseRoot["candidates"] = []any{}
			}
		}
		if hasTraceID {
			if _, exists := responseRoot["responseId"]; !exists {
				responseRoot["responseId"] = traceID
			}
		}
		return responseRoot, geminiOAuthRewriteApply
	}

	if hasGeminiOAuthPublicResponseShape(root) {
		if hasTraceID {
			if _, exists := root["responseId"]; !exists {
				rewritten := cloneGeminiOAuthMap(root)
				rewritten["responseId"] = traceID
				return rewritten, geminiOAuthRewriteApply
			}
		}
		return nil, geminiOAuthRewritePass
	}

	if hasTraceID {
		return map[string]any{
			"responseId": traceID,
			"candidates": []any{},
		}, geminiOAuthRewriteApply
	}

	return nil, geminiOAuthRewriteDrop
}

func hasGeminiOAuthPublicResponseShape(root map[string]any) bool {
	for _, key := range []string{
		"candidates",
		"promptFeedback",
		"usageMetadata",
		"modelVersion",
		"automaticFunctionCallingHistory",
		"responseId",
	} {
		if _, ok := root[key]; ok {
			return true
		}
	}
	return false
}

func geminiOAuthStringValue(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	return text, true
}

func cloneGeminiOAuthMap(root map[string]any) map[string]any {
	out := make(map[string]any, len(root))
	for key, value := range root {
		out[key] = value
	}
	return out
}
