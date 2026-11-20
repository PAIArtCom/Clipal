package diagnostic

import (
	"regexp"
	"strings"
)

// Message prepares a short, single-line diagnostic for display or copying.
func Message(message string, maxRunes int, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
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
	if len(r) > maxRunes {
		message = string(r[:maxRunes]) + "…"
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
