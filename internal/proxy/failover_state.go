package proxy

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/logger"
)

type providerDeactivation struct {
	at      time.Time
	until   time.Time
	reason  string
	status  int
	message string
}

type providerBusyState struct {
	Until         time.Time
	BackoffStep   int
	Reason        string
	ProbeInFlight int
}

func setRetryAfterHeader(w http.ResponseWriter, wait time.Duration) {
	// Best-effort: include Retry-After for retryable errors.
	// If a handler already set it, keep that value.
	if w.Header().Get("Retry-After") != "" {
		return
	}
	secs := 1
	if wait > 0 {
		secs = int(wait/time.Second) + 1
		if secs < 1 {
			secs = 1
		}
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}

func writeProxyError(w http.ResponseWriter, msg string, status int) {
	// Only attach Retry-After for retryable errors we generate locally.
	// Upstream responses are streamed through as-is.
	if status == http.StatusTooManyRequests || shouldRetry(status) {
		setRetryAfterHeader(w, time.Second)
	}
	http.Error(w, msg, status)
}

func (cp *ClientProxy) reactivateExpired() {
	now := time.Now()

	cp.mu.Lock()
	defer cp.mu.Unlock()

	if len(cp.deactivated) != len(cp.providers) {
		// This indicates a bug in initialization; log it for debugging.
		logger.Error("[%s] deactivated slice length mismatch: %d != %d (providers)",
			cp.clientType, len(cp.deactivated), len(cp.providers))
		return
	}
	for i, d := range cp.deactivated {
		if d.until.IsZero() {
			continue
		}
		if now.Before(d.until) {
			continue
		}
		detail := reactivationDetail(d.reason)
		cp.deactivated[i] = providerDeactivation{}
		if i < len(cp.providers) {
			logger.Info("[%s] provider %s %s", cp.clientType, cp.providers[i].Name, detail)
		} else {
			logger.Info("[%s] provider #%d %s", cp.clientType, i, detail)
		}
	}
	for i := range cp.keyDeactivated {
		for j, d := range cp.keyDeactivated[i] {
			if d.until.IsZero() {
				continue
			}
			if now.Before(d.until) {
				continue
			}
			cp.keyDeactivated[i][j] = providerDeactivation{}
			if i < len(cp.providers) {
				logger.Info("[%s] provider %s key %d/%d reactivated", cp.clientType, cp.providers[i].Name, j+1, len(cp.providerKeys[i]))
			}
		}
	}
}

func (cp *ClientProxy) isDeactivated(index int) bool {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	if index < 0 || index >= len(cp.deactivated) {
		return false
	}
	d := cp.deactivated[index]
	return !d.until.IsZero() && time.Now().Before(d.until)
}

func (cp *ClientProxy) deactivateFor(index int, reason string, status int, msg string, d time.Duration) {
	if d <= 0 {
		return
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if index < 0 || index >= len(cp.deactivated) {
		return
	}
	if !cp.deactivated[index].until.IsZero() && time.Now().Before(cp.deactivated[index].until) {
		return
	}
	now := time.Now()
	cp.deactivated[index] = providerDeactivation{
		at:      now,
		until:   now.Add(d),
		reason:  reason,
		status:  status,
		message: msg,
	}

	// If a routing cursor was deactivated, move it forward within its scope.
	if cp.mode != config.ClientModeManual {
		cp.advanceScopeIndicesLocked(index)
	}
}

func (cp *ClientProxy) markProviderBusy(index int, reason string, backoffStep int, now time.Time, wait time.Duration) {
	if wait <= 0 {
		return
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if index < 0 || index >= len(cp.providerBusy) {
		return
	}
	until := now.Add(wait)
	busy := cp.providerBusy[index]
	if backoffStep > busy.BackoffStep {
		busy.BackoffStep = backoffStep
	}
	if until.After(busy.Until) {
		busy.Until = until
	}
	if strings.TrimSpace(reason) != "" {
		busy.Reason = reason
	}
	cp.providerBusy[index] = busy
}

func (cp *ClientProxy) providerBusySnapshot(index int) providerBusyState {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	if index < 0 || index >= len(cp.providerBusy) {
		return providerBusyState{}
	}
	return cp.providerBusy[index]
}

func (cp *ClientProxy) providerBusyWait(index int, now time.Time) (time.Duration, bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	if index < 0 || index >= len(cp.providerBusy) {
		return 0, false
	}
	busy := cp.providerBusy[index]
	if busy.Until.IsZero() || !now.Before(busy.Until) {
		return 0, false
	}
	return time.Until(busy.Until), true
}

func (cp *ClientProxy) clearProviderBusy(index int) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if index < 0 || index >= len(cp.providerBusy) {
		return
	}
	cp.providerBusy[index] = providerBusyState{}
}

func (cp *ClientProxy) acquireProviderBusyProbe(index int) bool {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if index < 0 || index >= len(cp.providerBusy) {
		return false
	}
	limit := cp.routing.busyProbeMaxInFlight
	if limit <= 0 {
		limit = 1
	}
	if cp.providerBusy[index].ProbeInFlight >= limit {
		return false
	}
	cp.providerBusy[index].ProbeInFlight++
	return true
}

func (cp *ClientProxy) releaseProviderBusyProbe(index int) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if index < 0 || index >= len(cp.providerBusy) {
		return
	}
	if cp.providerBusy[index].ProbeInFlight > 0 {
		cp.providerBusy[index].ProbeInFlight--
	}
}

func (cp *ClientProxy) nextBusyBackoff(index int) (int, time.Duration) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	step := 0
	if index >= 0 && index < len(cp.providerBusy) {
		busy := cp.providerBusy[index]
		if !busy.Until.IsZero() || busy.Reason != "" || busy.BackoffStep > 0 {
			step = busy.BackoffStep + 1
		}
	}
	if len(cp.routing.busyRetryDelays) == 0 {
		return step, 0
	}
	if step >= len(cp.routing.busyRetryDelays) {
		step = len(cp.routing.busyRetryDelays) - 1
	}
	return step, cp.routing.busyRetryDelays[step]
}

