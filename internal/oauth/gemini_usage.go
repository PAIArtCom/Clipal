package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

type geminiUsageFetcher interface {
	FetchUsage(ctx context.Context, cred *Credential) (*GeminiUsageDetails, error)
}

type GeminiUsageDetails struct {
	Buckets []GeminiUsageBucket
	Groups  []GeminiUsageGroup
}

type GeminiUsageBucket struct {
	ModelID           string
	TokenType         string
	RemainingAmount   *int64
	RemainingFraction *float64
	ResetTime         time.Time
}

type GeminiUsageGroup struct {
	DisplayName string
	Description string
	Buckets     []GeminiUsageSummaryBucket
}

type GeminiUsageSummaryBucket struct {
	BucketID          string
	DisplayName       string
	Description       string
	RemainingFraction *float64
	ResetTime         time.Time
	Window            string
}

type geminiUsagePayload struct {
	Buckets []geminiUsageBucketPayload `json:"buckets"`
}

type geminiUsageBucketPayload struct {
	ModelID           string   `json:"modelId"`
	TokenType         string   `json:"tokenType"`
	RemainingAmount   string   `json:"remainingAmount"`
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         string   `json:"resetTime"`
}

type antigravityUsageSummaryPayload struct {
	Description string                         `json:"description"`
	Groups      []antigravityUsageSummaryGroup `json:"groups"`
}

type antigravityUsageSummaryGroup struct {
	DisplayName string                          `json:"displayName"`
	Description string                          `json:"description"`
	Buckets     []antigravityUsageSummaryBucket `json:"buckets"`
}

type antigravityUsageSummaryBucket struct {
	BucketID          string   `json:"bucketId"`
	DisplayName       string   `json:"displayName"`
	Description       string   `json:"description"`
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         string   `json:"resetTime"`
	Window            string   `json:"window"`
}

func (d *GeminiUsageDetails) Clone() *GeminiUsageDetails {
	if d == nil {
		return nil
	}
	clone := &GeminiUsageDetails{
		Buckets: make([]GeminiUsageBucket, 0, len(d.Buckets)),
		Groups:  make([]GeminiUsageGroup, 0, len(d.Groups)),
	}
	for _, bucket := range d.Buckets {
		clone.Buckets = append(clone.Buckets, bucket.clone())
	}
	for _, group := range d.Groups {
		clone.Groups = append(clone.Groups, group.clone())
	}
	return clone
}

func (b GeminiUsageBucket) clone() GeminiUsageBucket {
	clone := b
	if b.RemainingAmount != nil {
		amount := *b.RemainingAmount
		clone.RemainingAmount = &amount
	}
	if b.RemainingFraction != nil {
		fraction := *b.RemainingFraction
		clone.RemainingFraction = &fraction
	}
	return clone
}

func (g GeminiUsageGroup) clone() GeminiUsageGroup {
	clone := GeminiUsageGroup{
		DisplayName: g.DisplayName,
		Description: g.Description,
		Buckets:     make([]GeminiUsageSummaryBucket, 0, len(g.Buckets)),
	}
	for _, bucket := range g.Buckets {
		clone.Buckets = append(clone.Buckets, bucket.clone())
	}
	return clone
}

func (b GeminiUsageSummaryBucket) clone() GeminiUsageSummaryBucket {
	clone := b
	if b.RemainingFraction != nil {
		fraction := *b.RemainingFraction
		clone.RemainingFraction = &fraction
	}
	return clone
}

func (s *Service) GetGeminiUsage(ctx context.Context, ref string) (*GeminiUsageDetails, error) {
	return s.GetGeminiUsageWithHTTPClient(ctx, ref, nil)
}

func (s *Service) GetGeminiUsageWithHTTPClient(ctx context.Context, ref string, httpClient *http.Client) (*GeminiUsageDetails, error) {
	return s.GetGeminiUsageForProviderWithHTTPClient(ctx, config.OAuthProviderGemini, ref, httpClient)
}

