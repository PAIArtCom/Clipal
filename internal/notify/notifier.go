package notify

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
	"github.com/gen2brain/beeep"
)

const (
	dedupeWindow  = 30 * time.Second
	maxPerMinute  = 6
	maxMsgRunes   = 280
	queueCapacity = 64
)

var (
	sendTimeout     = 2 * time.Second
	cleanupInterval = 5 * time.Minute
	disableCooldown = 5 * time.Minute
)

//go:embed clipal.png
var defaultIconPNG []byte

type event struct {
	title   string
	message string
	key     string
}

type Notifier struct {
	enabled        bool
	minLevel       config.LogLevel
	providerSwitch bool

	send func(title, message string, icon any) error

	ch   chan event
	stop chan struct{}
	done chan struct{}
}

var (
	mu      sync.RWMutex
	current *Notifier
)

func Configure(cfg config.NotificationsConfig) {
	ConfigureWithSender(cfg, platformSender(beeep.Notify))
}

func safeSender(sender func(title, message string, icon any) error) func(title, message string, icon any) (err error) {
	if sender == nil {
		return nil
	}
	return func(title, message string, icon any) (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("notification sender panicked: %v", r)
			}
		}()
		return sender(title, message, icon)
	}
}

func ConfigureWithSender(cfg config.NotificationsConfig, sender func(title, message string, icon any) error) {
	mu.Lock()
	defer mu.Unlock()

	if current != nil {
		current.shutdownLocked()
		current = nil
	}

	if !cfg.Enabled {
		return
	}

	providerSwitch := true
	if cfg.ProviderSwitch != nil {
		providerSwitch = *cfg.ProviderSwitch
	}
	minLevel := cfg.MinLevel
	if strings.TrimSpace(string(minLevel)) == "" {
		minLevel = config.LogLevelError
	}

	n := &Notifier{
		enabled:        true,
		minLevel:       minLevel,
		providerSwitch: providerSwitch,
		send:           safeSender(sender),
		ch:             make(chan event, queueCapacity),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
	current = n
	go n.loop()
}

func Shutdown() {
	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		return
	}
	current.shutdownLocked()
	current = nil
}

func (n *Notifier) shutdownLocked() {
	select {
	case <-n.stop:
		// already closed
	default:
		close(n.stop)
	}
	select {
	case <-n.done:
	case <-time.After(5 * time.Second):
	}
}

func get() *Notifier {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func ProviderSwitched(client string, summary string, detail string) {
	n := get()
	if n == nil || !n.enabled || !n.providerSwitch {
		return
	}
	client = strings.TrimSpace(client)
	summary = strings.TrimSpace(summary)
	detail = strings.TrimSpace(detail)
	if client == "" || summary == "" {
		return
	}

	msg := fmt.Sprintf("%s: %s", client, summary)
	if detail != "" {
		msg = fmt.Sprintf("%s. %s", msg, detail)
	}

	n.enqueue("clipal", msg, "switch:"+client+":"+summary)
}

func LogHook(levelStr string, message string) {
	n := get()
	if n == nil || !n.enabled {
		return
	}
	if !n.shouldNotifyLog(levelStr) {
		return
	}
	levelStr = strings.ToUpper(strings.TrimSpace(levelStr))
	msg := normalizeMessage(message)
	if msg == "" {
		return
	}
	title := "clipal " + levelStr
	key := "log:" + levelStr + ":" + msg
	n.enqueue(title, msg, key)
}

func (n *Notifier) shouldNotifyLog(levelStr string) bool {
	min := levelRank(n.minLevel)
	if min < 0 {
		min = levelRank(config.LogLevelError)
	}
	return levelRankFromString(levelStr) >= min
}

func levelRank(l config.LogLevel) int {
	switch l {
	case config.LogLevelDebug:
		return 0
	case config.LogLevelInfo:
		return 1
	case config.LogLevelWarn:
		return 2
	case config.LogLevelError:
		return 3
	default:
		return -1
	}
}

func levelRankFromString(levelStr string) int {
	switch strings.ToUpper(strings.TrimSpace(levelStr)) {
	case "DEBUG":
		return 0
	case "INFO":
		return 1
	case "WARN", "WARNING":
		return 2
	case "ERROR":
		return 3
	default:
		return -1
	}
}

func normalizeMessage(message string) string {
	message = redactSensitive(message)
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return ""
	}
	r := []rune(message)
	if len(r) > maxMsgRunes {
		message = string(r[:maxMsgRunes]) + "…"
	}
	return message
}

