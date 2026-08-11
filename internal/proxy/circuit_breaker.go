package proxy

import (
	"sync"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/PAIArtCom/Clipal/internal/logger"
)

type circuitState string

const (
	circuitClosed   circuitState = "closed"
	circuitOpen     circuitState = "open"
	circuitHalfOpen circuitState = "half_open"
)

type circuitBlockReason string

const (
	circuitBlockOpen         circuitBlockReason = "circuit_open"
	circuitBlockHalfOpenBusy circuitBlockReason = "circuit_half_open_busy"
)

const halfOpenBusyRetryAfter = 1 * time.Second

type circuitBreakerConfig struct {
	enabled             bool
	failureThreshold    int
	successThreshold    int
	openTimeout         time.Duration
	halfOpenMaxInFlight int
}

func normalizeCircuitBreakerConfig(cfg config.CircuitBreakerConfig) circuitBreakerConfig {
	out := circuitBreakerConfig{
		failureThreshold:    cfg.FailureThreshold,
		successThreshold:    cfg.SuccessThreshold,
		halfOpenMaxInFlight: cfg.HalfOpenMaxInFlight,
	}

	// Keep semantics consistent with config validation:
	// failure_threshold == 0 disables the circuit breaker entirely and should not warn about other fields.
	if out.failureThreshold <= 0 {
		out.enabled = false
		return out
	}

	openTimeout, err := cfg.OpenTimeoutDuration()
	if err != nil || openTimeout <= 0 {
		openTimeout = time.Minute
		logger.Warn("invalid circuit_breaker.open_timeout %q, defaulting to 60s", cfg.OpenTimeout)
	}
	out.openTimeout = openTimeout

	if out.successThreshold <= 0 || out.halfOpenMaxInFlight <= 0 {
		out.enabled = false
		return out
	}
	out.enabled = true
	return out
}

type circuitBreaker struct {
	mu sync.Mutex

	cfg circuitBreakerConfig

	state circuitState

	consecutiveFailures  int
	consecutiveSuccesses int

	openedAt time.Time
	// halfOpenInFlight tracks probe requests currently in flight in half-open state.
	halfOpenInFlight int
}

type circuitAllowResult struct {
	allowed   bool
	usedProbe bool
	wait      time.Duration
	reason    circuitBlockReason
}

func newCircuitBreaker(cfg circuitBreakerConfig) *circuitBreaker {
	return &circuitBreaker{
		cfg:   cfg,
		state: circuitClosed,
	}
}

// peekAllow returns whether a request would be allowed right now without reserving a probe slot
// and without modifying circuit state. This is used for availability checks (active provider
// count / Retry-After calculations). Actual state transitions happen exclusively in allow().
func (cb *circuitBreaker) peekAllow(now time.Time) circuitAllowResult {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.cfg.enabled {
		return circuitAllowResult{allowed: true}
	}

	switch cb.state {
	case circuitClosed:
		return circuitAllowResult{allowed: true}
	case circuitOpen:
		if cb.openedAt.IsZero() {
			logger.Error("circuit breaker in open state without openedAt")
			return circuitAllowResult{allowed: false, wait: cb.cfg.openTimeout, reason: circuitBlockOpen}
		}
		elapsed := now.Sub(cb.openedAt)
		if elapsed >= cb.cfg.openTimeout {
			// Open timeout has elapsed — a probe would be allowed in half-open,
			// but we do NOT mutate state here. allow() will perform the transition.
			if cb.halfOpenInFlight >= cb.cfg.halfOpenMaxInFlight {
				return circuitAllowResult{allowed: false, wait: halfOpenBusyRetryAfter, reason: circuitBlockHalfOpenBusy}
			}
			return circuitAllowResult{allowed: true}
		}
		return circuitAllowResult{allowed: false, wait: cb.cfg.openTimeout - elapsed, reason: circuitBlockOpen}
	case circuitHalfOpen:
		if cb.halfOpenInFlight >= cb.cfg.halfOpenMaxInFlight {
			return circuitAllowResult{allowed: false, wait: halfOpenBusyRetryAfter, reason: circuitBlockHalfOpenBusy}
		}
		return circuitAllowResult{allowed: true}
	default:
		// Unknown state; be permissive (read-only: don't reset state here).
		return circuitAllowResult{allowed: true}
	}
}