func (s *Service) GetGeminiUsageForProviderWithHTTPClient(ctx context.Context, provider config.OAuthProvider, ref string, httpClient *http.Client) (*GeminiUsageDetails, error) {
	if s == nil {
		return nil, fmt.Errorf("oauth service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	provider = config.OAuthProvider(strings.TrimSpace(string(provider)))
	if provider == "" {
		provider = config.OAuthProviderGemini
	}

	cred, err := s.store.Load(provider, ref)
	if err != nil {
		return nil, err
	}

	cacheKey := geminiUsageCacheKey(provider, ref)
	if cached, ok := s.cachedGeminiUsage(cacheKey); ok {
		return cached, nil
	}

	details := &GeminiUsageDetails{}
	if strings.TrimSpace(cred.AccessToken) == "" || geminiCredentialProjectID(cred) == "" {
		return details, nil
	}

	if cached, ok := s.cachedGeminiUsage(cacheKey); ok {
		return cached, nil
	}

	wait, ok := s.beginGeminiUsageFetch(cacheKey)
	if ok {
		<-wait.done
		if wait.details == nil {
			if wait.err != nil {
				return nil, wait.err
			}
			return &GeminiUsageDetails{}, nil
		}
		return wait.details.Clone(), wait.err
	}

	refreshed, err := s.RefreshIfNeededWithHTTPClient(ctx, provider, ref, httpClient)
	if err != nil {
		s.finishGeminiUsageFetch(cacheKey, nil, err, false)
		return details, err
	}
	if refreshed != nil {
		cred = refreshed
	}

	client, ok := s.providerClient(provider)
	if !ok {
		err := fmt.Errorf("unsupported oauth provider %q", provider)
		s.finishGeminiUsageFetch(cacheKey, nil, err, false)
		return details, err
	}
	client = providerClientWithHTTPClient(client, httpClient)
	fetcher, ok := client.(geminiUsageFetcher)
	if !ok {
		err := fmt.Errorf("oauth provider %q does not support usage retrieval", provider)
		s.finishGeminiUsageFetch(cacheKey, nil, err, false)
		return details, err
	}

	fetched, err := fetcher.FetchUsage(ctx, cred)
	if fetched == nil && err == nil {
		fetched = &GeminiUsageDetails{}
	}
	s.finishGeminiUsageFetch(cacheKey, fetched, err, err == nil && fetched != nil)
	if fetched != nil {
		return fetched, err
	}
	return details, err
}

func (c *GeminiClient) FetchUsage(ctx context.Context, cred *Credential) (*GeminiUsageDetails, error) {
	if c == nil {
		return nil, fmt.Errorf("gemini client is nil")
	}
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	accessToken := strings.TrimSpace(cred.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("gemini credential %q has no access token", strings.TrimSpace(cred.Ref))
	}
	projectID := geminiCredentialProjectID(cred)
	if projectID == "" {
		return nil, fmt.Errorf("gemini credential %q has no project id", strings.TrimSpace(cred.Ref))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	reqBody, err := json.Marshal(map[string]any{
		"project": projectID,
	})
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(c.cloudCodeURL(), "/") + "/" + strings.Trim(c.cloudCodeVersion(), "/") + ":retrieveUserQuota"
	var lastErr error
	for attempt := 0; attempt < defaultGeminiCloudCodeRetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(reqBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", geminiCloudCodeUserAgent(defaultGeminiCloudCodeUserAgentModel))

		resp, err := c.httpClient().Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < defaultGeminiCloudCodeRetryAttempts {
				c.sleep(defaultGeminiCloudCodeRetryDelay)
				continue
			}
			return nil, err
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("gemini usage request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			if attempt+1 < defaultGeminiCloudCodeRetryAttempts && geminiCloudCodeShouldRetry(resp.StatusCode) {
				c.sleep(geminiCloudCodeRetryDelay(resp.Header, c.now()))
				continue
			}
			return nil, lastErr
		}

		var payload geminiUsagePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode gemini usage: %w", err)
		}
		return mapGeminiUsagePayload(payload), nil
	}
	return nil, lastErr
}

func mapGeminiUsagePayload(payload geminiUsagePayload) *GeminiUsageDetails {
	out := &GeminiUsageDetails{
		Buckets: make([]GeminiUsageBucket, 0, len(payload.Buckets)),
	}
	for _, bucket := range payload.Buckets {
		out.Buckets = append(out.Buckets, mapGeminiUsageBucket(bucket))
	}
	return out
}

func mapGeminiUsageBucket(payload geminiUsageBucketPayload) GeminiUsageBucket {
	out := GeminiUsageBucket{
		ModelID:   strings.TrimSpace(payload.ModelID),
		TokenType: strings.TrimSpace(payload.TokenType),
	}
	if amount, ok := parseGeminiRemainingAmount(payload.RemainingAmount); ok {
		out.RemainingAmount = &amount
	}
	if payload.RemainingFraction != nil {
		fraction := *payload.RemainingFraction
		out.RemainingFraction = &fraction
	}
	if resetTime, ok := parseGeminiResetTime(payload.ResetTime); ok {
		out.ResetTime = resetTime
	}
	return out
}

func parseGeminiRemainingAmount(value string) (int64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return amount, true
}

func parseGeminiResetTime(value string) (time.Time, bool) {
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

func (c *AntigravityClient) FetchUsage(ctx context.Context, cred *Credential) (*GeminiUsageDetails, error) {
	if c == nil {
		return nil, fmt.Errorf("antigravity client is nil")
	}
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	accessToken := strings.TrimSpace(cred.AccessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("antigravity credential %q has no access token", strings.TrimSpace(cred.Ref))
	}
	projectID := geminiCredentialProjectID(cred)
	if projectID == "" {
		return nil, fmt.Errorf("antigravity credential %q has no project id", strings.TrimSpace(cred.Ref))
	}
	if ctx == nil {
		ctx = context.Background()
	}

	reqBody, err := json.Marshal(map[string]any{
		"project": projectID,
	})
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(c.dailyCloudCodeURL(), "/") + "/" + strings.Trim(c.cloudCodeVersion(), "/") + ":retrieveUserQuota"
	var lastErr error
	for attempt := 0; attempt < defaultAntigravityCloudCodeRetry; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(reqBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", antigravityUserAgent())

		resp, err := c.httpClient().Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < defaultAntigravityCloudCodeRetry {
				c.sleep(defaultAntigravityCloudCodeRetryWait)
				continue
			}
			return nil, err
		}

		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("antigravity usage request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			if attempt+1 < defaultAntigravityCloudCodeRetry && geminiCloudCodeShouldRetry(resp.StatusCode) {
				c.sleep(geminiCloudCodeRetryDelay(resp.Header, c.now()))
				continue
			}
			return nil, lastErr
		}

		var payload geminiUsagePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode antigravity usage: %w", err)
		}
		details := mapGeminiUsagePayload(payload)
		if summary, err := c.fetchUsageSummary(ctx, accessToken, projectID); err == nil && summary != nil {
			details.Groups = summary.Groups
		}
		return details, nil
	}
	return nil, lastErr
}