func (cp *ClientProxy) advanceScopeIndicesLocked(index int) {
	if cp.currentIndex == index {
		cp.currentIndex = cp.nextActiveIndexLocked(index)
	}
	if cp.countTokensIndex == index {
		cp.countTokensIndex = cp.nextActiveIndexLocked(index)
	}
	if cp.responsesIndex == index {
		cp.responsesIndex = cp.nextActiveIndexLocked(index)
	}
	if cp.geminiStreamIndex == index {
		cp.geminiStreamIndex = cp.nextActiveIndexLocked(index)
	}
}

func (cp *ClientProxy) isKeyDeactivated(providerIndex int, keyIndex int) bool {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.isKeyDeactivatedLocked(providerIndex, keyIndex, time.Now())
}

func (cp *ClientProxy) isKeyDeactivatedLocked(providerIndex int, keyIndex int, now time.Time) bool {
	if providerIndex < 0 || providerIndex >= len(cp.keyDeactivated) {
		return false
	}
	if keyIndex < 0 || keyIndex >= len(cp.keyDeactivated[providerIndex]) {
		return false
	}
	d := cp.keyDeactivated[providerIndex][keyIndex]
	return !d.until.IsZero() && now.Before(d.until)
}

func (cp *ClientProxy) availableKeyCountLocked(providerIndex int, now time.Time) int {
	if providerIndex < 0 || providerIndex >= len(cp.providerKeys) {
		return 0
	}
	count := 0
	for keyIndex := range cp.providerKeys[providerIndex] {
		if !cp.isKeyDeactivatedLocked(providerIndex, keyIndex, now) {
			count++
		}
	}
	return count
}

