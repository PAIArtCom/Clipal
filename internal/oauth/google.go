package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Google OAuth and Cloud Code primitives used by Antigravity.
const (
	defaultGoogleAuthURL                  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL                 = "https://oauth2.googleapis.com/token"
	defaultGoogleUserInfoURL              = "https://www.googleapis.com/oauth2/v1/userinfo?alt=json"
	defaultGoogleScopeCloudPlatform       = "https://www.googleapis.com/auth/cloud-platform"
	defaultGoogleScopeUserInfoEmail       = "https://www.googleapis.com/auth/userinfo.email"
	defaultGoogleScopeUserInfoProfile     = "https://www.googleapis.com/auth/userinfo.profile"
	defaultGoogleOnboardAttempts          = 5
	defaultGoogleOnboardDelay             = 2 * time.Second
	defaultGoogleOperationPollDelay       = 5 * time.Second
	defaultGoogleOperationPollTimeout     = 90 * time.Second
	defaultGoogleOperationPollMaxAttempts = 18
	defaultGoogleCloudCodeRetryAttempts   = 3
	defaultGoogleCloudCodeRetryDelay      = 100 * time.Millisecond
)

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
}

type googleUserInfoResponse struct {
	Email string `json:"email"`
}

func exchangeGoogleToken(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*googleTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token googleTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func fetchGoogleUserEmail(ctx context.Context, client *http.Client, endpoint, accessToken string) (string, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return "", fmt.Errorf("missing access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var info googleUserInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	return strings.TrimSpace(info.Email), nil
}

func extractGoogleProjectID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	switch value := payload["cloudaicompanionProject"].(type) {
	case string:
		return strings.TrimSpace(value)
	case map[string]any:
		if id, ok := value["id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func extractGoogleProjectIDFromOnboard(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	response, ok := payload["response"].(map[string]any)
	if !ok {
		return ""
	}
	return extractGoogleProjectID(response)
}

func extractGoogleOperationName(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	name, _ := payload["name"].(string)
	return strings.TrimSpace(name)
}

func validateGoogleProjectID(projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	for _, r := range projectID {
		if r < '0' || r > '9' {
			return nil
		}
	}
	return fmt.Errorf("invalid Google Cloud project ID %q: use the string project ID, not the numeric project number", projectID)
}

func googleProjectIDFromOfficialEnv() string {
	for _, key := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"} {
		if v, ok := lookupNonEmptyEnv(key); ok {
			return v
		}
	}
	return ""
}

func googleCloudCodeShouldRetry(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, 499:
		return true
	default:
		return statusCode >= http.StatusInternalServerError && statusCode <= 599
	}
}

func googleCloudCodeRetryDelay(hdr http.Header, now time.Time) time.Duration {
	if d, ok := parseGoogleRetryAfter(hdr.Get("Retry-After"), now); ok && d > 0 {
		return d
	}
	return defaultGoogleCloudCodeRetryDelay
}

func parseGoogleRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if parsed, err := http.ParseTime(value); err == nil {
		if now.IsZero() {
			now = time.Now()
		}
		return parsed.Sub(now), true
	}
	return 0, false
}

func previousValue(cred *Credential, field func(*Credential) string) string {
	if cred == nil || field == nil {
		return ""
	}
	return strings.TrimSpace(field(cred))
}
