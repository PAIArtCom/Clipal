package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/logger"
	"github.com/lansespirit/Clipal/internal/telemetry"
)

type streamResultKind int

const (
	streamRetryNext streamResultKind = iota
	streamFinal
)

type streamResult struct {
	kind     streamResultKind
	delivery deliveryStatus
	protocol protocolStatus
	proto    streamProtocolKind
	cause    string
	bytes    int
	err      error
}

// streamResponseToClient handles the final stage of an upstream attempt: waiting for the first byte,
// committing headers, and streaming the body. It handles idle timeouts, circuit breaker recording,
// and cleanup. It returns a terminal stream result so callers can distinguish a clean completion
// from client disconnects and upstream aborts after the response has already been committed.
func (cp *ClientProxy) streamResponseToClient(w http.ResponseWriter, resp *http.Response, originalReq *http.Request, attemptCtx context.Context, cancelAttempt context.CancelCauseFunc, index int, allow circuitAllowResult, onCommit func(), onSuccess func(streamSuccess)) streamResult {
	// Stream response to the client, with idle-timeout protection.
	var idleTimer *time.Timer
	if cp.upstreamIdle > 0 {
		idleTimer = time.AfterFunc(cp.upstreamIdle, func() { cancelAttempt(errUpstreamIdleTimeout) })
	}
	if resp == nil || resp.Body == nil {
		err := errors.New("upstream response missing body")
		stopTimer(idleTimer)
		return streamResult{
			kind:     streamRetryNext,
			delivery: deliveryRetryNext,
			protocol: protocolNotApplicable,
			proto:    streamProtocolNone,
			cause:    streamCause(protocolNotApplicable, err, attemptCtx, originalReq),
			err:      err,
		}
	}
	upstreamResp := resp

	buf := make([]byte, 32*1024)
	total := 0
	firstN, firstErr := upstreamResp.Body.Read(buf)
	if firstN > 0 && idleTimer != nil {
		idleTimer.Reset(cp.upstreamIdle)
	}
	derivedContentType := inferredResponseContentType(originalReq, upstreamResp, buf[:firstN])
	if strings.TrimSpace(derivedContentType) != "" {
		upstreamResp.Header.Set("Content-Type", derivedContentType)
	}
	tracker := newProtocolTrackerWithContentType(cp.clientType, originalReq, derivedContentType)
	var capture bytes.Buffer
	shouldCapture := !isEventStreamContentType(derivedContentType)
	usageExtractor := usageExtractorFromRequestWithContentType(originalReq, derivedContentType)
	if usageExtractor != nil {
		defer usageExtractor.Cleanup()
	}
	total += firstN
	tracker.append(buf[:firstN])
	if usageExtractor != nil {
		usageExtractor.Append(buf[:firstN])
	}
	if shouldCapture && firstN > 0 && capture.Len() < protocolScanWindow {
		_, _ = capture.Write(buf[:min(firstN, protocolScanWindow-capture.Len())])
	}

	if firstN == 0 && firstErr != nil {
		_ = upstreamResp.Body.Close()
		stopTimer(idleTimer)
		if errors.Is(firstErr, io.EOF) {
			// Legitimately empty body; pass through as-is.
			if onCommit != nil {
				onCommit()
			}
			copyHeaders(w.Header(), upstreamResp.Header)
			w.WriteHeader(upstreamResp.StatusCode)
			protocol := tracker.finalStatus()
			if protocol == protocolIncomplete {
				cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "protocol_incomplete")
			} else {
				if onSuccess != nil {
					onSuccess(buildStreamSuccess(capture.Bytes(), usageExtractor))
				}
				cp.recordCircuitSuccess(time.Now(), index, allow.usedProbe)
			}
			cancelAttempt(nil)
			return streamResult{
				kind:     streamFinal,
				delivery: deliveryCommittedComplete,
				protocol: protocol,
				proto:    tracker.kind,
				cause:    streamCause(protocol, nil, attemptCtx, originalReq),
			}
		}

		if originalReq.Context().Err() != nil {
			// Client went away; do not record a provider failure.
			cp.releaseCircuitPermit(index, allow.usedProbe)
			cancelAttempt(nil)
			return streamResult{
				kind:     streamFinal,
				delivery: deliveryClientCanceled,
				protocol: tracker.abortedStatus(),
				proto:    tracker.kind,
				cause:    "client_canceled",
				err:      originalReq.Context().Err(),
			}
		}
		// Return false so the caller can handle failure (e.g. failover).
		return streamResult{
			kind:     streamRetryNext,
			delivery: deliveryRetryNext,
			protocol: tracker.abortedStatus(),
			proto:    tracker.kind,
			cause:    streamCause(protocolNotApplicable, firstErr, attemptCtx, originalReq),
			err:      firstErr,
		}
	}

	// Committed to this provider.
	if onCommit != nil {
		onCommit()
	}
	copyHeaders(w.Header(), upstreamResp.Header)
	w.WriteHeader(upstreamResp.StatusCode)

	fw := responseBodyWriter(w, originalReq, upstreamResp)
	if firstN > 0 {
		if _, err := fw.Write(buf[:firstN]); err != nil {
			_ = upstreamResp.Body.Close()
			stopTimer(idleTimer)
			cp.releaseCircuitPermit(index, allow.usedProbe)
			cancelAttempt(nil)
			return streamResult{
				kind:     streamFinal,
				delivery: deliveryClientCanceled,
				protocol: tracker.abortedStatus(),
				proto:    tracker.kind,
				cause:    "client_canceled",
				bytes:    total,
				err:      err,
			}
		}
	}

	var copyErr error
	for {
		nr, er := upstreamResp.Body.Read(buf)
		if nr > 0 {
			if idleTimer != nil {
				idleTimer.Reset(cp.upstreamIdle)
			}
			total += nr
			tracker.append(buf[:nr])
			if usageExtractor != nil {
				usageExtractor.Append(buf[:nr])
			}
			if shouldCapture && capture.Len() < protocolScanWindow {
				limit := min(nr, protocolScanWindow-capture.Len())
				if limit > 0 {
					_, _ = capture.Write(buf[:limit])
				}
			}
			if _, ew := fw.Write(buf[:nr]); ew != nil {
				_ = upstreamResp.Body.Close()
				stopTimer(idleTimer)
				cp.releaseCircuitPermit(index, allow.usedProbe)
				cancelAttempt(nil)
				return streamResult{
					kind:     streamFinal,
					delivery: deliveryClientCanceled,
					protocol: tracker.abortedStatus(),
					proto:    tracker.kind,
					cause:    "client_canceled",
					bytes:    total,
					err:      ew,
				}
			}
		}
		if er != nil {
			if errors.Is(er, io.EOF) {
				break
			}
			copyErr = er
			if originalReq.Context().Err() == nil {
				if isUpstreamIdleTimeout(attemptCtx, er) {
					logger.Warn("[%s] upstream %s stalled for %s (after %d bytes)", cp.clientType, cp.providers[index].Name, cp.upstreamIdle, total)
				} else {
					logger.Warn("[%s] response copy failed via %s: %v", cp.clientType, cp.providers[index].Name, er)
				}
			}
			break
		}
	}

	_ = upstreamResp.Body.Close()
	stopTimer(idleTimer)
	if copyErr != nil && originalReq.Context().Err() != nil {
		cp.releaseCircuitPermit(index, allow.usedProbe)
		cancelAttempt(nil)
		return streamResult{
			kind:     streamFinal,
			delivery: deliveryClientCanceled,
			protocol: tracker.abortedStatus(),
			proto:    tracker.kind,
			cause:    "client_canceled",
			bytes:    total,
			err:      originalReq.Context().Err(),
		}
	}
	protocol := tracker.finalStatus()
	if copyErr == nil {
		if protocol == protocolIncomplete {
			cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "protocol_incomplete")
		} else {
			if onSuccess != nil {
				onSuccess(buildStreamSuccess(capture.Bytes(), usageExtractor))
			}
			cp.recordCircuitSuccess(time.Now(), index, allow.usedProbe)
		}
	} else if isUpstreamIdleTimeout(attemptCtx, copyErr) {
		cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "idle_timeout")
	} else {
		cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "network")
	}
	cancelAttempt(nil)
	if copyErr == nil {
		return streamResult{
			kind:     streamFinal,
			delivery: deliveryCommittedComplete,
			protocol: protocol,
			proto:    tracker.kind,
			cause:    streamCause(protocol, nil, attemptCtx, originalReq),
			bytes:    total,
		}
	}
	return streamResult{
		kind:     streamFinal,
		delivery: deliveryCommittedPartial,
		protocol: tracker.abortedStatus(),
		proto:    tracker.kind,
		cause:    streamCause(protocolNotApplicable, copyErr, attemptCtx, originalReq),
		bytes:    total,
		err:      copyErr,
	}
}

