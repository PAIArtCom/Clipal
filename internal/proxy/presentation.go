package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type SwitchPresentation struct {
	Label  string
	Detail string
}

type RequestOutcomePresentation struct {
	Result string
	Label  string
	Detail string
}

type ProviderAvailabilityPresentation struct {
	State  string
	Label  string
	Detail string
}

func DescribeProviderSwitch(from string, to string, reason string, status int) SwitchPresentation {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)

	label := strings.TrimSpace(from + " -> " + to)
	if from == "" && to != "" {
		label = "Switched to " + to
	} else if from != "" && to == "" {
		label = "Switched from " + from
	} else if from == "" && to == "" {
		label = "Provider switched"
	}

	return SwitchPresentation{
		Label:  label,
		Detail: switchReasonDetail(from, reason, status),
	}
}

func DescribeRequestOutcome(event RequestOutcomeEvent) RequestOutcomePresentation {
	if explicit := strings.TrimSpace(event.Result); explicit != "" {
		return describeExplicitRequestOutcome(event)
	}

	provider := strings.TrimSpace(event.Provider)
	switch {
	case event.Delivery == string(deliveryClientCanceled):
		return RequestOutcomePresentation{
			Result: "client_canceled",
			Label:  "Canceled by client",
			Detail: "The client disconnected before the response finished.",
		}
	case event.Delivery == string(deliveryCommittedPartial):
		return RequestOutcomePresentation{
			Result: "interrupted_after_partial_output",
			Label:  providerOutcomeLabel("Interrupted after partial output", provider),
			Detail: interruptedResponseDetail(provider, event.Cause),
		}
	case event.Delivery == string(deliveryCommittedComplete) &&
		(event.Protocol == string(protocolCompleted) || event.Protocol == string(protocolNotApplicable)):
		return RequestOutcomePresentation{
			Result: "completed",
			Label:  providerOutcomeLabel("Completed", provider),
			Detail: completedResponseDetail(provider, event.Status),
		}
	case event.Delivery == string(deliveryCommittedComplete) && event.Protocol == string(protocolFailed):
		return RequestOutcomePresentation{
			Result: "failed_response",
			Label:  providerOutcomeLabel("Failed response", provider),
			Detail: "The upstream stream ended with a protocol-level failure event.",
		}
	default:
		return RequestOutcomePresentation{
			Result: "incomplete_response",
			Label:  providerOutcomeLabel("Incomplete response", provider),
			Detail: incompleteResponseDetail(provider, event.Status),
		}
	}
}

func describeExplicitRequestOutcome(event RequestOutcomeEvent) RequestOutcomePresentation {
	detail := strings.TrimSpace(event.Detail)

	switch strings.TrimSpace(event.Result) {
	case "request_rejected":
		if detail == "" {
			detail = "The proxy rejected the request before contacting any provider."
		}
		return RequestOutcomePresentation{
			Result: "request_rejected",
			Label:  "Request rejected by proxy",
			Detail: detail,
		}
	case "all_providers_unavailable":
		if detail == "" {
			detail = "All configured providers are temporarily unavailable."
		}
		return RequestOutcomePresentation{
			Result: "all_providers_unavailable",
			Label:  "No provider available",
			Detail: detail,
		}
	case "advisory_request_unavailable":
		if detail == "" {
			detail = "The advisory request is temporarily unavailable. Primary traffic is unaffected."
		}
		return RequestOutcomePresentation{
			Result: "advisory_request_unavailable",
			Label:  "Advisory request unavailable",
			Detail: detail,
		}
	case "all_providers_failed":
		if detail == "" {
			detail = "Every provider attempt failed before a response could be completed."
		}
		return RequestOutcomePresentation{
			Result: "all_providers_failed",
			Label:  "All providers failed",
			Detail: detail,
		}
	case "failed_before_response":
		if detail == "" {
			detail = "The upstream request failed before any response body was sent."
		}
		return RequestOutcomePresentation{
			Result: "failed_before_response",
			Label:  providerOutcomeLabel("Failed before response started", strings.TrimSpace(event.Provider)),
			Detail: detail,
		}
	default:
		return RequestOutcomePresentation{
			Result: event.Result,
			Label:  strings.TrimSpace(event.Result),
			Detail: detail,
		}
	}
}

