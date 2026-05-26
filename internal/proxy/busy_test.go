package proxy

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

func TestClassifyUpstreamFailure_ConcurrencyBusyRequiresConcurrencySignal(t *testing.T) {
	t.Parallel()

	hdr := make(http.Header)
	hdr.Set("Retry-After", "1")

	action, reason, _, cooldown := classifyUpstreamFailure(
		http.StatusTooManyRequests,
		hdr,
		[]byte(`{"error":{"message":"too many concurrent requests","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`),
		false,
	)
	if action != failureBusyRetry {
		t.Fatalf("action: got %v want %v", action, failureBusyRetry)
	}
	if reason != "busy" {
		t.Fatalf("reason: got %q want %q", reason, "busy")
	}
	if cooldown != time.Second {
		t.Fatalf("cooldown: got %s want %s", cooldown, time.Second)
	}

	action, reason, _, cooldown = classifyUpstreamFailure(
		http.StatusTooManyRequests,
		hdr,
		[]byte(`{"error":{"message":"requests per minute exceeded","type":"rate_limit_exceeded","code":"rate_limit_exceeded"}}`),
		false,
	)
	if action != failureRetryNext {
		t.Fatalf("rpm action: got %v want %v", action, failureRetryNext)
	}
	if reason != "rate_limit" {
		t.Fatalf("rpm reason: got %q want %q", reason, "rate_limit")
	}
	if cooldown != time.Second {
		t.Fatalf("rpm cooldown: got %s want %s", cooldown, time.Second)
	}
}

func TestClassifyUpstreamFailure_429AuthAndQuotaStillDeactivate(t *testing.T) {
	t.Parallel()

	hdr := make(http.Header)
	hdr.Set("Retry-After", "1")

	tests := []struct {
		name   string
		body   string
		reason string
	}{
		{
			name:   "auth",
			body:   `{"error":{"message":"invalid api key","type":"authentication_error","code":"invalid_api_key"}}`,
			reason: "auth",
		},
		{
			name:   "quota",
			body:   `{"error":{"message":"insufficient quota","type":"insufficient_quota","code":"insufficient_quota"}}`,
			reason: "quota",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action, reason, _, cooldown := classifyUpstreamFailure(http.StatusTooManyRequests, hdr, []byte(tt.body), false)
			if action != failureDeactivateAndRetryNext {
				t.Fatalf("action: got %v want %v", action, failureDeactivateAndRetryNext)
			}
			if reason != tt.reason {
				t.Fatalf("reason: got %q want %q", reason, tt.reason)
			}
			if cooldown != 0 {
				t.Fatalf("cooldown: got %s want 0", cooldown)
			}
		})
	}
}

func TestClassifyUpstreamFailure_GoogleCloudCodeQuotaAndRateLimit(t *testing.T) {
	t.Parallel()

	action, reason, _, _ := classifyUpstreamFailure(
		http.StatusTooManyRequests,
		nil,
		[]byte(`{
			"error": {
				"code": 429,
				"status": "RESOURCE_EXHAUSTED",
				"message": "Quota exhausted for quota metric",
				"details": [
					{
						"@type": "type.googleapis.com/google.rpc.ErrorInfo",
						"reason": "QUOTA_EXHAUSTED",
						"domain": "googleapis.com"
					}
				]
			}
		}`),
		false,
	)
	if action != failureDeactivateAndRetryNext || reason != "quota" {
		t.Fatalf("quota classification: action=%v reason=%q", action, reason)
	}

	action, reason, _, _ = classifyUpstreamFailure(
		http.StatusTooManyRequests,
		nil,
		[]byte(`{
			"error": {
				"code": 429,
				"status": "RESOURCE_EXHAUSTED",
				"message": "Rate limit exceeded",
				"details": [
					{
						"@type": "type.googleapis.com/google.rpc.ErrorInfo",
						"reason": "RATE_LIMIT_EXCEEDED",
						"domain": "googleapis.com"
					},
					{
						"@type": "type.googleapis.com/google.rpc.RetryInfo",
						"retryDelay": "60s"
					}
				]
			}
		}`),
		false,
	)
	if action != failureRetryNext || reason != "rate_limit" {
		t.Fatalf("rate classification: action=%v reason=%q", action, reason)
	}
}

func TestMarkProviderBusy_MergesByMaxWindow(t *testing.T) {
	t.Parallel()

	cp := newClientProxy(ClientOpenAI, config.ClientModeAuto, "", []config.Provider{
		{Name: "p1", BaseURL: "http://p1", APIKey: "k1", Priority: 1},
	}, time.Hour, 0, testResponseHeaderTimeout, circuitBreakerConfig{})

	now := time.Now()
	cp.markProviderBusy(0, "busy", 0, now, 5*time.Second)
	first := cp.providerBusySnapshot(0)
	if first.BackoffStep != 0 {
		t.Fatalf("first.BackoffStep: got %d want 0", first.BackoffStep)
	}
	if wait := first.Until.Sub(now); wait < 4*time.Second || wait > 6*time.Second {
		t.Fatalf("first wait: got %s want near 5s", wait)
	}

	cp.markProviderBusy(0, "busy", 1, now.Add(500*time.Millisecond), 10*time.Second)
	second := cp.providerBusySnapshot(0)
	if second.BackoffStep != 1 {
		t.Fatalf("second.BackoffStep: got %d want 1", second.BackoffStep)
	}
	if !second.Until.After(first.Until) {
		t.Fatalf("second.Until: got %s want after %s", second.Until, first.Until)
	}

	cp.markProviderBusy(0, "busy", 0, now.Add(time.Second), 3*time.Second)
	third := cp.providerBusySnapshot(0)
	if third.BackoffStep != 1 {
		t.Fatalf("third.BackoffStep shrank: got %d want 1", third.BackoffStep)
	}
	if third.Until.Before(second.Until) {
		t.Fatalf("third.Until shrank: got %s want >= %s", third.Until, second.Until)
	}
	if !strings.Contains(third.Reason, "busy") {
		t.Fatalf("third.Reason: got %q want to contain busy", third.Reason)
	}
}