func (cp *ClientProxy) providerAvailableForRoutingLocked(providerIndex int, now time.Time) bool {
	if providerIndex < 0 || providerIndex >= len(cp.providers) {
		return false
	}
	if !cp.deactivated[providerIndex].until.IsZero() && now.Before(cp.deactivated[providerIndex].until) {
		return false
	}
	if cp.availableKeyCountLocked(providerIndex, now) == 0 {
		return false
	}
	if providerIndex < len(cp.breakers) && cp.breakers[providerIndex] != nil {
		allow := cp.breakers[providerIndex].peekAllow(now)
		if !allow.allowed && allow.wait > 0 {
			return false
		}
	}
	return true
}

func (cp *ClientProxy) nextActiveKeyIndexLocked(providerIndex int, from int, now time.Time) int {
	if providerIndex < 0 || providerIndex >= len(cp.providerKeys) {
		return 0
	}
	n := len(cp.providerKeys[providerIndex])
	if n == 0 {
		return 0
	}
	if from < 0 || from >= n {
		from = 0
	}
	if !cp.isKeyDeactivatedLocked(providerIndex, from, now) {
		return from
	}
	for step := 1; step <= n; step++ {
		idx := (from + step) % n
		if !cp.isKeyDeactivatedLocked(providerIndex, idx, now) {
			return idx
		}
	}
	return from
}

func (cp *ClientProxy) keyIndicesForScopeLocked(scope routingScope) []int {
	switch scope {
	case routingScopeClaudeCountTokens:
		return cp.countTokensKeyIndex
	case routingScopeOpenAIResponses:
		return cp.responsesKeyIndex
	case routingScopeGeminiStream:
		return cp.geminiStreamKeyIndex
	default:
		return cp.currentKeyIndex
	}
}

func (cp *ClientProxy) setCurrentKeyIndexForScope(providerIndex int, keyIndex int, scope routingScope) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cur := cp.keyIndicesForScopeLocked(scope)
	if cur == nil || providerIndex < 0 || providerIndex >= len(cur) {
		return
	}
	cur[providerIndex] = keyIndex
}

func (cp *ClientProxy) preferredKeyIndexForScope(providerIndex int, scope routingScope) int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	if providerIndex < 0 || providerIndex >= len(cp.providerKeys) || len(cp.providerKeys[providerIndex]) == 0 {
		return 0
	}
	cur := cp.keyIndicesForScopeLocked(scope)
	if cur == nil || providerIndex >= len(cur) {
		return 0
	}
	if cur[providerIndex] < 0 || cur[providerIndex] >= len(cp.providerKeys[providerIndex]) {
		return 0
	}
	return cur[providerIndex]
}

func (cp *ClientProxy) getActiveKeyCountAndStartIndexForScope(providerIndex int, scope routingScope) (active int, startIndex int) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	now := time.Now()
	if providerIndex < 0 || providerIndex >= len(cp.providerKeys) || len(cp.providerKeys[providerIndex]) == 0 {
		return 0, 0
	}
	active = cp.availableKeyCountLocked(providerIndex, now)
	if active == 0 {
		return 0, 0
	}
	cur := cp.keyIndicesForScopeLocked(scope)
	if cur == nil || providerIndex >= len(cur) {
		return active, 0
	}
	if cur[providerIndex] < 0 || cur[providerIndex] >= len(cp.providerKeys[providerIndex]) {
		cur[providerIndex] = 0
	}
	cur[providerIndex] = cp.nextActiveKeyIndexLocked(providerIndex, cur[providerIndex], now)
	return active, cur[providerIndex]
}

func (cp *ClientProxy) activeKeyCount(providerIndex int) int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.availableKeyCountLocked(providerIndex, time.Now())
}

func (cp *ClientProxy) nextActiveKeyIndex(providerIndex int, from int) int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.nextActiveKeyIndexLocked(providerIndex, from, time.Now())
}

