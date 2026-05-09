package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
	"github.com/lansespirit/Clipal/internal/logger"
	"github.com/lansespirit/Clipal/internal/notify"
)

func nextProviderName(cp *ClientProxy, fromIndex int) (idx int, name string) {
	idx = cp.nextActiveIndex(fromIndex)
	if idx == fromIndex {
		return idx, ""
	}
	if idx < 0 || idx >= len(cp.providers) {
		return idx, ""
	}
	n := strings.TrimSpace(cp.providers[idx].Name)
	if n == "" {
		return idx, ""
	}
	return idx, n
}

func (cp *ClientProxy) announceProviderSwitch(from string, to string, reason string, status int) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" || from == to {
		return
	}
	switchView := DescribeProviderSwitch(from, to, reason, status)
	logger.Info("[%s] %s. %s", cp.clientType, switchView.Label, switchView.Detail)
	cp.recordProviderSwitch(from, to, reason, status)
	notify.ProviderSwitched(string(cp.clientType), switchView.Label, switchView.Detail)
}

func unavailableRequestStatus(reason string) (result string, status int, detail string) {
	if reason == "rate_limit" || reason == "overloaded" {
		return "all_providers_unavailable", http.StatusTooManyRequests, "All providers are rate limited; retry later."
	}
	return "all_providers_unavailable", http.StatusServiceUnavailable, "All providers are temporarily unavailable; retry later."
}

func advisoryUnavailableRequestStatus(reason string) (result string, status int, detail string, userMessage string) {
	if reason == "rate_limit" || reason == "overloaded" {
		return "advisory_request_unavailable", http.StatusTooManyRequests,
			"The advisory request is temporarily rate limited; retry later. Primary traffic is unaffected.",
			"Advisory request temporarily rate limited; retry later"
	}
	return "advisory_request_unavailable", http.StatusServiceUnavailable,
		"The advisory request is temporarily unavailable. Primary traffic is unaffected.",
		"Advisory request temporarily unavailable"
}

func describeRequestBuildFailure(provider string, err error) string {
	msg := "local request setup failed"
	if err != nil {
		msg = truncateString(sanitizeLogString(err.Error()), 512)
	}
	if strings.TrimSpace(provider) == "" {
		return "Request could not be prepared locally: " + msg
	}
	return fmt.Sprintf("%s request could not be prepared locally: %s", provider, msg)
}

func isKeyScopedFailure(reason string) bool {
	switch reason {
	case "auth", "billing", "quota", "rate_limit", "overloaded":
		return true
	default:
		return false
	}
}

func keyFailureDuration(reason string, cooldown time.Duration, reactivateAfter time.Duration) time.Duration {
	switch reason {
	case "auth", "billing":
		return reactivateAfter
	case "quota":
		if cooldown > 0 {
			return cooldown
		}
		return reactivateAfter
	case "rate_limit", "overloaded":
		return cooldown
	default:
		return 0
	}
}

