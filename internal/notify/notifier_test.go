package notify

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

func TestCleanupLastSentRemovesExpired(t *testing.T) {
	now := time.Now()
	m := map[string]time.Time{
		"keep": now.Add(-dedupeWindow),
		"drop": now.Add(-3 * dedupeWindow),
	}
	cleanupLastSent(m, now)
	if _, ok := m["drop"]; ok {
		t.Fatalf("expected expired key to be removed")
	}
	if _, ok := m["keep"]; !ok {
		t.Fatalf("expected recent key to remain")
	}
}

func TestNormalizeMessageRedactsSensitiveData(t *testing.T) {
	msg := normalizeMessage("Authorization: Bearer SECRET_TOKEN_123")
	if strings.Contains(msg, "SECRET_TOKEN_123") {
		t.Fatalf("expected bearer token to be redacted, got %q", msg)
	}
	if !strings.Contains(strings.ToLower(msg), "bearer [redacted]") {
		t.Fatalf("expected redaction marker, got %q", msg)
	}

	msg = normalizeMessage("api_key=sk-ant-abcdefghijklmnopqrstuvwxyz")
	if strings.Contains(msg, "sk-ant-") && strings.Contains(msg, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("expected api key to be redacted, got %q", msg)
	}
}

func TestShutdownDoesNotBlockOnSender(t *testing.T) {
	oldSendTimeout := sendTimeout
	sendTimeout = 10 * time.Millisecond
	defer func() { sendTimeout = oldSendTimeout }()

	called := make(chan struct{})
	sender := func(title, message string, icon any) error {
		select {
		case <-called:
		default:
			close(called)
		}
		select {}
	}

	ps := true
	cfg := config.NotificationsConfig{
		Enabled:        true,
		MinLevel:       config.LogLevelError,
		ProviderSwitch: &ps,
	}

	ConfigureWithSender(cfg, sender)
	LogHook("ERROR", "test error")

	select {
	case <-called:
	case <-time.After(200 * time.Millisecond):
		Shutdown()
		t.Fatalf("sender was not called")
	}

	start := time.Now()
	Shutdown()
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("Shutdown took too long: %s", time.Since(start))
	}
}

func TestTimeoutDoesNotPermanentlyDisableNotifications(t *testing.T) {
	oldSendTimeout := sendTimeout
	oldDisableCooldown := disableCooldown
	sendTimeout = 5 * time.Millisecond
	disableCooldown = 10 * time.Millisecond
	defer func() {
		sendTimeout = oldSendTimeout
		disableCooldown = oldDisableCooldown
	}()

	var muCount sync.Mutex
	calls := 0
	sender := func(title, message string, icon any) error {
		muCount.Lock()
		calls++
		n := calls
		muCount.Unlock()
		if n == 1 {
			time.Sleep(20 * time.Millisecond)
			return nil
		}
		return nil
	}

	ps := true
	cfg := config.NotificationsConfig{
		Enabled:        true,
		MinLevel:       config.LogLevelError,
		ProviderSwitch: &ps,
	}
	ConfigureWithSender(cfg, sender)
	defer Shutdown()

	LogHook("ERROR", "first error")
	time.Sleep(35 * time.Millisecond) // allow in-flight send to finish and cooldown to expire
	LogHook("ERROR", "second error")
	time.Sleep(10 * time.Millisecond)

	muCount.Lock()
	got := calls
	muCount.Unlock()
	if got < 2 {
		t.Fatalf("expected sender to be called at least twice, got %d", got)
	}
}

func TestSenderPanicDoesNotCrashProcess(t *testing.T) {
	oldSendTimeout := sendTimeout
	sendTimeout = 10 * time.Millisecond
	defer func() { sendTimeout = oldSendTimeout }()

	var once sync.Once
	called := make(chan struct{})
	sender := func(title, message string, icon any) error {
		once.Do(func() { close(called) })
		panic("boom")
	}

	ps := true
	cfg := config.NotificationsConfig{
		Enabled:        true,
		MinLevel:       config.LogLevelError,
		ProviderSwitch: &ps,
	}

	ConfigureWithSender(cfg, sender)
	defer Shutdown()

	LogHook("ERROR", "test error")

	select {
	case <-called:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected sender to be called")
	}
}

func TestNotifierPassesEmbeddedIconBytes(t *testing.T) {
	ps := true
	cfg := config.NotificationsConfig{
		Enabled:        true,
		MinLevel:       config.LogLevelError,
		ProviderSwitch: &ps,
	}

	got := make(chan any, 1)
	sender := func(title, message string, icon any) error {
		got <- icon
		return nil
	}

	ConfigureWithSender(cfg, sender)
	defer Shutdown()

	LogHook("ERROR", "test error")

	select {
	case icon := <-got:
		b, ok := icon.([]byte)
		if !ok {
			t.Fatalf("expected []byte icon, got %T", icon)
		}
		if len(b) == 0 {
			t.Fatalf("expected embedded icon bytes")
		}
		if len(defaultIconPNG) == 0 {
			t.Fatalf("expected embedded icon fixture to be loaded")
		}
		if string(b) != string(defaultIconPNG) {
			t.Fatalf("expected embedded icon bytes to match fixture")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected sender to be called")
	}
}