func DescribeProviderAvailability(name string, enabled bool, snap ProviderRuntimeSnapshot) ProviderAvailabilityPresentation {
	name = strings.TrimSpace(name)
	if !enabled {
		return ProviderAvailabilityPresentation{
			State:  "disabled",
			Label:  providerStateLabel(name, "disabled"),
			Detail: "Disabled in configuration.",
		}
	}

	if !snap.DeactivatedUntil.IsZero() {
		d := ""
		if wait := time.Until(snap.DeactivatedUntil).Truncate(time.Second); wait > 0 {
			d = wait.String()
		}
		state := "cooling_down"
		if isHardUnavailableReason(snap.DeactivatedReason) {
			state = "unavailable"
		}
		return ProviderAvailabilityPresentation{
			State:  state,
			Label:  providerStateLabel(name, providerStateShortLabel(state)),
			Detail: providerUnavailableDetail(snap.DeactivatedReason, d),
		}
	}

	if snap.KeyCount > 0 && snap.AvailableKeyCount == 0 {
		detail := "No API keys are currently available for this provider."
		if snap.KeyCount == 1 {
			detail = "The configured API key is currently unavailable."
		}
		return ProviderAvailabilityPresentation{
			State:  "unavailable",
			Label:  providerStateLabel(name, "no keys available"),
			Detail: detail,
		}
	}

	switch strings.TrimSpace(snap.CircuitState) {
	case "open":
		return ProviderAvailabilityPresentation{
			State:  "cooling_down",
			Label:  providerStateLabel(name, "cooling down"),
			Detail: providerCircuitDetail("open", snap.CircuitOpenIn.String()),
		}
	case "half_open":
		return ProviderAvailabilityPresentation{
			State:  "recovery_probe",
			Label:  providerStateLabel(name, "recovery probe"),
			Detail: "Clipal is sending limited traffic to verify the provider has recovered.",
		}
	default:
		return ProviderAvailabilityPresentation{
			State:  "available",
			Label:  name,
			Detail: "Available.",
		}
	}
}

func providerOutcomeLabel(prefix string, provider string) string {
	if provider == "" {
		return prefix
	}
	return fmt.Sprintf("%s via %s", prefix, provider)
}

func completedResponseDetail(provider string, status int) string {
	if provider == "" {
		return "The request completed successfully."
	}
	if status > 0 {
		return fmt.Sprintf("%s returned a complete response (%s).", provider, formatHTTPStatus(status))
	}
	return fmt.Sprintf("%s returned a complete response.", provider)
}

func incompleteResponseDetail(provider string, status int) string {
	if provider == "" {
		return "The response stream ended before completion."
	}
	if status > 0 {
		return fmt.Sprintf("%s ended the response before completion (%s).", provider, formatHTTPStatus(status))
	}
	return fmt.Sprintf("%s ended the response before completion.", provider)
}

func interruptedResponseDetail(provider string, cause string) string {
	subject := "The response stream"
	if provider != "" {
		subject = provider + " stream"
	}
	switch strings.TrimSpace(cause) {
	case "idle_timeout":
		return subject + " stalled after partial output."
	case "protocol_incomplete":
		return subject + " stopped after partial output and before completion."
	case "client_canceled":
		return "The client disconnected after receiving partial output."
	default:
		return subject + " was interrupted after partial output."
	}
}

func isHardUnavailableReason(reason string) bool {
	switch strings.TrimSpace(reason) {
	case "auth", "billing", "quota":
		return true
	default:
		return false
	}
}

func providerStateLabel(name string, state string) string {
	if name == "" {
		return state
	}
	return fmt.Sprintf("%s (%s)", name, state)
}

func providerStateShortLabel(state string) string {
	switch state {
	case "unavailable":
		return "unavailable"
	case "disabled":
		return "disabled"
	case "recovery_probe":
		return "recovery probe"
	default:
		return "cooling down"
	}
}

func providerUnavailableDetail(reason string, duration string) string {
	reason = strings.TrimSpace(reason)
	var detail string
	switch reason {
	case "auth":
		detail = "Authentication failed."
	case "billing":
		detail = "Billing is blocking requests."
	case "quota":
		detail = "Quota is exhausted."
	case "rate_limit":
		detail = "Rate limited."
	case "overloaded":
		detail = "Temporarily overloaded."
	case "server":
		detail = "Upstream server error."
	case "idle_timeout":
		detail = "Timed out waiting for upstream response."
	case "protocol_incomplete":
		detail = "The previous response ended before completion."
	case "network":
		detail = "Network request failed."
	default:
		detail = "Temporarily unavailable."
	}
	if strings.TrimSpace(duration) != "" {
		detail = fmt.Sprintf("%s Retry in %s.", strings.TrimSuffix(detail, "."), duration)
	}
	return detail
}

