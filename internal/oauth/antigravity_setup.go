package oauth

import (
	"fmt"
	"net/http"
	"strings"
)

// AntigravitySetupError contains safe setup facts, never tokens, verification
// URLs or raw upstream response bodies. It may be returned to API clients.
type AntigravitySetupError struct {
	Kind        string
	TierID      string
	ReasonCodes []string
}

func (e *AntigravitySetupError) Error() string {
	message := "Antigravity account setup is incomplete"
	tier := safeCodeAssistIdentifier(e.TierID)
	switch e.Kind {
	case "project_required":
		message = fmt.Sprintf("Antigravity %s requires a user-provided Google Cloud project", tier)
	case "validation_required":
		message = "Antigravity account validation is required"
	case "tier_unavailable":
		message = fmt.Sprintf("Antigravity %s is unavailable for this account", tier)
	}
	var codes []string
	for _, code := range e.ReasonCodes {
		if value := safeCodeAssistIdentifier(code); value != "" {
			codes = append(codes, value)
		}
	}
	if len(codes) > 0 {
		message += "; unavailable tier reasons: " + strings.Join(codes, ", ")
	}
	return message
}

func (e *AntigravitySetupError) HTTPStatus() int {
	if e.Kind == "project_required" {
		return http.StatusConflict
	}
	return http.StatusForbidden
}

func safeCodeAssistIdentifier(value string) string {
	if len(value) > 64 {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return ""
		}
	}
	return value
}

func antigravityProjectRequirement(payload map[string]any, tierID string) error {
	var allCodes, selectedCodes []string
	ineligible, _ := payload["ineligibleTiers"].([]any)
	for _, raw := range ineligible {
		tier, _ := raw.(map[string]any)
		code, _ := tier["reasonCode"].(string)
		code = safeCodeAssistIdentifier(strings.TrimSpace(code))
		if code == "" {
			continue
		}
		allCodes = append(allCodes, code)
		id, _ := tier["tierId"].(string)
		if id == "" || id == tierID {
			selectedCodes = append(selectedCodes, code)
		}
	}
	for _, code := range selectedCodes {
		if code == "VALIDATION_REQUIRED" {
			return &AntigravitySetupError{Kind: "validation_required", TierID: tierID, ReasonCodes: selectedCodes}
		}
	}
	allowed, _ := payload["allowedTiers"].([]any)
	for _, raw := range allowed {
		tier, _ := raw.(map[string]any)
		id, _ := tier["id"].(string)
		required, _ := tier["userDefinedCloudaicompanionProject"].(bool)
		if id == tierID && required {
			return &AntigravitySetupError{Kind: "project_required", TierID: tierID, ReasonCodes: allCodes}
		}
	}
	if len(selectedCodes) > 0 {
		return &AntigravitySetupError{Kind: "tier_unavailable", TierID: tierID, ReasonCodes: selectedCodes}
	}
	return nil
}
