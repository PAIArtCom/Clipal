package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func newSSETrackerForCapability(t *testing.T, capability RequestCapability) *protocolTracker {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://proxy/v1/test", nil)
	req = withRequestContext(req, RequestContext{
		ClientType: ClientOpenAI,
		Family:     ProtocolFamilyOpenAI,
		Capability: capability,
	})
	return newProtocolTrackerWithContentType(ClientOpenAI, req, "text/event-stream")
}

func TestProtocolTrackerLargeResponsesTerminalEventAcrossChunks(t *testing.T) {
	terminalEvent := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"output":[{"result":"` +
		strings.Repeat("A", 2*protocolScanWindow) + `"}]}}` + "\n\n"

	for _, chunkSize := range []int{1, 2, 7, 1024, 32 * 1024} {
		t.Run(strconv.Itoa(chunkSize), func(t *testing.T) {
			tracker := newSSETrackerForCapability(t, CapabilityOpenAIResponses)
			forwarded := 0
			terminal := false
			for offset := 0; offset < len(terminalEvent); {
				end := min(offset+chunkSize, len(terminalEvent))
				n, done := tracker.append([]byte(terminalEvent[offset:end]))
				forwarded += n
				terminal = done
				offset = end
				if done && offset != len(terminalEvent) {
					t.Fatalf("terminated before the complete SSE event: offset=%d len=%d", offset, len(terminalEvent))
				}
			}
			if !terminal {
				t.Fatal("terminal event was not recognized")
			}
			if forwarded != len(terminalEvent) {
				t.Fatalf("forwarded=%d want=%d", forwarded, len(terminalEvent))
			}
			if got := tracker.finalStatus(); got != protocolCompleted {
				t.Fatalf("status=%s want=%s", got, protocolCompleted)
			}
		})
	}
}

func TestProtocolTrackerLargeDataOnlyTerminalEventWithReorderedType(t *testing.T) {
	tests := []struct {
		name       string
		capability RequestCapability
		eventType  string
		largeField string
	}{
		{name: "responses", capability: CapabilityOpenAIResponses, eventType: "response.completed", largeField: "result"},
		{name: "images", capability: CapabilityOpenAIImages, eventType: "image_generation.completed", largeField: "b64_json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newSSETrackerForCapability(t, tc.capability)
			event := `data: {"sequence_number":7,"` + tc.largeField + `":"` +
				strings.Repeat("D", 2*protocolScanWindow) + `","type":"` + tc.eventType + `"}` + "\n\n"
			for offset := 0; offset < len(event); offset += 4096 {
				end := min(offset+4096, len(event))
				_, _ = tracker.append([]byte(event[offset:end]))
			}
			if !tracker.hasTerminalEvent() || tracker.finalStatus() != protocolCompleted {
				t.Fatalf("large reordered data-only %s event was not recognized", tc.eventType)
			}
		})
	}
}

func TestProtocolTrackerImageCompletionAndExactCutoff(t *testing.T) {
	tracker := newSSETrackerForCapability(t, CapabilityOpenAIImages)
	terminalEvent := "event: image_generation.completed\n" +
		`data: {"type":"image_generation.completed","b64_json":"` +
		strings.Repeat("B", 2*protocolScanWindow) + `"}` + "\n\n"
	chunk := terminalEvent + ": heartbeat after completion\n\n"

	forward, terminal := tracker.append([]byte(chunk))
	if !terminal {
		t.Fatal("image_generation.completed was not recognized")
	}
	if got := chunk[:forward]; got != terminalEvent {
		t.Fatalf("forwarded suffix after terminal boundary: got len=%d want len=%d", len(got), len(terminalEvent))
	}
	if got := tracker.finalStatus(); got != protocolCompleted {
		t.Fatalf("status=%s want=%s", got, protocolCompleted)
	}
}

func TestProtocolTrackerDoesNotMatchMarkerLikeContent(t *testing.T) {
	tracker := newSSETrackerForCapability(t, CapabilityOpenAIResponses)
	chunk := []byte("event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"response.completed"}` + "\n\n")
	forward, terminal := tracker.append(chunk)
	if terminal {
		t.Fatal("ordinary text containing a terminal marker ended the stream")
	}
	if forward != len(chunk) {
		t.Fatalf("forward=%d want=%d", forward, len(chunk))
	}
}

func TestProtocolTrackerSupportsAllSSELineEndings(t *testing.T) {
	for _, lineEnding := range []string{"\n", "\r\n", "\r"} {
		name := strings.ReplaceAll(strings.ReplaceAll(lineEnding, "\r", "CR"), "\n", "LF")
		t.Run(name, func(t *testing.T) {
			tracker := newSSETrackerForCapability(t, CapabilityOpenAIResponses)
			event := "event: response.completed" + lineEnding +
				`data: {"type":"response.completed"}` + lineEnding + lineEnding
			input := event + ": heartbeat after completion" + lineEnding + lineEnding
			terminal := false
			forwarded := 0
			for offset := 0; offset < len(input) && !terminal; offset++ {
				n, done := tracker.append([]byte(input[offset : offset+1]))
				forwarded += n
				terminal = done
			}
			if !terminal || tracker.finalStatus() != protocolCompleted {
				t.Fatalf("terminal event not recognized with %q line endings", lineEnding)
			}
			if forwarded != len(event) {
				t.Fatalf("forwarded=%d want complete terminal event=%d", forwarded, len(event))
			}
		})
	}
}

func TestProtocolTrackerChatAndClaudeTerminalBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		clientType ClientType
		family     ProtocolFamily
		capability RequestCapability
		event      string
		want       protocolStatus
	}{
		{name: "chat done", clientType: ClientOpenAI, family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIChatCompletions, event: "data: [DONE]\n\n", want: protocolCompleted},
		{name: "claude stop", clientType: ClientClaude, family: ProtocolFamilyClaude, capability: CapabilityClaudeMessages, event: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", want: protocolCompleted},
		{name: "claude error", clientType: ClientClaude, family: ProtocolFamilyClaude, capability: CapabilityClaudeMessages, event: "event: error\ndata: {\"type\":\"error\"}\n\n", want: protocolFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://proxy/v1/test", nil)
			req = withRequestContext(req, RequestContext{ClientType: tc.clientType, Family: tc.family, Capability: tc.capability})
			tracker := newProtocolTrackerWithContentType(tc.clientType, req, "text/event-stream")
			chunk := tc.event + ": heartbeat after completion\n\n"
			forward, terminal := tracker.append([]byte(chunk))
			if !terminal || forward != len(tc.event) {
				t.Fatalf("terminal=%v forward=%d want=%d", terminal, forward, len(tc.event))
			}
			if got := tracker.finalStatus(); got != tc.want {
				t.Fatalf("status=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestPreferIdentityEncodingForStreamingRequests(t *testing.T) {
	tests := []struct {
		name       string
		family     ProtocolFamily
		capability RequestCapability
		accept     string
		body       string
		want       string
	}{
		{name: "responses stream", family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIResponses, body: `{"stream":true}`, want: "identity"},
		{name: "images stream", family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIImages, body: `{"stream":true}`, want: "identity"},
		{name: "claude stream", family: ProtocolFamilyClaude, capability: CapabilityClaudeMessages, body: `{"stream":true}`, want: "identity"},
		{name: "gemini stream endpoint", family: ProtocolFamilyGemini, capability: CapabilityGeminiStreamGenerate, body: `{}`, want: "identity"},
		{name: "explicit SSE accept", family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIResponses, accept: "text/event-stream", body: `{"stream":false}`, want: "identity"},
		{name: "non-streaming", family: ProtocolFamilyOpenAI, capability: CapabilityOpenAIResponses, body: `{"stream":false}`, want: "gzip"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := httptest.NewRequest(http.MethodPost, "http://proxy/v1/test", nil)
			original = withRequestContext(original, RequestContext{
				ClientType: ClientOpenAI,
				Family:     tc.family,
				Capability: tc.capability,
			})
			proxyReq := httptest.NewRequest(http.MethodPost, "http://upstream/v1/test", nil)
			proxyReq.Header.Set("Accept-Encoding", "gzip")
			if tc.accept != "" {
				proxyReq.Header.Set("Accept", tc.accept)
			}
			preferIdentityEncodingForStream(original, proxyReq, newRequestPayload([]byte(tc.body)))
			if got := proxyReq.Header.Get("Accept-Encoding"); got != tc.want {
				t.Fatalf("Accept-Encoding=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestForwardWithFailoverRequestsIdentityForStreamingUpstream(t *testing.T) {
	var gotEncoding string
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "responses", BaseURL: "http://responses", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		gotEncoding = req.Header.Get("Accept-Encoding")
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"), nil
	})

	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"stream":true}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, br")
	req = withRequestContext(req, requestContextForClientPath(ClientOpenAI, "/v1/responses", true))
	cp.forwardWithFailover(httptest.NewRecorder(), req, "/v1/responses")

	if gotEncoding != "identity" {
		t.Fatalf("upstream Accept-Encoding=%q want=identity", gotEncoding)
	}
}

func TestProtocolTrackerSemanticTerminalStatuses(t *testing.T) {
	tests := []struct {
		event string
		want  protocolStatus
	}{
		{event: "response.failed", want: protocolFailed},
		{event: "response.incomplete", want: protocolIncomplete},
	}
	for _, tc := range tests {
		t.Run(tc.event, func(t *testing.T) {
			tracker := newSSETrackerForCapability(t, CapabilityOpenAIResponses)
			payload := []byte("event: " + tc.event + "\ndata: {\"type\":\"" + tc.event + "\"}\n\n")
			_, terminal := tracker.append(payload)
			if !terminal || !tracker.hasTerminalEvent() {
				t.Fatal("expected a complete semantic terminal event")
			}
			if got := tracker.finalStatus(); got != tc.want {
				t.Fatalf("status=%s want=%s", got, tc.want)
			}
		})
	}
}

type terminalThenBlockingBody struct {
	data      []byte
	offset    int
	closed    chan struct{}
	closeOnce sync.Once
}

func (b *terminalThenBlockingBody) Read(p []byte) (int, error) {
	if b.offset < len(b.data) {
		n := copy(p, b.data[b.offset:])
		b.offset += n
		return n, nil
	}
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *terminalThenBlockingBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestForwardWithFailoverStopsAfterLargeImageTerminalEvent(t *testing.T) {
	terminalEvent := "event: image_generation.completed\n" +
		`data: {"type":"image_generation.completed","b64_json":"` +
		strings.Repeat("C", 2*protocolScanWindow) + `"}` + "\n\n"
	body := &terminalThenBlockingBody{
		data:   []byte(terminalEvent + ": heartbeat after completion\n\n"),
		closed: make(chan struct{}),
	}
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "images", BaseURL: "http://images", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       body,
		}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/images/generations", bytes.NewReader([]byte(`{"model":"gpt-image-1","stream":true}`)))
	req = withRequestContext(req, requestContextForClientPath(ClientOpenAI, "/v1/images/generations", true))
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cp.forwardWithFailover(rr, req, "/v1/images/generations")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = body.Close()
		t.Fatal("proxy waited for upstream EOF after the terminal image event")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("upstream response body was not closed after terminal event")
	}
	if got := rr.Body.String(); got != terminalEvent {
		t.Fatalf("forwarded body len=%d want=%d", len(got), len(terminalEvent))
	}
}

func TestForwardWithFailoverRemovesContentLengthWhenEndingAtTerminalEvent(t *testing.T) {
	terminalEvent := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}` + "\n\n"
	upstreamBody := terminalEvent + ": heartbeat after completion\n\n"
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "responses", BaseURL: "http://responses", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})
	cp.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"text/event-stream"}, "Content-Length": []string{strconv.Itoa(len(upstreamBody))}},
			ContentLength: int64(len(upstreamBody)),
			Body:          io.NopCloser(strings.NewReader(upstreamBody)),
		}, nil
	})

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = withRequestContext(req, requestContextForClientPath(ClientOpenAI, "/v1/responses", true))
		cp.forwardWithFailover(w, req, "/v1/responses")
	}))
	defer proxyServer.Close()

	resp, err := proxyServer.Client().Post(proxyServer.URL+"/clipal/v1/responses", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(gotBody); got != terminalEvent {
		t.Fatalf("body len=%d want=%d", len(got), len(terminalEvent))
	}
	if got := resp.Header.Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length=%q want empty", got)
	}
	if resp.ContentLength != -1 {
		t.Fatalf("response ContentLength=%d want=-1", resp.ContentLength)
	}
}

func TestSemanticTerminalEventDoesNotTripCircuitBreaker(t *testing.T) {
	for _, event := range []string{"response.failed", "response.incomplete"} {
		t.Run(event, func(t *testing.T) {
			cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
				{Name: "responses", BaseURL: "http://responses", APIKey: "k1", Priority: 1},
			}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{
				enabled:             true,
				failureThreshold:    1,
				successThreshold:    1,
				openTimeout:         time.Minute,
				halfOpenMaxInFlight: 1,
			})
			cp.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
				payload := "event: " + event + "\ndata: {\"type\":\"" + event + "\"}\n\n"
				return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, payload), nil
			})
			cb := cp.breakers[0]
			cb.mu.Lock()
			cb.consecutiveFailures = 1
			cb.mu.Unlock()

			req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"stream":true}`)))
			req = withRequestContext(req, requestContextForClientPath(ClientOpenAI, "/v1/responses", true))
			cp.forwardWithFailover(httptest.NewRecorder(), req, "/v1/responses")

			cb.mu.Lock()
			defer cb.mu.Unlock()
			if cb.state != circuitClosed || cb.consecutiveFailures != 1 {
				t.Fatalf("semantic terminal event mutated circuit evidence: state=%s failures=%d", cb.state, cb.consecutiveFailures)
			}
		})
	}
}

func TestSemanticTerminalEventLeavesHalfOpenCircuitNeutral(t *testing.T) {
	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "responses", BaseURL: "http://responses", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{
		enabled:             true,
		failureThreshold:    1,
		successThreshold:    1,
		openTimeout:         time.Minute,
		halfOpenMaxInFlight: 1,
	})
	cp.httpClient.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, http.Header{"Content-Type": []string{"text/event-stream"}}, "event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n"), nil
	})
	cb := cp.breakers[0]
	cb.mu.Lock()
	cb.state = circuitHalfOpen
	cb.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "http://proxy/clipal/v1/responses", bytes.NewReader([]byte(`{"stream":true}`)))
	req = withRequestContext(req, requestContextForClientPath(ClientOpenAI, "/v1/responses", true))
	cp.forwardWithFailover(httptest.NewRecorder(), req, "/v1/responses")

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != circuitHalfOpen || cb.halfOpenInFlight != 0 || cb.consecutiveSuccesses != 0 {
		t.Fatalf("semantic failure changed half-open circuit: state=%s in_flight=%d successes=%d", cb.state, cb.halfOpenInFlight, cb.consecutiveSuccesses)
	}
}