func (cp *ClientProxy) deactivateKeyFor(providerIndex int, keyIndex int, reason string, status int, msg string, d time.Duration) {
	if d <= 0 {
		return
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if providerIndex < 0 || providerIndex >= len(cp.keyDeactivated) {
		return
	}
	if keyIndex < 0 || keyIndex >= len(cp.keyDeactivated[providerIndex]) {
		return
	}
	now := time.Now()
	if !cp.keyDeactivated[providerIndex][keyIndex].until.IsZero() && now.Before(cp.keyDeactivated[providerIndex][keyIndex].until) {
		return
	}
	cp.keyDeactivated[providerIndex][keyIndex] = providerDeactivation{
		at:      now,
		until:   now.Add(d),
		reason:  reason,
		status:  status,
		message: msg,
	}
	for _, cur := range [][]int{cp.currentKeyIndex, cp.countTokensKeyIndex, cp.responsesKeyIndex, cp.geminiStreamKeyIndex} {
		if providerIndex < len(cur) && cur[providerIndex] == keyIndex {
			cur[providerIndex] = cp.nextActiveKeyIndexLocked(providerIndex, keyIndex, now)
		}
	}
}

func (cp *ClientProxy) timeUntilNextKeyAvailableLocked(providerIndex int, now time.Time) (wait time.Duration, reason string, ok bool) {
	if providerIndex < 0 || providerIndex >= len(cp.keyDeactivated) {
		return 0, "", false
	}
	var soonest time.Time
	var soonestReason string
	for _, d := range cp.keyDeactivated[providerIndex] {
		if d.until.IsZero() || !now.Before(d.until) {
			continue
		}
		if soonest.IsZero() || d.until.Before(soonest) {
			soonest = d.until
			soonestReason = d.reason
		}
	}
	if soonest.IsZero() {
		return 0, "", false
	}
	return time.Until(soonest), soonestReason, true
}

func (cp *ClientProxy) deactivationUntil(index int) time.Time {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	if index < 0 || index >= len(cp.deactivated) {
		return time.Time{}
	}
	return cp.deactivated[index].until
}

func (cp *ClientProxy) timeUntilNextAvailable() (wait time.Duration, reason string, ok bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	now := time.Now()
	var soonest time.Time
	var soonestReason string
	for _, d := range cp.deactivated {
		if d.until.IsZero() || !now.Before(d.until) {
			continue
		}
		if soonest.IsZero() || d.until.Before(soonest) {
			soonest = d.until
			soonestReason = d.reason
		}
	}
	for i := range cp.providers {
		if cp.availableKeyCountLocked(i, now) > 0 {
			continue
		}
		wait, reason, ok := cp.timeUntilNextKeyAvailableLocked(i, now)
		if !ok || wait <= 0 {
			continue
		}
		until := now.Add(wait)
		if soonest.IsZero() || until.Before(soonest) {
			soonest = until
			soonestReason = reason
		}
	}
	for _, cb := range cp.breakers {
		if cb == nil {
			continue
		}
		allow := cb.peekAllow(now)
		if allow.allowed || allow.wait <= 0 {
			continue
		}
		until := now.Add(allow.wait)
		if soonest.IsZero() || until.Before(soonest) {
			soonest = until
			soonestReason = string(allow.reason)
		}
	}
	if soonest.IsZero() {
		return 0, "", false
	}
	return time.Until(soonest), soonestReason, true
}

func (cp *ClientProxy) activeProviderCount() int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	now := time.Now()
	count := 0
	for i := range cp.providers {
		if cp.providerAvailableForRoutingLocked(i, now) {
			count++
		}
	}
	return count
}

func (cp *ClientProxy) providerAvailableForCapabilityLocked(providerIndex int, now time.Time, capability RequestCapability) bool {
	if providerIndex < 0 || providerIndex >= len(cp.providers) {
		return false
	}
	if !providerSupportsCapability(cp.providers[providerIndex], capability) {
		return false
	}
	return cp.providerAvailableForRoutingLocked(providerIndex, now)
}