// forwardWithFailover forwards the request with automatic failover.
func (cp *ClientProxy) forwardWithFailover(w http.ResponseWriter, req *http.Request, path string) {
	if cp.mode == config.ClientModeManual {
		cp.forwardManual(w, req, path)
		return
	}
	scope := routingScopeForRequest(req)
	requestCtx, ok := requestContextFromRequest(req)
	if !ok {
		requestCtx = requestContextForClientPath(cp.clientType, path, false)
	}

	cp.reactivateExpired()

	// If the client has already gone away (or the server is shutting down), don't do any work.
	if err := req.Context().Err(); err != nil {
		return
	}

	// This availability check does not depend on the request body, so do it
	// before buffering potentially large prompts.
	active, startIndex := cp.getActiveCountAndStartIndexForScope(scope, requestCtx.Capability)
	if active == 0 {
		if wait, reason, ok := cp.timeUntilNextAvailable(); ok && wait > 0 {
			result, status, detail := unavailableRequestStatus(reason)
			cp.recordTerminalRequest(time.Now(), req, "", status, result, detail)
		} else {
			cp.recordTerminalRequest(time.Now(), req, "", http.StatusServiceUnavailable, "all_providers_unavailable", "All providers are unavailable.")
		}
		if handled := cp.handleAllUnavailable(w); handled {
			return
		}
		logger.Error("[%s] all providers unavailable", cp.clientType)
		writeProxyError(w, "All providers are unavailable", http.StatusServiceUnavailable)
		return
	}

	// Read the request body once for potential retries
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		logger.Error("[%s] failed to read request body: %v", cp.clientType, err)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			cp.recordTerminalRequest(time.Now(), req, "", http.StatusRequestEntityTooLarge, "request_rejected", "Request body too large.")
			writeProxyError(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		cp.recordTerminalRequest(time.Now(), req, "", http.StatusBadRequest, "request_rejected", "Failed to read request body.")
		writeProxyError(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer func() { _ = req.Body.Close() }()
	payload := newRequestPayload(bodyBytes)
	requestKey := payload.requestStickyKey(requestCtx)
	if preferredIndex, preferredKeyIndex, ok := cp.resolveStickyProvider(scope, requestKey, time.Now()); ok {
		if providerSupportsCapability(cp.providers[preferredIndex], requestCtx.Capability) &&
			!cp.isDeactivated(preferredIndex) &&
			cp.activeKeyCount(preferredIndex) > 0 {
			startIndex = preferredIndex
			if preferredKeyIndex >= 0 {
				cp.setCurrentKeyIndexForScope(preferredIndex, preferredKeyIndex, scope)
			}
		}
	}
	preferredIndex := startIndex

	attempted := 0
	lastSwitchReason := ""
	lastSwitchStatus := 0
	lastFailedProvider := ""
	attemptSummaries := make([]string, 0, active)
	hadUpstreamAttempt := false

	for offset := 0; offset < len(cp.providers) && attempted < active; offset++ {
		if err := req.Context().Err(); err != nil {
			return
		}

		index := (startIndex + offset) % len(cp.providers)
		if !providerSupportsCapability(cp.providers[index], requestCtx.Capability) ||
			cp.isDeactivated(index) ||
			cp.activeKeyCount(index) == 0 {
			continue
		}
		now := time.Now()
		allow := cp.allowCircuit(now, index)
		if !allow.allowed {
			continue
		}
		provider := cp.providers[index]
		keyActive, keyStart := cp.getActiveKeyCountAndStartIndexForScope(index, scope)
		if keyActive == 0 {
			cp.releaseCircuitPermit(index, allow.usedProbe)
			continue
		}

		attempted++
		logger.Debug("[%s] forwarding to: %s (attempt %d/%d, keys=%d)", cp.clientType, provider.Name, attempted, active, len(cp.providerKeys[index]))

		providerFailed := false
		busyRetried := false
		busyProbeHeld := false
		keyExhausted := false
		keyExhaustedReason := ""
		keyExhaustedStatus := 0
		if wait, ok := cp.providerBusyWait(index, time.Now()); ok {
			if index != preferredIndex || wait > cp.routing.maxInlineWait {
				cp.releaseCircuitPermit(index, allow.usedProbe)
				continue
			}
			if !waitInline(req.Context(), wait) {
				cp.releaseCircuitPermit(index, allow.usedProbe)
				return
			}
			if !cp.acquireProviderBusyProbe(index) {
				cp.releaseCircuitPermit(index, allow.usedProbe)
				continue
			}
			busyProbeHeld = true
			busyRetried = true
		}

		for keyOffset, keyTried := 0, 0; keyOffset < len(cp.providerKeys[index]) && keyTried < keyActive; keyOffset++ {
			keyIndex := (keyStart + keyOffset) % len(cp.providerKeys[index])
			if cp.isKeyDeactivated(index, keyIndex) {
				continue
			}
			keyTried++
			apiKey := cp.providerKeys[index][keyIndex]

			attemptCtx, cancelAttempt := context.WithCancelCause(req.Context())
			reqWithAttemptCtx := req.WithContext(attemptCtx)
			resp, prepared, err := cp.doProviderRequestWithPayload(reqWithAttemptCtx, provider, index, apiKey, path, payload)
			if err != nil {
				if !prepared {
					summary := describeRequestBuildFailure(provider.Name, err)
					attemptSummaries = append(attemptSummaries, summary)
					lastFailedProvider = provider.Name
					logger.Error("[%s] %s", cp.clientType, summary)
					if busyProbeHeld {
						cp.releaseProviderBusyProbe(index)
						busyProbeHeld = false
					}
					cp.releaseCircuitPermit(index, allow.usedProbe)
					cancelAttempt(nil)
					providerFailed = true
					break
				}
				hadUpstreamAttempt = true
				if req.Context().Err() != nil {
					if busyProbeHeld {
						cp.releaseProviderBusyProbe(index)
						busyProbeHeld = false
					}
					cp.releaseCircuitPermit(index, allow.usedProbe)
					cancelAttempt(nil)
					return
				}
				if busyProbeHeld {
					cp.releaseProviderBusyProbe(index)
					busyProbeHeld = false
				}
				cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "network")
				cancelAttempt(nil)
				nextIndex, nextName := nextProviderName(cp, index)
				summary := describeAttemptFailure(provider.Name, "network", 0, true)
				attemptSummaries = append(attemptSummaries, summary)
				if nextName != "" {
					logger.Warn("[%s] %s; trying next=%s", cp.clientType, summary, nextName)
				} else {
					logger.Warn("[%s] %s; trying next provider", cp.clientType, summary)
				}
				lastSwitchReason = "network"
				lastSwitchStatus = 0
				lastFailedProvider = provider.Name
				if nextName != "" {
					cp.announceProviderSwitch(provider.Name, nextName, lastSwitchReason, lastSwitchStatus)
				}
				cp.setCurrentIndexForScope(nextIndex, scope)
				providerFailed = true
				break
			}
			hadUpstreamAttempt = true

			var (
				action   failureAction
				reason   string
				msg      string
				cooldown time.Duration
			)
			inspect := resp.StatusCode == http.StatusUnauthorized ||
				resp.StatusCode == http.StatusForbidden ||
				resp.StatusCode == http.StatusPaymentRequired ||
				resp.StatusCode == http.StatusTooManyRequests ||
				shouldRetry(resp.StatusCode)
			if inspect {
				body, truncated := readResponseBodyBytes(resp, 32*1024)
				action, reason, msg, cooldown = classifyUpstreamFailure(resp.StatusCode, resp.Header, body, truncated)
				if resp.StatusCode == http.StatusTooManyRequests && provider.UsesOAuth() && isOAuthCooldownReason(reason) {
					cooldown = cp.oauthCooldownForFailure(req.Context(), provider, index, path, resp.Header, body, cooldown)
				}
			} else {
				action = failureReturnToClient
			}

			if action != failureReturnToClient {
				_ = resp.Body.Close()
				cancelAttempt(nil)
				lastFailedProvider = provider.Name
				summary := describeAttemptFailure(provider.Name, reason, resp.StatusCode, false)
				attemptSummaries = append(attemptSummaries, summary)
				if action == failureBusyRetry {
					if busyProbeHeld {
						cp.releaseProviderBusyProbe(index)
						busyProbeHeld = false
					}
					cp.releaseCircuitPermit(index, allow.usedProbe)
					step, wait := cp.nextBusyBackoff(index)
					cp.markProviderBusy(index, reason, step, time.Now(), wait)
					if index == preferredIndex && !busyRetried && wait > 0 && wait <= cp.routing.maxInlineWait {
						if !waitInline(req.Context(), wait) {
							return
						}
						if cp.acquireProviderBusyProbe(index) {
							busyProbeHeld = true
							busyRetried = true
							// Reset the inner key loop; the `for` post statement will
							// increment keyOffset back to 0 before the next iteration.
							keyOffset = -1
							keyTried = 0
							continue
						}
					}
					lastSwitchReason = reason
					lastSwitchStatus = resp.StatusCode
					nextIndex, nextName := nextProviderName(cp, index)
					if nextName != "" {
						logger.Warn("[%s] %s; provider busy, overflowing to next=%s", cp.clientType, summary, nextName)
						cp.announceProviderSwitch(provider.Name, nextName, lastSwitchReason, lastSwitchStatus)
					} else {
						logger.Warn("[%s] %s; provider busy, trying next provider", cp.clientType, summary)
					}
					cp.setCurrentIndexForScope(nextIndex, scope)
					providerFailed = true
					break
				}
				if isKeyScopedFailure(reason) {
					d := keyFailureDuration(reason, cooldown, cp.reactivateAfter)
					if d > 0 {
						cp.deactivateKeyFor(index, keyIndex, reason, resp.StatusCode, msg, d)
					}
					nextKeyActive := cp.activeKeyCount(index)
					if nextKeyActive > 0 {
						nextKeyIndex := cp.nextActiveKeyIndex(index, keyIndex)
						cp.setCurrentKeyIndexForScope(index, nextKeyIndex, scope)
						if action == failureDeactivateAndRetryNext {
							logger.Error("[%s] %s; trying next key for provider=%s", cp.clientType, summary, provider.Name)
						} else {
							logger.Warn("[%s] %s; trying next key for provider=%s", cp.clientType, summary, provider.Name)
						}
						continue
					}
					if d > 0 {
						cp.deactivateFor(index, reason, resp.StatusCode, msg, d)
					}
					cp.recordCircuitFailureFromClassification(time.Now(), index, allow.usedProbe, reason)
					keyExhausted = true
					keyExhaustedReason = reason
					keyExhaustedStatus = resp.StatusCode
					break
				}

				cp.recordCircuitFailureFromClassification(time.Now(), index, allow.usedProbe, reason)
				lastSwitchReason = reason
				lastSwitchStatus = resp.StatusCode
				nextIndex, nextName := nextProviderName(cp, index)
				switch action {
				case failureDeactivateAndRetryNext:
					cp.deactivateFor(index, reason, resp.StatusCode, msg, cp.reactivateAfter)
					if nextName != "" {
						logger.Error("[%s] %s; marking provider unavailable and trying next=%s", cp.clientType, summary, nextName)
					} else {
						logger.Error("[%s] %s; marking provider unavailable and trying next provider", cp.clientType, summary)
					}
				case failureRetryNext:
					summaryWithCooldown := summary
					if cooldown > 0 {
						cp.deactivateFor(index, reason, resp.StatusCode, msg, cooldown)
						summaryWithCooldown = fmt.Sprintf("%s; cooling down for %s", summary, cooldown)
					}
					if nextName != "" {
						logger.Warn("[%s] %s; trying next=%s", cp.clientType, summaryWithCooldown, nextName)
					} else {
						logger.Warn("[%s] %s; trying next provider", cp.clientType, summaryWithCooldown)
					}
				}
				if nextName != "" {
					cp.announceProviderSwitch(provider.Name, nextName, lastSwitchReason, lastSwitchStatus)
				}
				cp.setCurrentIndexForScope(nextIndex, scope)
				providerFailed = true
				break
			}

			onCommit := func() {
				cp.setCurrentIndexForScope(index, scope)
				cp.setCurrentKeyIndexForScope(index, keyIndex, scope)
			}
			onSuccess := func(success streamSuccess) {
				if busyProbeHeld {
					cp.releaseProviderBusyProbe(index)
					busyProbeHeld = false
				}
				cp.clearProviderBusy(index)
				now := time.Now()
				cp.learnStickySuccessWithPayload(scope, requestCtx, requestKey, payload, success.responseBody, index, keyIndex, now)
				success.usage = applyUsageCostSnapshot(req, requestCtx, provider, payload, success.usage)
				cp.recordCompletedUsage(req, provider.Name, resp.StatusCode, success.usage, now)
			}

			result := cp.streamResponseToClient(w, resp, req, attemptCtx, cancelAttempt, index, allow, onCommit, onSuccess)
			if result.kind == streamFinal {
				if result.delivery != deliveryCommittedComplete && busyProbeHeld {
					cp.releaseProviderBusyProbe(index)
				}
				cp.logRequestResult(req, provider.Name, resp.StatusCode, result, false)
				return
			}

			if isUpstreamIdleTimeout(attemptCtx, attemptCtx.Err()) {
				summary := describeAttemptFailure(provider.Name, "idle_timeout", 0, true)
				attemptSummaries = append(attemptSummaries, summary)
				logger.Warn("[%s] %s; trying next provider", cp.clientType, summary)
				lastSwitchReason = "idle_timeout"
				lastSwitchStatus = 0
				lastFailedProvider = provider.Name
				cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "idle_timeout")
			} else {
				summary := describeAttemptFailure(provider.Name, "network", 0, true)
				attemptSummaries = append(attemptSummaries, summary)
				logger.Warn("[%s] %s; trying next provider", cp.clientType, summary)
				lastSwitchReason = "network"
				lastSwitchStatus = 0
				lastFailedProvider = provider.Name
				cp.recordCircuitFailure(time.Now(), index, allow.usedProbe, "network")
			}
			if busyProbeHeld {
				cp.releaseProviderBusyProbe(index)
				busyProbeHeld = false
			}
			cancelAttempt(nil)
			nextIndex, nextName := nextProviderName(cp, index)
			if nextName != "" {
				cp.announceProviderSwitch(provider.Name, nextName, lastSwitchReason, lastSwitchStatus)
			}
			cp.setCurrentIndexForScope(nextIndex, scope)
			providerFailed = true
			break
		}

		if providerFailed {
			continue
		}
		if keyExhausted {
			lastFailedProvider = provider.Name
			nextIndex, nextName := nextProviderName(cp, index)
			if nextName != "" {
				logger.Warn("[%s] provider %s exhausted available keys; trying next=%s", cp.clientType, provider.Name, nextName)
				cp.announceProviderSwitch(provider.Name, nextName, keyExhaustedReason, keyExhaustedStatus)
			} else {
				logger.Warn("[%s] provider %s exhausted available keys; trying next provider", cp.clientType, provider.Name)
			}
			cp.setCurrentIndexForScope(nextIndex, scope)
		}
	}

	// If we've cooled down all providers during this request, surface a Retry-After to the client.
	if cp.activeProviderCount() == 0 {
		if wait, reason, ok := cp.timeUntilNextAvailable(); ok && wait > 0 {
			result, status, detail := unavailableRequestStatus(reason)
			cp.recordTerminalRequest(time.Now(), req, "", status, result, detail)
		}
		if handled := cp.handleAllUnavailable(w); handled {
			return
		}
	}

	lastProvider := strings.TrimSpace(lastFailedProvider)
	terminalResult := "all_providers_failed"
	terminalStatus := http.StatusServiceUnavailable
	if !hadUpstreamAttempt {
		terminalResult = "request_rejected"
		terminalStatus = http.StatusBadGateway
	}
	cp.recordTerminalRequest(time.Now(), req, lastProvider, terminalStatus, terminalResult, strings.Join(attemptSummaries, "; "))
	if len(attemptSummaries) > 0 {
		logger.Error("[%s] all providers failed: %s", cp.clientType, strings.Join(attemptSummaries, "; "))
	} else {
		logger.Error("[%s] all providers failed", cp.clientType)
	}
	writeProxyError(w, "All providers failed", http.StatusServiceUnavailable)
}

