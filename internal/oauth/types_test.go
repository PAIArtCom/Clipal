package oauth

import (
	"testing"
	"time"
)

func TestCredentialNeedsRefreshUsesLifecycleThreshold(t *testing.T) {
	lastRefresh := time.Date(2026, 6, 4, 8, 0, 0, 0, time.UTC)
	expiresAt := lastRefresh.Add(100 * time.Minute)
	cred := &Credential{
		ExpiresAt:   expiresAt,
		LastRefresh: lastRefresh,
	}

	if cred.NeedsRefresh(lastRefresh.Add(79*time.Minute), 30*time.Second) {
		t.Fatalf("NeedsRefresh before 80%% lifecycle threshold = true, want false")
	}
	if !cred.NeedsRefresh(lastRefresh.Add(80*time.Minute), 30*time.Second) {
		t.Fatalf("NeedsRefresh at 80%% lifecycle threshold = false, want true")
	}
}

func TestCredentialNeedsRefreshFallsBackToSkewWithoutLastRefresh(t *testing.T) {
	now := time.Date(2026, 6, 4, 8, 0, 0, 0, time.UTC)
	cred := &Credential{
		ExpiresAt: now.Add(time.Minute),
	}

	if cred.NeedsRefresh(now, 30*time.Second) {
		t.Fatalf("NeedsRefresh outside fallback skew = true, want false")
	}
	if !cred.NeedsRefresh(now.Add(31*time.Second), 30*time.Second) {
		t.Fatalf("NeedsRefresh inside fallback skew = false, want true")
	}
}

func TestCredentialNeedsRefreshUsesEarlierSkewForShortTTL(t *testing.T) {
	lastRefresh := time.Date(2026, 6, 4, 8, 0, 0, 0, time.UTC)
	cred := &Credential{
		ExpiresAt:   lastRefresh.Add(100 * time.Second),
		LastRefresh: lastRefresh,
	}

	if cred.NeedsRefresh(lastRefresh.Add(69*time.Second), 30*time.Second) {
		t.Fatalf("NeedsRefresh before earlier skew threshold = true, want false")
	}
	if !cred.NeedsRefresh(lastRefresh.Add(70*time.Second), 30*time.Second) {
		t.Fatalf("NeedsRefresh at earlier skew threshold = false, want true")
	}
}

func TestCredentialNeedsRefreshFallbackForInvalidLifecycle(t *testing.T) {
	now := time.Date(2026, 6, 4, 8, 0, 0, 0, time.UTC)
	cred := &Credential{
		ExpiresAt:   now.Add(time.Minute),
		LastRefresh: now.Add(2 * time.Minute),
	}

	if cred.NeedsRefresh(now, 30*time.Second) {
		t.Fatalf("NeedsRefresh outside fallback skew = true, want false")
	}
	if !cred.NeedsRefresh(now.Add(31*time.Second), 30*time.Second) {
		t.Fatalf("NeedsRefresh inside fallback skew = false, want true")
	}
}

func TestCredentialNeedsRefreshNilAndZeroExpiry(t *testing.T) {
	now := time.Date(2026, 6, 4, 8, 0, 0, 0, time.UTC)
	var nilCred *Credential
	if nilCred.NeedsRefresh(now, time.Minute) {
		t.Fatalf("nil credential NeedsRefresh = true, want false")
	}
	if (&Credential{}).NeedsRefresh(now, time.Minute) {
		t.Fatalf("zero expiry credential NeedsRefresh = true, want false")
	}
}