func (cp *ClientProxy) getActiveCountAndStartIndexForScope(scope routingScope, capability RequestCapability) (active int, startIndex int) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	now := time.Now()

	// Count active providers.
	for i := range cp.providers {
		if cp.providerAvailableForCapabilityLocked(i, now, capability) {
			active++
		}
	}
	if active == 0 || len(cp.providers) == 0 {
		return active, 0
	}

	startIndex = cp.ensureActiveScopeIndexLocked(scope, now, capability)
	return active, startIndex
}

// handleAllUnavailable writes a Retry-After response if all providers are temporarily unavailable.
// Returns true if a response was written, false otherwise.
func (cp *ClientProxy) handleAllUnavailable(w http.ResponseWriter) bool {
	wait, reason, ok := cp.timeUntilNextAvailable()
	if !ok || wait <= 0 {
		return false
	}
	secs := int(wait/time.Second) + 1
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	if reason == "rate_limit" || reason == "overloaded" {
		http.Error(w, "All providers are rate limited; retry later", http.StatusTooManyRequests)
	} else {
		http.Error(w, "All providers are temporarily unavailable; retry later", http.StatusServiceUnavailable)
	}
	return true
}

func (cp *ClientProxy) countTokensSingleShotTarget() (int, config.Provider, int, bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	if len(cp.providers) == 0 {
		return 0, config.Provider{}, 0, false
	}

	now := time.Now()
	startIndex := cp.currentIndex
	if startIndex < 0 || startIndex >= len(cp.providers) {
		startIndex = 0
	}

	for step := 0; step < len(cp.providers); step++ {
		index := (startIndex + step) % len(cp.providers)
		if !cp.providerAvailableForRoutingLocked(index, now) {
			continue
		}
		if len(cp.providerKeys) <= index || len(cp.providerKeys[index]) == 0 {
			continue
		}

		keyIndex := 0
		if len(cp.currentKeyIndex) > index {
			keyIndex = cp.currentKeyIndex[index]
		}
		if keyIndex < 0 || keyIndex >= len(cp.providerKeys[index]) {
			keyIndex = 0
		}
		keyIndex = cp.nextActiveKeyIndexLocked(index, keyIndex, now)
		if cp.isKeyDeactivatedLocked(index, keyIndex, now) {
			continue
		}

		return index, cp.providers[index], keyIndex, true
	}

	return 0, config.Provider{}, 0, false
}

func (cp *ClientProxy) setCurrentIndexForScope(index int, scope routingScope) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.setScopeIndexLocked(index, scope)
}

func (cp *ClientProxy) setScopeIndexLocked(index int, scope routingScope) {
	switch scope {
	case routingScopeClaudeCountTokens:
		cp.countTokensIndex = index
	case routingScopeOpenAIResponses:
		cp.responsesIndex = index
	case routingScopeGeminiStream:
		cp.geminiStreamIndex = index
	default:
		cp.currentIndex = index
	}
}

func (cp *ClientProxy) scopeIndexLocked(scope routingScope) int {
	switch scope {
	case routingScopeClaudeCountTokens:
		return cp.countTokensIndex
	case routingScopeOpenAIResponses:
		return cp.responsesIndex
	case routingScopeGeminiStream:
		return cp.geminiStreamIndex
	default:
		return cp.currentIndex
	}
}

func (cp *ClientProxy) ensureActiveScopeIndexLocked(scope routingScope, now time.Time, capability RequestCapability) int {
	idx := cp.scopeIndexLocked(scope)
	if idx < 0 || idx >= len(cp.providers) {
		idx = 0
	}

	for step := 0; step < len(cp.providers); step++ {
		candidate := (idx + step) % len(cp.providers)
		if !cp.providerAvailableForCapabilityLocked(candidate, now, capability) {
			continue
		}
		cp.setScopeIndexLocked(candidate, scope)
		return candidate
	}

	cp.setScopeIndexLocked(idx, scope)
	return idx
}

func (cp *ClientProxy) nextActiveIndex(from int) int {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	return cp.nextActiveIndexLocked(from)
}