func providerCircuitDetail(state string, duration string) string {
	if state == "open" {
		if strings.TrimSpace(duration) != "" && duration != "0s" {
			return fmt.Sprintf("Circuit breaker is open. Retry in %s.", duration)
		}
		return "Circuit breaker is open."
	}
	return "Circuit breaker is probing the provider."
}

func reactivationDetail(reason string) string {
	switch strings.TrimSpace(reason) {
	case "auth":
		return "available again after the authentication quarantine expired"
	case "billing":
		return "available again after the billing quarantine expired"
	case "quota":
		return "available again after the quota quarantine expired"
	case "rate_limit":
		return "available again after the rate limit cooldown expired"
	case "overloaded":
		return "available again after the overload cooldown expired"
	case "server":
		return "available again after the server-error cooldown expired"
	case "idle_timeout":
		return "available again after the timeout cooldown expired"
	case "protocol_incomplete":
		return "available again after the incomplete-response cooldown expired"
	case "network":
		return "available again after the network cooldown expired"
	default:
		return "available again"
	}
}

func switchReasonDetail(from string, reason string, status int) string {
	subject := "the previous provider"
	if strings.TrimSpace(from) != "" {
		subject = from
	}

	switch strings.TrimSpace(reason) {
	case "idle_timeout":
		return fmt.Sprintf("Switched after %s timed out before any response body.", subject)
	case "network":
		return fmt.Sprintf("Switched after %s failed before any response body.", subject)
	case "auth":
		return fmt.Sprintf("Switched after %s returned %s for authentication.", subject, formatHTTPStatus(status))
	case "billing":
		return fmt.Sprintf("Switched after %s returned %s for billing.", subject, formatHTTPStatus(status))
	case "quota":
		return fmt.Sprintf("Switched after %s returned %s because quota was exhausted.", subject, formatHTTPStatus(status))
	case "rate_limit":
		return fmt.Sprintf("Switched after %s returned %s and was rate limited.", subject, formatHTTPStatus(status))
	case "overloaded":
		return fmt.Sprintf("Switched after %s returned %s and reported overload.", subject, formatHTTPStatus(status))
	case "server":
		return fmt.Sprintf("Switched after %s returned %s.", subject, formatHTTPStatus(status))
	default:
		if status > 0 {
			return fmt.Sprintf("Switched after %s returned %s.", subject, formatHTTPStatus(status))
		}
		return "Switched to another provider."
	}
}

func describeAttemptFailure(provider string, reason string, status int, beforeBody bool) string {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "provider"
	}

	if beforeBody {
		switch strings.TrimSpace(reason) {
		case "idle_timeout":
			return fmt.Sprintf("%s timed out before any response body", provider)
		default:
			return fmt.Sprintf("%s failed before any response body", provider)
		}
	}

	switch strings.TrimSpace(reason) {
	case "auth":
		return fmt.Sprintf("%s returned %s for authentication", provider, formatHTTPStatus(status))
	case "billing":
		return fmt.Sprintf("%s returned %s for billing", provider, formatHTTPStatus(status))
	case "quota":
		return fmt.Sprintf("%s returned %s because quota was exhausted", provider, formatHTTPStatus(status))
	case "rate_limit":
		return fmt.Sprintf("%s returned %s and was rate limited", provider, formatHTTPStatus(status))
	case "overloaded":
		return fmt.Sprintf("%s returned %s and reported overload", provider, formatHTTPStatus(status))
	case "server":
		return fmt.Sprintf("%s returned %s", provider, formatHTTPStatus(status))
	case "network":
		return fmt.Sprintf("%s had a network failure", provider)
	default:
		if status > 0 {
			return fmt.Sprintf("%s returned %s", provider, formatHTTPStatus(status))
		}
		return fmt.Sprintf("%s failed", provider)
	}
}

func formatHTTPStatus(status int) string {
	if status <= 0 {
		return "no HTTP status"
	}
	text := strings.TrimSpace(http.StatusText(status))
	if text == "" {
		return fmt.Sprintf("HTTP %d", status)
	}
	return fmt.Sprintf("HTTP %d %s", status, text)
}