func usageExtractorFromRequestWithContentType(req *http.Request, contentType string) *telemetry.UsageExtractor {
	if req == nil {
		return nil
	}
	requestCtx, ok := requestContextFromRequest(req)
	if !ok {
		return nil
	}
	return telemetry.NewUsageExtractor(string(requestCtx.Family), string(requestCtx.Capability), contentType)
}

func buildStreamSuccess(responseBody []byte, extractor *telemetry.UsageExtractor) streamSuccess {
	out := streamSuccess{responseBody: responseBody}
	if extractor == nil {
		return out
	}
	if usage, ok := extractor.Finalize(); ok {
		out.usage = usage
	}
	return out
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

type flushWriter struct {
	w http.ResponseWriter
}

func newFlushWriter(w http.ResponseWriter) io.Writer {
	if _, ok := w.(http.Flusher); !ok {
		return w
	}
	return &flushWriter{w: w}
}

func responseBodyWriter(w http.ResponseWriter, req *http.Request, resp *http.Response) io.Writer {
	if shouldFlushResponse(req, resp) {
		return newFlushWriter(w)
	}
	return w
}

func shouldFlushResponse(req *http.Request, resp *http.Response) bool {
	if resp != nil && isEventStreamContentType(resp.Header.Get("Content-Type")) {
		return true
	}
	requestCtx, ok := requestContextFromRequest(req)
	if !ok {
		return false
	}
	switch requestCtx.Capability {
	case CapabilityGeminiStreamGenerate:
		return true
	default:
		return false
	}
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fl, ok := fw.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

func streamCause(protocol protocolStatus, err error, attemptCtx context.Context, originalReq *http.Request) string {
	if originalReq != nil && originalReq.Context().Err() != nil {
		return "client_canceled"
	}
	if protocol == protocolIncomplete {
		return "protocol_incomplete"
	}
	if isUpstreamIdleTimeout(attemptCtx, err) {
		return "idle_timeout"
	}
	if err != nil {
		return "network"
	}
	return ""
}