func (cb *circuitBreaker) allow(now time.Time) circuitAllowResult {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.cfg.enabled {
		return circuitAllowResult{allowed: true}
	}

	switch cb.state {
	case circuitClosed:
		return circuitAllowResult{allowed: true}
	case circuitOpen:
		if cb.openedAt.IsZero() {
			// openedAt should always be set when entering circuitOpen via transitionToOpenLocked.
			// If it's missing, treat this as a bug and fail closed (keep the circuit open).
			logger.Error("circuit breaker in open state without openedAt")
			return circuitAllowResult{allowed: false, wait: cb.cfg.openTimeout, reason: circuitBlockOpen}
		}
		elapsed := now.Sub(cb.openedAt)
		if elapsed >= cb.cfg.openTimeout {
			cb.state = circuitHalfOpen
			cb.consecutiveSuccesses = 0
			cb.halfOpenInFlight = 0
			// fallthrough to half-open handling
		} else {
			return circuitAllowResult{allowed: false, wait: cb.cfg.openTimeout - elapsed, reason: circuitBlockOpen}
		}
		fallthrough
	case circuitHalfOpen:
		if cb.halfOpenInFlight >= cb.cfg.halfOpenMaxInFlight {
			return circuitAllowResult{allowed: false, wait: halfOpenBusyRetryAfter, reason: circuitBlockHalfOpenBusy}
		}
		cb.halfOpenInFlight++
		return circuitAllowResult{allowed: true, usedProbe: true}
	default:
		// Unknown state; be permissive.
		cb.state = circuitClosed
		cb.consecutiveFailures = 0
		cb.consecutiveSuccesses = 0
		cb.openedAt = time.Time{}
		cb.halfOpenInFlight = 0
		return circuitAllowResult{allowed: true}
	}
}

func (cb *circuitBreaker) recordSuccess(now time.Time, usedProbe bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.cfg.enabled {
		return
	}

	if usedProbe && cb.halfOpenInFlight > 0 {
		cb.halfOpenInFlight--
	}

	cb.consecutiveFailures = 0

	switch cb.state {
	case circuitHalfOpen:
		cb.consecutiveSuccesses++
		if cb.consecutiveSuccesses >= cb.cfg.successThreshold {
			cb.state = circuitClosed
			cb.consecutiveSuccesses = 0
			cb.openedAt = time.Time{}
			cb.halfOpenInFlight = 0
		}
	default:
		// Closed/Open: nothing else to do
	}
}

func (cb *circuitBreaker) recordFailure(now time.Time, usedProbe bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.cfg.enabled {
		return
	}

	if usedProbe && cb.halfOpenInFlight > 0 {
		cb.halfOpenInFlight--
	}

	switch cb.state {
	case circuitHalfOpen:
		cb.transitionToOpenLocked(now)
		return
	case circuitOpen:
		if cb.openedAt.IsZero() {
			logger.Error("circuit breaker in open state without openedAt")
		}
		return
	default:
		// closed
	}

	cb.consecutiveFailures++
	if cb.consecutiveFailures >= cb.cfg.failureThreshold {
		cb.transitionToOpenLocked(now)
	}
}

func (cb *circuitBreaker) releaseProbeNeutral(usedProbe bool) {
	if !usedProbe {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.halfOpenInFlight > 0 {
		cb.halfOpenInFlight--
	}
}

func (cb *circuitBreaker) transitionToOpenLocked(now time.Time) {
	cb.state = circuitOpen
	cb.openedAt = now
	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses = 0
	cb.halfOpenInFlight = 0
}

func (cb *circuitBreaker) snapshot(now time.Time) (state circuitState, openWait time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if !cb.cfg.enabled {
		return circuitClosed, 0
	}

	switch cb.state {
	case circuitOpen:
		if cb.openedAt.IsZero() {
			return circuitOpen, cb.cfg.openTimeout
		}
		elapsed := now.Sub(cb.openedAt)
		if elapsed >= cb.cfg.openTimeout {
			// Mirror allow(): after the open timeout elapses, the breaker is ready
			// to probe in half-open state.
			return circuitHalfOpen, 0
		}
		return circuitOpen, cb.cfg.openTimeout - elapsed
	case circuitHalfOpen:
		return circuitHalfOpen, 0
	default:
		return circuitClosed, 0
	}
}
