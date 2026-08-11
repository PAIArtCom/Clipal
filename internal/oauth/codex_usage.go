package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PAIArtCom/Clipal/internal/config"
)

const codexUsageUserAgent = "codex-cli"

type codexUsageFetcher interface {
	FetchUsage(ctx context.Context, cred *Credential) (*CodexUsageDetails, error)
}

type CodexUsageDetails struct {
	PlanType     string
	Allowed      bool
	LimitReached bool
	Primary      *CodexUsageWindow
	Secondary    *CodexUsageWindow
	Additional   []CodexAdditionalRateLimit
}

type CodexAdditionalRateLimit struct {
	LimitID      string
	LimitName    string
	Allowed      bool
	LimitReached bool
	Primary      *CodexUsageWindow
	Secondary    *CodexUsageWindow
}

type CodexUsageWindow struct {
	UsedPercent   float64
	WindowMinutes int
	ResetsAt      time.Time
}

type codexUsagePayload struct {
	PlanType             string                      `json:"plan_type"`
	RateLimit            *codexUsageRateLimitDetails `json:"rate_limit"`
	AdditionalRateLimits []codexAdditionalLimit      `json:"additional_rate_limits"`

	// Older Codex surfaces used a dedicated code-review field. The current
	// ChatGPT usage endpoint exposes extra buckets through additional_rate_limits,
	// but keep accepting both shapes to avoid dropping useful quota data.
	CodeReviewRateLimit  *codexUsageRateLimitDetails `json:"code_review_rate_limit"`
	CodeReviewRateLimits *codexUsageRateLimitDetails `json:"code_review_rate_limits"`
}

type codexAdditionalLimit struct {
	LimitName      string                      `json:"limit_name"`
	MeteredFeature string                      `json:"metered_feature"`
	RateLimit      *codexUsageRateLimitDetails `json:"rate_limit"`
}

type codexUsageRateLimitDetails struct {
	Allowed         bool              `json:"allowed"`
	LimitReached    bool              `json:"limit_reached"`
	PrimaryWindow   *codexUsageWindow `json:"primary_window"`
	SecondaryWindow *codexUsageWindow `json:"secondary_window"`
	Primary         *codexUsageWindow `json:"primary"`
	Secondary       *codexUsageWindow `json:"secondary"`
}

type codexUsageWindow struct {
	UsedPercent       float64 `json:"used_percent"`
	LimitWindowSecs   int64   `json:"limit_window_seconds"`
	WindowMinutes     int64   `json:"window_minutes"`
	ResetAfterSeconds int64   `json:"reset_after_seconds"`
	ResetAt           int64   `json:"reset_at"`
	ResetsAt          int64   `json:"resets_at"`
}

func (s *Service) GetCodexUsage(ctx context.Context, ref string) (*CodexUsageDetails, error) {
	return s.GetCodexUsageWithHTTPClient(ctx, ref, nil)
}