func (cp *ClientProxy) nextActiveIndexLocked(from int) int {
	n := len(cp.providers)
	if n == 0 {
		return 0
	}
	now := time.Now()
	for step := 1; step <= n; step++ {
		idx := (from + step) % n
		if cp.providerAvailableForRoutingLocked(idx, now) {
			return idx
		}
	}
	// All deactivated; keep current.
	return from % n
}

func (cp *ClientProxy) allowCircuit(now time.Time, providerIndex int) circuitAllowResult {
	if providerIndex < 0 || providerIndex >= len(cp.breakers) {
		return circuitAllowResult{allowed: true}
	}
	cb := cp.breakers[providerIndex]
	if cb == nil {
		return circuitAllowResult{allowed: true}
	}
	return cb.allow(now)
}

func (cp *ClientProxy) releaseCircuitPermit(providerIndex int, usedProbe bool) {
	if providerIndex < 0 || providerIndex >= len(cp.breakers) {
		return
	}
	cb := cp.breakers[providerIndex]
	if cb == nil {
		return
	}
	cb.releaseProbeNeutral(usedProbe)
}

func shouldRecordCircuitFailure(reason string) bool {
	switch reason {
	case "network", "server", "idle_timeout":
		return true
	default:
		return false
	}
}

func (cp *ClientProxy) recordCircuitSuccess(now time.Time, providerIndex int, usedProbe bool) {
	if cp.mode == config.ClientModeManual {
		// Manual mode bypasses circuit breaker behavior and should not mutate breaker state.
		cp.releaseCircuitPermit(providerIndex, usedProbe)
		return
	}
	if providerIndex < 0 || providerIndex >= len(cp.breakers) {
		return
	}
	cb := cp.breakers[providerIndex]
	if cb == nil {
		return
	}
	cb.recordSuccess(now, usedProbe)
}

func (cp *ClientProxy) recordCircuitFailure(now time.Time, providerIndex int, usedProbe bool, reason string) {
	if cp.mode == config.ClientModeManual {
		// Manual mode bypasses circuit breaker behavior and should not mutate breaker state.
		cp.releaseCircuitPermit(providerIndex, usedProbe)
		return
	}
	if providerIndex < 0 || providerIndex >= len(cp.breakers) {
		return
	}
	cb := cp.breakers[providerIndex]
	if cb == nil {
		return
	}
	if shouldRecordCircuitFailure(reason) {
		cb.recordFailure(now, usedProbe)
	} else {
		cb.releaseProbeNeutral(usedProbe)
	}
}

func (cp *ClientProxy) recordCircuitFailureFromClassification(now time.Time, providerIndex int, usedProbe bool, reason string) {
	// Classification reasons map directly.
	cp.recordCircuitFailure(now, providerIndex, usedProbe, reason)
}

func (cp *ClientProxy) recordProviderSwitch(from string, to string, reason string, status int) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.lastSwitch = ProviderSwitchEvent{
		At:     time.Now(),
		From:   from,
		To:     to,
		Reason: reason,
		Status: status,
	}
}

func (cp *ClientProxy) recordLastRequest(now time.Time, req *http.Request, provider string, status int, result streamResult) {
	requestCtx, _ := requestContextFromRequest(req)
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.lastRequest = RequestOutcomeEvent{
		At:         now,
		Provider:   provider,
		Status:     status,
		Delivery:   string(result.delivery),
		Protocol:   string(result.protocol),
		Capability: string(requestCtx.Capability),
		Cause:      result.cause,
		Bytes:      result.bytes,
	}
}

func (cp *ClientProxy) recordTerminalRequest(now time.Time, req *http.Request, provider string, status int, result string, detail string) {
	requestCtx, _ := requestContextFromRequest(req)
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.lastRequest = RequestOutcomeEvent{
		At:         now,
		Provider:   provider,
		Status:     status,
		Capability: string(requestCtx.Capability),
		Result:     result,
		Detail:     detail,
	}
}