type redactor struct {
	pattern     *regexp.Regexp
	replacement string
}

var sensitiveRedactors = []redactor{
	{regexp.MustCompile(`(?i)\b(bearer)\s+([^\s]+)`), "$1 [redacted]"},
	{regexp.MustCompile(`\bsk-[a-zA-Z0-9_-]{10,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_-]{10,}\b`), "[redacted]"},
	{regexp.MustCompile(`\bsk-or-[a-zA-Z0-9_-]{10,}\b`), "[redacted]"},
	{regexp.MustCompile(`(?i)\b(api[_-]?key)\s*[:=]\s*([^\s]+)`), "$1=[redacted]"},
	{regexp.MustCompile(`(?i)([?&](?:api[_-]?key|token|access[_-]?token)=)([^&\s]+)`), "$1[redacted]"},
}

func redactSensitive(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	redacted := message
	for _, r := range sensitiveRedactors {
		redacted = r.pattern.ReplaceAllString(redacted, r.replacement)
	}
	return redacted
}

func (n *Notifier) enqueue(title string, message string, key string) {
	if title = strings.TrimSpace(title); title == "" {
		title = "clipal"
	}
	message = normalizeMessage(message)
	if message == "" {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = title + ":" + message
	}

	select {
	case n.ch <- event{title: title, message: message, key: key}:
	default:
		// best-effort: drop if overloaded
	}
}

func cleanupLastSent(lastSent map[string]time.Time, now time.Time) {
	cutoff := now.Add(-2 * dedupeWindow)
	for k, t := range lastSent {
		if t.Before(cutoff) {
			delete(lastSent, k)
		}
	}
}

func logSendError(err error, lastErrLog *time.Time) {
	if err == nil {
		return
	}
	now := time.Now()
	if lastErrLog != nil && !lastErrLog.IsZero() && now.Sub(*lastErrLog) < time.Minute {
		return
	}
	if lastErrLog != nil {
		*lastErrLog = now
	}
	fmt.Fprintf(os.Stderr, "clipal: notification failed: %v\n", err)
}

func notificationIcon() any {
	if len(defaultIconPNG) > 0 {
		return defaultIconPNG
	}
	return ""
}

func (n *Notifier) loop() {
	defer close(n.done)

	lastSent := make(map[string]time.Time)
	windowStart := time.Now()
	sent := 0
	disabledUntil := time.Time{}
	inFlight := false
	var inFlightDone chan error
	var lastErrLog time.Time

	cleanupTicker := time.NewTicker(cleanupInterval)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-n.stop:
			return
		case <-cleanupTicker.C:
			now := time.Now()
			cleanupLastSent(lastSent, now)
			if inFlight && inFlightDone != nil {
				select {
				case err := <-inFlightDone:
					inFlight = false
					inFlightDone = nil
					logSendError(err, &lastErrLog)
				default:
				}
			}
		case ev := <-n.ch:
			now := time.Now()
			if now.Sub(windowStart) >= time.Minute {
				windowStart = now
				sent = 0
			}
			if sent >= maxPerMinute {
				continue
			}
			if t, ok := lastSent[ev.key]; ok && now.Sub(t) < dedupeWindow {
				continue
			}
			lastSent[ev.key] = now
			sent++

			if inFlight && inFlightDone != nil {
				select {
				case err := <-inFlightDone:
					inFlight = false
					inFlightDone = nil
					logSendError(err, &lastErrLog)
				default:
				}
			}

			if n.send == nil || inFlight {
				continue
			}
			if !disabledUntil.IsZero() && now.Before(disabledUntil) {
				continue
			}

			done := make(chan error, 1)
			inFlight = true
			inFlightDone = done
			go func() {
				done <- n.send(ev.title, ev.message, notificationIcon())
			}()

			select {
			case err := <-done:
				inFlight = false
				inFlightDone = nil
				logSendError(err, &lastErrLog)
			case <-time.After(sendTimeout):
				disabledUntil = now.Add(disableCooldown)
				logSendError(fmt.Errorf("notification timed out after %s; disabling for %s", sendTimeout, disableCooldown), &lastErrLog)
			}
		}
	}
}