func (s *Service) GetCodexUsageWithHTTPClient(ctx context.Context, ref string, httpClient *http.Client) (*CodexUsageDetails, error) {
	if s == nil {
		return nil, fmt.Errorf("oauth service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cred, err := s.store.Load(config.OAuthProviderCodex, ref)
	if err != nil {
		return nil, err
	}

	details := &CodexUsageDetails{
		PlanType: codexCredentialPlanType(cred),
	}

	if strings.TrimSpace(cred.AccessToken) == "" || strings.TrimSpace(cred.AccountID) == "" {
		return details, nil
	}

	refreshed, err := s.RefreshIfNeededWithHTTPClient(ctx, config.OAuthProviderCodex, ref, httpClient)
	if err != nil {
		return details, err
	}
	if refreshed != nil {
		cred = refreshed
		if details.PlanType == "" {
			details.PlanType = codexCredentialPlanType(cred)
		}
	}

	client, ok := s.providerClient(config.OAuthProviderCodex)
	if !ok {
		return details, fmt.Errorf("unsupported oauth provider %q", config.OAuthProviderCodex)
	}
	client = providerClientWithHTTPClient(client, httpClient)
	fetcher, ok := client.(codexUsageFetcher)
	if !ok {
		return details, fmt.Errorf("oauth provider %q does not support usage retrieval", config.OAuthProviderCodex)
	}

	fetched, err := fetcher.FetchUsage(ctx, cred)
	if fetched != nil {
		if fetched.PlanType == "" {
			fetched.PlanType = details.PlanType
		}
		return fetched, err
	}
	return details, err
}

func (c *CodexClient) FetchUsage(ctx context.Context, cred *Credential) (*CodexUsageDetails, error) {
	if c == nil {
		return nil, fmt.Errorf("codex client is nil")
	}
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	accessToken := strings.TrimSpace(cred.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("codex credential %q has no access token", strings.TrimSpace(cred.Ref))
	}
	accountID := strings.TrimSpace(cred.AccountID)
	if accountID == "" {
		return nil, fmt.Errorf("codex credential %q has no account id", strings.TrimSpace(cred.Ref))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.usageURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", codexUsageUserAgent)
	req.Header.Set("ChatGPT-Account-Id", accountID)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("codex usage request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload codexUsagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode codex usage: %w", err)
	}
	return mapCodexUsagePayload(payload, c.now()), nil
}

func mapCodexUsagePayload(payload codexUsagePayload, now time.Time) *CodexUsageDetails {
	out := &CodexUsageDetails{
		PlanType: strings.TrimSpace(payload.PlanType),
	}
	if payload.RateLimit != nil {
		out.Allowed = payload.RateLimit.Allowed
		out.LimitReached = payload.RateLimit.LimitReached
		out.Primary = mapCodexUsageWindow(payload.RateLimit.primaryWindow(), now)
		out.Secondary = mapCodexUsageWindow(payload.RateLimit.secondaryWindow(), now)
	}
	for _, item := range payload.AdditionalRateLimits {
		mapped := mapCodexAdditionalRateLimit(item, now)
		if mapped.Primary == nil && mapped.Secondary == nil {
			continue
		}
		out.Additional = append(out.Additional, mapped)
	}
	if payload.CodeReviewRateLimit != nil {
		out.Additional = appendCodexAdditionalIfMissing(out.Additional, CodexAdditionalRateLimit{
			LimitID:   "code_review",
			LimitName: "Code review",
			Primary:   mapCodexUsageWindow(payload.CodeReviewRateLimit.primaryWindow(), now),
			Secondary: mapCodexUsageWindow(payload.CodeReviewRateLimit.secondaryWindow(), now),
		})
	}
	if payload.CodeReviewRateLimits != nil {
		out.Additional = appendCodexAdditionalIfMissing(out.Additional, CodexAdditionalRateLimit{
			LimitID:   "code_review",
			LimitName: "Code review",
			Primary:   mapCodexUsageWindow(payload.CodeReviewRateLimits.primaryWindow(), now),
			Secondary: mapCodexUsageWindow(payload.CodeReviewRateLimits.secondaryWindow(), now),
		})
	}
	return out
}

func mapCodexAdditionalRateLimit(item codexAdditionalLimit, now time.Time) CodexAdditionalRateLimit {
	out := CodexAdditionalRateLimit{
		LimitID:   strings.TrimSpace(item.MeteredFeature),
		LimitName: strings.TrimSpace(item.LimitName),
	}
	if out.LimitID == "" {
		out.LimitID = out.LimitName
	}
	if item.RateLimit != nil {
		out.Allowed = item.RateLimit.Allowed
		out.LimitReached = item.RateLimit.LimitReached
		out.Primary = mapCodexUsageWindow(item.RateLimit.primaryWindow(), now)
		out.Secondary = mapCodexUsageWindow(item.RateLimit.secondaryWindow(), now)
	}
	return out
}

func (details *codexUsageRateLimitDetails) primaryWindow() *codexUsageWindow {
	if details == nil {
		return nil
	}
	if details.PrimaryWindow != nil {
		return details.PrimaryWindow
	}
	return details.Primary
}

func (details *codexUsageRateLimitDetails) secondaryWindow() *codexUsageWindow {
	if details == nil {
		return nil
	}
	if details.SecondaryWindow != nil {
		return details.SecondaryWindow
	}
	return details.Secondary
}

func mapCodexUsageWindow(window *codexUsageWindow, now time.Time) *CodexUsageWindow {
	if window == nil {
		return nil
	}
	windowMinutes := window.WindowMinutes
	if windowMinutes == 0 && window.LimitWindowSecs > 0 {
		windowMinutes = window.LimitWindowSecs / 60
	}
	resetAt := window.ResetAt
	if resetAt == 0 {
		resetAt = window.ResetsAt
	}
	out := &CodexUsageWindow{
		UsedPercent:   window.UsedPercent,
		WindowMinutes: int(windowMinutes),
	}
	if resetAt > 0 {
		out.ResetsAt = time.Unix(resetAt, 0).UTC()
	} else if window.ResetAfterSeconds > 0 {
		out.ResetsAt = now.Add(time.Duration(window.ResetAfterSeconds) * time.Second).UTC()
	}
	return out
}

func appendCodexAdditionalIfMissing(items []CodexAdditionalRateLimit, item CodexAdditionalRateLimit) []CodexAdditionalRateLimit {
	if item.Primary == nil && item.Secondary == nil {
		return items
	}
	itemID := strings.ToLower(strings.TrimSpace(item.LimitID))
	for _, existing := range items {
		if strings.ToLower(strings.TrimSpace(existing.LimitID)) == itemID && itemID != "" {
			return items
		}
	}
	return append(items, item)
}

func codexCredentialPlanType(cred *Credential) string {
	if cred == nil || cred.Metadata == nil {
		return ""
	}
	if planType := parseCodexPlanType(cred.Metadata["id_token"]); planType != "" {
		return planType
	}
	return strings.TrimSpace(cred.Metadata["plan_type"])
}
