package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

const (
	claudeUsageUserAgent = "claude-cli/2.1.161 (external, sdk-cli)"
	claudeUsageBeta      = "oauth-2025-04-20"
)

type claudeUsageFetcher interface {
	FetchUsage(ctx context.Context, cred *Credential) (*ClaudeUsageDetails, error)
}

type ClaudeUsageDetails struct {
	PlanType          string
	FiveHour          *ClaudeUsageWindow
	SevenDay          *ClaudeUsageWindow
	SevenDayOAuthApps *ClaudeUsageWindow
	SevenDayOpus      *ClaudeUsageWindow
	SevenDaySonnet    *ClaudeUsageWindow
	ExtraUsage        *ClaudeExtraUsage
}

type ClaudeUsageWindow struct {
	Utilization float64
	ResetsAt    time.Time
}

type ClaudeExtraUsage struct {
	IsEnabled    bool
	MonthlyLimit *int64
	UsedCredits  *int64
	Utilization  *float64
}

type claudeUsagePayload struct {
	PlanType              string                    `json:"plan_type"`
	SubscriptionType      string                    `json:"subscription_type"`
	SubscriptionTypeCamel string                    `json:"subscriptionType"`
	FiveHour              *claudeUsageWindowPayload `json:"five_hour"`
	SevenDay              *claudeUsageWindowPayload `json:"seven_day"`
	SevenDayOAuthApps     *claudeUsageWindowPayload `json:"seven_day_oauth_apps"`
	SevenDayOpus          *claudeUsageWindowPayload `json:"seven_day_opus"`
	SevenDaySonnet        *claudeUsageWindowPayload `json:"seven_day_sonnet"`
	ExtraUsage            *claudeExtraUsagePayload  `json:"extra_usage"`
}

type claudeUsageWindowPayload struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type claudeExtraUsagePayload struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit *int64   `json:"monthly_limit"`
	UsedCredits  *int64   `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
}

func (s *Service) GetClaudeUsage(ctx context.Context, ref string) (*ClaudeUsageDetails, error) {
	return s.GetClaudeUsageWithHTTPClient(ctx, ref, nil)
}

func (s *Service) GetClaudeUsageWithHTTPClient(ctx context.Context, ref string, httpClient *http.Client) (*ClaudeUsageDetails, error) {
	if s == nil {
		return nil, fmt.Errorf("oauth service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cred, err := s.store.Load(config.OAuthProviderClaude, ref)
	if err != nil {
		return nil, err
	}

	details := &ClaudeUsageDetails{}
	if strings.TrimSpace(cred.AccessToken) == "" {
		return details, nil
	}

	refreshed, err := s.RefreshIfNeededWithHTTPClient(ctx, config.OAuthProviderClaude, ref, httpClient)
	if err != nil {
		return details, err
	}
	if refreshed != nil {
		cred = refreshed
	}

	client, ok := s.providerClient(config.OAuthProviderClaude)
	if !ok {
		return details, fmt.Errorf("unsupported oauth provider %q", config.OAuthProviderClaude)
	}
	client = providerClientWithHTTPClient(client, httpClient)
	fetcher, ok := client.(claudeUsageFetcher)
	if !ok {
		return details, fmt.Errorf("oauth provider %q does not support usage retrieval", config.OAuthProviderClaude)
	}

	fetched, err := fetcher.FetchUsage(ctx, cred)
	if fetched != nil {
		return fetched, err
	}
	return details, err
}

func (c *ClaudeClient) FetchUsage(ctx context.Context, cred *Credential) (*ClaudeUsageDetails, error) {
	if c == nil {
		return nil, fmt.Errorf("claude client is nil")
	}
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	accessToken := strings.TrimSpace(cred.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("claude credential %q has no access token", strings.TrimSpace(cred.Ref))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.usageURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Anthropic-Beta", claudeUsageBeta)
	req.Header.Set("User-Agent", claudeUsageUserAgent)

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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("claude usage request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload claudeUsagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode claude usage: %w", err)
	}
	return mapClaudeUsagePayload(payload), nil
}

func mapClaudeUsagePayload(payload claudeUsagePayload) *ClaudeUsageDetails {
	return &ClaudeUsageDetails{
		PlanType:          claudeUsagePlanType(payload),
		FiveHour:          mapClaudeUsageWindow(payload.FiveHour),
		SevenDay:          mapClaudeUsageWindow(payload.SevenDay),
		SevenDayOAuthApps: mapClaudeUsageWindow(payload.SevenDayOAuthApps),
		SevenDayOpus:      mapClaudeUsageWindow(payload.SevenDayOpus),
		SevenDaySonnet:    mapClaudeUsageWindow(payload.SevenDaySonnet),
		ExtraUsage:        mapClaudeExtraUsage(payload.ExtraUsage),
	}
}

func mapClaudeUsageWindow(payload *claudeUsageWindowPayload) *ClaudeUsageWindow {
	if payload == nil || payload.Utilization == nil {
		return nil
	}
	window := &ClaudeUsageWindow{
		Utilization: *payload.Utilization,
	}
	if resetAt, ok := parseClaudeUsageResetTime(payload.ResetsAt); ok {
		window.ResetsAt = resetAt
	}
	return window
}

func mapClaudeExtraUsage(payload *claudeExtraUsagePayload) *ClaudeExtraUsage {
	if payload == nil {
		return nil
	}
	return &ClaudeExtraUsage{
		IsEnabled:    payload.IsEnabled,
		MonthlyLimit: payload.MonthlyLimit,
		UsedCredits:  payload.UsedCredits,
		Utilization:  payload.Utilization,
	}
}

func claudeUsagePlanType(payload claudeUsagePayload) string {
	for _, candidate := range []string{payload.SubscriptionType, payload.SubscriptionTypeCamel, payload.PlanType} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

func parseClaudeUsageResetTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	if parsed, err := http.ParseTime(value); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}