func waitInline(ctx context.Context, wait time.Duration) bool {
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// forwardCountTokensSingleShot forwards advisory count-token requests as a
// single-shot passthrough. It never retries and never mutates provider health state.
func (cp *ClientProxy) forwardCountTokensSingleShot(w http.ResponseWriter, req *http.Request, path string) {
	if cp.mode == config.ClientModeManual {
		cp.forwardManual(w, req, path)
		return
	}

	cp.reactivateExpired()

	if err := req.Context().Err(); err != nil {
		return
	}

	index, provider, keyIndex, ok := cp.countTokensSingleShotTarget()
	if !ok {
		if wait, reason, ok := cp.timeUntilNextAvailable(); ok && wait > 0 {
			result, status, detail, userMessage := advisoryUnavailableRequestStatus(reason)
			cp.recordTerminalRequest(time.Now(), req, "", status, result, detail)
			setRetryAfterHeader(w, wait)
			logger.Warn("[%s] advisory request unavailable during count_tokens. %s", cp.clientType, detail)
			writeProxyError(w, userMessage, status)
			return
		} else {
			result, status, detail, userMessage := advisoryUnavailableRequestStatus("")
			cp.recordTerminalRequest(time.Now(), req, "", status, result, detail)
			logger.Warn("[%s] advisory request unavailable during count_tokens. %s", cp.clientType, detail)
			writeProxyError(w, userMessage, status)
			return
		}
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		logger.Error("[%s] failed to read request body: %v", cp.clientType, err)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeProxyError(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		writeProxyError(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer func() { _ = req.Body.Close() }()
	payload := newRequestPayload(bodyBytes)

	logger.Debug("[%s] forwarding to: %s (count_tokens single-shot, keys=%d)", cp.clientType, provider.Name, len(cp.providerKeys[index]))

	attemptCtx, cancelAttempt := context.WithCancelCause(req.Context())
	defer cancelAttempt(nil)

	reqWithAttemptCtx := req.WithContext(attemptCtx)
	resp, _, err := cp.doProviderRequestWithPayload(reqWithAttemptCtx, provider, index, cp.providerKeys[index][keyIndex], path, payload)
	if err != nil {
		if req.Context().Err() != nil {
			return
		}
		logger.Warn("[%s] %s during count_tokens", cp.clientType, describeAttemptFailure(provider.Name, "network", 0, true))
		cp.recordTerminalRequest(time.Now(), req, provider.Name, http.StatusBadGateway, "failed_before_response", describeAttemptFailure(provider.Name, "network", 0, true)+".")
		writeProxyError(w, "Upstream request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	n, copyErr := io.Copy(responseBodyWriter(w, req, resp), resp.Body)
	if copyErr == nil {
		cp.logRequestResult(req, provider.Name, resp.StatusCode, streamResult{
			kind:     streamFinal,
			delivery: deliveryCommittedComplete,
			protocol: protocolNotApplicable,
			proto:    streamProtocolNone,
			cause:    "",
			bytes:    int(n),
		}, false)
		return
	}

	if req.Context().Err() != nil {
		cp.logRequestResult(req, provider.Name, resp.StatusCode, streamResult{
			kind:     streamFinal,
			delivery: deliveryClientCanceled,
			protocol: protocolNotApplicable,
			proto:    streamProtocolNone,
			cause:    "client_canceled",
			bytes:    int(n),
			err:      req.Context().Err(),
		}, false)
		return
	}

	reason := "network"
	if isUpstreamIdleTimeout(attemptCtx, copyErr) {
		reason = "idle_timeout"
	}
	cp.recordTerminalRequest(time.Now(), req, provider.Name, http.StatusBadGateway, "failed_before_response", describeAttemptFailure(provider.Name, reason, 0, true)+".")
	logger.Warn("[%s] %s during count_tokens", cp.clientType, describeAttemptFailure(provider.Name, reason, 0, true))
}