func (c *AntigravityClient) fetchUsageSummary(ctx context.Context, accessToken string, projectID string) (*GeminiUsageDetails, error) {
	reqBody, err := json.Marshal(map[string]any{
		"project": strings.TrimSpace(projectID),
	})
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.dailyCloudCodeURL(), "/") + "/" + strings.Trim(c.cloudCodeVersion(), "/") + ":retrieveUserQuotaSummary"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", antigravityUserAgent())

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("antigravity usage summary request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload antigravityUsageSummaryPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode antigravity usage summary: %w", err)
	}
	return mapAntigravityUsageSummaryPayload(payload), nil
}

func mapAntigravityUsageSummaryPayload(payload antigravityUsageSummaryPayload) *GeminiUsageDetails {
	out := &GeminiUsageDetails{
		Groups: make([]GeminiUsageGroup, 0, len(payload.Groups)),
	}
	for _, group := range payload.Groups {
		mappedGroup := GeminiUsageGroup{
			DisplayName: strings.TrimSpace(group.DisplayName),
			Description: strings.TrimSpace(group.Description),
			Buckets:     make([]GeminiUsageSummaryBucket, 0, len(group.Buckets)),
		}
		for _, bucket := range group.Buckets {
			mappedGroup.Buckets = append(mappedGroup.Buckets, mapAntigravityUsageSummaryBucket(bucket))
		}
		out.Groups = append(out.Groups, mappedGroup)
	}
	return out
}

func mapAntigravityUsageSummaryBucket(payload antigravityUsageSummaryBucket) GeminiUsageSummaryBucket {
	out := GeminiUsageSummaryBucket{
		BucketID:    strings.TrimSpace(payload.BucketID),
		DisplayName: strings.TrimSpace(payload.DisplayName),
		Description: strings.TrimSpace(payload.Description),
		Window:      strings.TrimSpace(payload.Window),
	}
	if payload.RemainingFraction != nil {
		fraction := *payload.RemainingFraction
		out.RemainingFraction = &fraction
	}
	if resetTime, ok := parseGeminiResetTime(payload.ResetTime); ok {
		out.ResetTime = resetTime
	}
	return out
}

func geminiUsageCacheKey(provider config.OAuthProvider, ref string) string {
	provider = config.OAuthProvider(strings.TrimSpace(string(provider)))
	if provider == "" {
		provider = config.OAuthProviderGemini
	}
	return string(provider) + ":" + strings.TrimSpace(ref)
}

func (s *Service) cachedGeminiUsage(key string) (*GeminiUsageDetails, bool) {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.geminiUsageCache[key]
	if !ok {
		return nil, false
	}
	if s.geminiUsageTTL <= 0 || s.now().Sub(entry.fetchedAt) > s.geminiUsageTTL {
		delete(s.geminiUsageCache, key)
		return nil, false
	}
	if entry.details == nil {
		delete(s.geminiUsageCache, key)
		return nil, false
	}
	return entry.details.Clone(), true
}

func (s *Service) beginGeminiUsageFetch(key string) (*geminiUsageCall, bool) {
	if s == nil || strings.TrimSpace(key) == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if call, ok := s.geminiUsageCalls[key]; ok {
		return call, true
	}
	s.geminiUsageCalls[key] = &geminiUsageCall{done: make(chan struct{})}
	return nil, false
}

func (s *Service) finishGeminiUsageFetch(key string, details *GeminiUsageDetails, err error, cache bool) {
	if s == nil || strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	call, ok := s.geminiUsageCalls[key]
	if ok {
		delete(s.geminiUsageCalls, key)
	}
	if cache && details != nil {
		s.geminiUsageCache[key] = geminiUsageCacheEntry{
			fetchedAt: s.now(),
			details:   details.Clone(),
		}
	}
	s.mu.Unlock()

	if !ok {
		return
	}
	call.details = details.Clone()
	call.err = err
	close(call.done)
}

func (s *Service) invalidateGeminiUsageCache(ref string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for _, provider := range []config.OAuthProvider{config.OAuthProviderGemini, config.OAuthProviderAntigravity} {
		delete(s.geminiUsageCache, geminiUsageCacheKey(provider, ref))
	}
	s.mu.Unlock()
}
