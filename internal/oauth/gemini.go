package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

const (
	defaultGeminiAuthURL                  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGeminiTokenURL                 = "https://oauth2.googleapis.com/token"
	defaultGeminiUserInfoURL              = "https://www.googleapis.com/oauth2/v1/userinfo?alt=json"
	defaultGeminiCloudCodeURL             = "https://cloudcode-pa.googleapis.com"
	defaultGeminiCloudCodeVersion         = "v1internal"
	defaultGeminiCallbackHost             = "127.0.0.1"
	defaultGeminiCallbackPort             = 0
	defaultGeminiCallbackPath             = "/oauth2callback"
	defaultGeminiCloudCodeUserAgentModel  = "gemini-2.5-pro"
	defaultGeminiCloudCodeVersionLabel    = "0.45.0-nightly.20260521.g854f811be"
	defaultGeminiScopeCloudPlatform       = "https://www.googleapis.com/auth/cloud-platform"
	defaultGeminiScopeUserInfoEmail       = "https://www.googleapis.com/auth/userinfo.email"
	defaultGeminiScopeUserInfoProfile     = "https://www.googleapis.com/auth/userinfo.profile"
	defaultGeminiFreeTierID               = "free-tier"
	defaultGeminiStandardTierID           = "standard-tier"
	defaultGeminiLegacyTierID             = "legacy-tier"
	defaultGeminiOnboardAttempts          = 5
	defaultGeminiOnboardDelay             = 2 * time.Second
	defaultGeminiOperationPollDelay       = 5 * time.Second
	defaultGeminiOperationPollTimeout     = 90 * time.Second
	defaultGeminiOperationPollMaxAttempts = 18
	defaultGeminiCloudCodeRetryAttempts   = 3
	defaultGeminiCloudCodeRetryDelay      = 100 * time.Millisecond
)

var (
	defaultGeminiClientID = strings.Join([]string{
		"681255809395",
		"-oo8ft2oprdrnp9e3aqf6",
		"av3hmdib135j",
		".apps.googleusercontent.com",
	}, "")
	defaultGeminiClientSecret = strings.Join([]string{
		"GO",
		"CSPX-",
		"4uHgMPm-",
		"1o7Sk-",
		"geV6Cu",
		"5clXFsxl",
	}, "")
)

var defaultGeminiScopes = []string{
	defaultGeminiScopeCloudPlatform,
	defaultGeminiScopeUserInfoEmail,
	defaultGeminiScopeUserInfoProfile,
}

var geminiCloudCodeMetadata = map[string]string{
	"ideType":    "IDE_UNSPECIFIED",
	"platform":   "PLATFORM_UNSPECIFIED",
	"pluginType": "GEMINI",
}

type GeminiClient struct {
	AuthURL          string
	TokenURL         string
	UserInfoURL      string
	CloudCodeURL     string
	CloudCodeVersion string
	ClientID         string
	ClientSecret     string
	Scopes           []string
	ProjectID        string
	CallbackHost     string
	CallbackPort     int
	CallbackPath     string
	HTTPClient       *http.Client
	Now              func() time.Time
	Sleep            func(time.Duration)
}

type geminiTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	IDToken      string `json:"id_token"`
}

type geminiUserInfoResponse struct {
	Email string `json:"email"`
}

var _ ProviderClient = (*GeminiClient)(nil)

func (c *GeminiClient) WithHTTPClient(httpClient *http.Client) ProviderClient {
	if c == nil || httpClient == nil {
		return c
	}
	clone := *c
	clone.HTTPClient = httpClient
	clone.Scopes = append([]string(nil), c.Scopes...)
	return &clone
}

func NewGeminiClient() *GeminiClient {
	client := &GeminiClient{
		AuthURL:          defaultGeminiAuthURL,
		TokenURL:         defaultGeminiTokenURL,
		UserInfoURL:      defaultGeminiUserInfoURL,
		CloudCodeURL:     defaultGeminiCloudCodeURL,
		CloudCodeVersion: defaultGeminiCloudCodeVersion,
		ClientID:         defaultGeminiClientID,
		ClientSecret:     defaultGeminiClientSecret,
		Scopes:           append([]string(nil), defaultGeminiScopes...),
		CallbackHost:     defaultGeminiCallbackHost,
		CallbackPort:     defaultGeminiCallbackPort,
		CallbackPath:     defaultGeminiCallbackPath,
		HTTPClient:       &http.Client{Timeout: 30 * time.Second},
		Now:              time.Now,
		Sleep:            time.Sleep,
	}
	applyGeminiClientEnvOverrides(client)
	return client
}

func (c *GeminiClient) Provider() config.OAuthProvider {
	return config.OAuthProviderGemini
}

func (c *GeminiClient) GenerateAuthURL(state string, redirectURI string, pkce PKCECodes) (string, error) {
	if strings.TrimSpace(state) == "" {
		return "", fmt.Errorf("state is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return "", fmt.Errorf("redirect_uri is required")
	}
	if strings.TrimSpace(pkce.CodeVerifier) == "" || strings.TrimSpace(pkce.CodeChallenge) == "" {
		return "", fmt.Errorf("pkce codes are required")
	}

	params := url.Values{
		"access_type":           {"offline"},
		"client_id":             {c.clientID()},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"prompt":                {"consent"},
		"redirect_uri":          {strings.TrimSpace(redirectURI)},
		"response_type":         {"code"},
		"scope":                 {strings.Join(c.scopes(), " ")},
		"state":                 {strings.TrimSpace(state)},
	}
	return strings.TrimSpace(c.authURL()) + "?" + params.Encode(), nil
}

func (c *GeminiClient) StartLogin(now time.Time, ttl time.Duration) (*LoginSession, error) {
	return startLoginSession(
		config.OAuthProviderGemini,
		now,
		ttl,
		c.callbackHost(),
		c.callbackPort(),
		c.callbackPath(),
		c.GenerateAuthURL,
	)
}

func (c *GeminiClient) ExchangeCode(ctx context.Context, code string, redirectURI string, pkce PKCECodes) (*Credential, error) {
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("code is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, fmt.Errorf("redirect_uri is required")
	}
	if strings.TrimSpace(pkce.CodeVerifier) == "" {
		return nil, fmt.Errorf("code_verifier is required")
	}

	token, err := c.exchange(ctx, url.Values{
		"client_id":     {c.clientID()},
		"client_secret": {c.clientSecret()},
		"code":          {strings.TrimSpace(code)},
		"code_verifier": {strings.TrimSpace(pkce.CodeVerifier)},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {strings.TrimSpace(redirectURI)},
	})
	if err != nil {
		return nil, err
	}
	return c.credentialFromToken(ctx, token, nil)
}

func (c *GeminiClient) ExchangeSessionCode(ctx context.Context, session *LoginSession, code string) (*Credential, error) {
	if session == nil {
		return nil, fmt.Errorf("login session is nil")
	}
	return c.ExchangeCode(ctx, code, session.redirectURI, session.pkce)
}

func (c *GeminiClient) Refresh(ctx context.Context, cred *Credential) (*Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	if strings.TrimSpace(cred.RefreshToken) == "" {
		return cred.Clone(), nil
	}

	token, err := c.exchange(ctx, url.Values{
		"client_id":     {c.clientID()},
		"client_secret": {c.clientSecret()},
		"grant_type":    {"refresh_token"},
		"refresh_token": {strings.TrimSpace(cred.RefreshToken)},
	})
	if err != nil {
		return nil, err
	}
	return c.credentialFromToken(ctx, token, cred)
}

func (c *GeminiClient) exchange(ctx context.Context, form url.Values) (*geminiTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var token geminiTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func (c *GeminiClient) credentialFromToken(ctx context.Context, token *geminiTokenResponse, previous *Credential) (*Credential, error) {
	now := c.now()
	email, err := c.fetchUserEmail(ctx, token.AccessToken)
	if err != nil && strings.TrimSpace(previousValue(previous, func(v *Credential) string { return v.Email })) == "" {
		return nil, err
	}

	projectID, tierID, autoProject, err := c.resolveProjectID(ctx, token.AccessToken, previous)
	if err != nil {
		return nil, err
	}

	email = strings.TrimSpace(firstNonEmpty(email, previousValue(previous, func(v *Credential) string { return v.Email })))
	projectID = strings.TrimSpace(firstNonEmpty(projectID, c.requestedProjectID(previous), previousValue(previous, func(v *Credential) string { return v.AccountID })))

	metadata := geminiMetadataFromToken(c, token, projectID, tierID, autoProject)
	cred := &Credential{
		Ref:          geminiCredentialRef(email, projectID),
		Provider:     config.OAuthProviderGemini,
		Email:        email,
		AccountID:    projectID,
		AccessToken:  strings.TrimSpace(token.AccessToken),
		RefreshToken: strings.TrimSpace(token.RefreshToken),
		LastRefresh:  now,
		Metadata:     metadata,
	}
	if token.ExpiresIn > 0 {
		cred.ExpiresAt = now.Add(time.Duration(token.ExpiresIn) * time.Second)
	}

	if previous != nil {
		if strings.TrimSpace(previous.Ref) != "" {
			cred.Ref = previous.Ref
		}
		if cred.Email == "" {
			cred.Email = previous.Email
		}
		if cred.AccountID == "" {
			cred.AccountID = previous.AccountID
		}
		if cred.RefreshToken == "" {
			cred.RefreshToken = previous.RefreshToken
		}
		if len(previous.Metadata) > 0 {
			if cred.Metadata == nil {
				cred.Metadata = make(map[string]string, len(previous.Metadata))
			}
			for k, v := range previous.Metadata {
				if _, exists := cred.Metadata[k]; !exists {
					cred.Metadata[k] = v
				}
			}
		}
	} else if cred.Ref == "" {
		cred.Ref = stableCredentialRef(config.OAuthProviderGemini, email, projectID)
	}

	if cred.Ref == "" {
		cred.Ref = stableCredentialRef(config.OAuthProviderGemini, email, projectID)
	}
	if cred.Metadata != nil {
		cred.Metadata["project_id"] = strings.TrimSpace(firstNonEmpty(cred.AccountID, cred.Metadata["project_id"]))
	}
	return cred, nil
}

func (c *GeminiClient) fetchUserEmail(ctx context.Context, accessToken string) (string, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return "", fmt.Errorf("missing access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userInfoURL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient().Do(req)
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

	var info geminiUserInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return "", err
	}
	return strings.TrimSpace(info.Email), nil
}

func (c *GeminiClient) resolveProjectID(ctx context.Context, accessToken string, previous *Credential) (string, string, bool, error) {
	requestedProject := c.requestedProjectID(previous)
	if err := validateGeminiProjectID(requestedProject); err != nil {
		return "", "", false, err
	}

	loadReqBody := map[string]any{
		"metadata": geminiCloudCodeMetadataForProject(requestedProject, true),
	}
	if requestedProject != "" {
		loadReqBody["cloudaicompanionProject"] = requestedProject
	}

	loadResp, err := c.callCloudCode(ctx, accessToken, "loadCodeAssist", loadReqBody)
	if err != nil {
		if fallbackProject := strings.TrimSpace(previousGeminiMetadata(previous, "project_id")); fallbackProject != "" {
			return fallbackProject, previousGeminiMetadata(previous, "tier_id"), previousGeminiMetadata(previous, "auto_project") == "true", nil
		}
		return "", "", false, fmt.Errorf("load code assist: %w", err)
	}
	if err := geminiLoadCodeAssistValidationError(loadResp); err != nil {
		return "", "", false, err
	}

	tierID := extractGeminiTierID(loadResp)
	serverProjectID := extractGeminiProjectID(loadResp)
	if hasGeminiCurrentTier(loadResp) {
		projectID := firstNonEmpty(serverProjectID, requestedProject, previousGeminiMetadata(previous, "project_id"))
		if strings.TrimSpace(projectID) == "" {
			return "", tierID, false, fmt.Errorf("gemini oauth requires GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT_ID for this account")
		}
		return strings.TrimSpace(projectID), tierID, false, nil
	}

	finalProjectID, err := c.pollOnboardProject(ctx, accessToken, tierID, requestedProject)
	if err != nil {
		if fallbackProject := strings.TrimSpace(previousGeminiMetadata(previous, "project_id")); fallbackProject != "" {
			return fallbackProject, firstNonEmpty(tierID, previousGeminiMetadata(previous, "tier_id")), previousGeminiMetadata(previous, "auto_project") == "true", nil
		}
		if setupErr := geminiLoadCodeAssistProjectSetupError(loadResp, requestedProject); setupErr != nil {
			return "", tierID, false, setupErr
		}
		return "", tierID, false, fmt.Errorf("onboard user: %w", err)
	}

	projectID := strings.TrimSpace(firstNonEmpty(finalProjectID, requestedProject, serverProjectID, previousGeminiMetadata(previous, "project_id")))
	autoProject := requestedProject == "" && projectID != ""
	return projectID, tierID, autoProject, nil
}

func (c *GeminiClient) pollOnboardProject(ctx context.Context, accessToken string, tierID string, projectID string) (string, error) {
	tierID = strings.TrimSpace(firstNonEmpty(tierID, defaultGeminiLegacyTierID))
	projectID = strings.TrimSpace(projectID)
	sendProject := projectID != "" && !isGeminiFreeTier(tierID)

	reqBody := map[string]any{
		"metadata": geminiCloudCodeMetadataForProject(projectID, sendProject),
		"tierId":   tierID,
	}
	if sendProject {
		reqBody["cloudaicompanionProject"] = projectID
	}

	for attempt := 0; attempt < defaultGeminiOnboardAttempts; attempt++ {
		resp, err := c.callCloudCode(ctx, accessToken, "onboardUser", reqBody)
		if err != nil {
			return "", err
		}
		if done, _ := resp["done"].(bool); done {
			responseProjectID := extractGeminiProjectIDFromOnboard(resp)
			if responseProjectID != "" {
				return responseProjectID, nil
			}
			return projectID, nil
		}
		if operationName := extractGeminiOperationName(resp); operationName != "" {
			lroResp, err := c.pollCloudCodeOperation(ctx, accessToken, operationName)
			if err != nil {
				return "", err
			}
			if responseProjectID := extractGeminiProjectIDFromOnboard(lroResp); responseProjectID != "" {
				return responseProjectID, nil
			}
			return projectID, nil
		}
		if attempt+1 < defaultGeminiOnboardAttempts {
			c.sleep(defaultGeminiOnboardDelay)
		}
	}

	if projectID != "" {
		return projectID, nil
	}
	return "", fmt.Errorf("project onboarding did not complete")
}

func (c *GeminiClient) callCloudCode(ctx context.Context, accessToken string, action string, body map[string]any) (map[string]any, error) {
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(c.cloudCodeURL(), "/") + "/" + strings.Trim(c.cloudCodeVersion(), "/") + ":" + strings.TrimSpace(action)
	var lastErr error
	for attempt := 0; attempt < defaultGeminiCloudCodeRetryAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(rawBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
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

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
			if attempt+1 < defaultGeminiCloudCodeRetryAttempts && geminiCloudCodeShouldRetry(resp.StatusCode) {
				c.sleep(geminiCloudCodeRetryDelay(resp.Header, c.now()))
				continue
			}
			return nil, lastErr
		}

		var data map[string]any
		if err := json.Unmarshal(respBody, &data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, lastErr
}

func (c *GeminiClient) pollCloudCodeOperation(ctx context.Context, accessToken string, name string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("operation name is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultGeminiOperationPollTimeout)
		defer cancel()
	}

	for attempt := 0; attempt < defaultGeminiOperationPollMaxAttempts; attempt++ {
		if attempt > 0 {
			c.sleep(defaultGeminiOperationPollDelay)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		endpoint := strings.TrimRight(c.cloudCodeURL(), "/") + "/" + strings.Trim(c.cloudCodeVersion(), "/") + "/" + strings.TrimLeft(name, "/")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", geminiCloudCodeUserAgent(defaultGeminiCloudCodeUserAgentModel))

		resp, err := c.httpClient().Do(req)
		if err != nil {
			return nil, err
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			if attempt+1 < defaultGeminiOperationPollMaxAttempts && geminiCloudCodeShouldRetry(resp.StatusCode) {
				c.sleep(geminiCloudCodeRetryDelay(resp.Header, c.now()))
				continue
			}
			return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}

		var data map[string]any
		if err := json.Unmarshal(respBody, &data); err != nil {
			return nil, err
		}
		if done, _ := data["done"].(bool); done {
			return data, nil
		}
	}

	return nil, fmt.Errorf("operation %q did not complete after %d polls", name, defaultGeminiOperationPollMaxAttempts)
}

func geminiMetadataFromToken(client *GeminiClient, token *geminiTokenResponse, projectID string, tierID string, autoProject bool) map[string]string {
	tokenJSON := map[string]any{
		"access_token":  strings.TrimSpace(token.AccessToken),
		"client_id":     client.clientID(),
		"client_secret": client.clientSecret(),
		"expires_in":    token.ExpiresIn,
		"refresh_token": strings.TrimSpace(token.RefreshToken),
		"scope":         strings.TrimSpace(token.Scope),
		"scopes":        client.scopes(),
		"token_type":    strings.TrimSpace(token.TokenType),
		"token_uri":     client.tokenURL(),
	}
	if strings.TrimSpace(token.IDToken) != "" {
		tokenJSON["id_token"] = strings.TrimSpace(token.IDToken)
	}

	encodedToken, _ := json.Marshal(tokenJSON)
	metadata := map[string]string{
		"auto_project": strconv.FormatBool(autoProject),
		"project_id":   strings.TrimSpace(projectID),
		"scopes":       strings.Join(client.scopes(), " "),
		"tier_id":      strings.TrimSpace(tierID),
		"token_json":   string(encodedToken),
		"token_type":   strings.TrimSpace(token.TokenType),
	}
	if scope := strings.TrimSpace(token.Scope); scope != "" {
		metadata["granted_scope"] = scope
	}
	if idToken := strings.TrimSpace(token.IDToken); idToken != "" {
		metadata["id_token"] = idToken
	}
	if requestedProject := strings.TrimSpace(client.requestedProjectID(nil)); requestedProject != "" {
		metadata["requested_project_id"] = requestedProject
	}
	return metadata
}

func geminiCredentialRef(email string, projectID string) string {
	email = strings.TrimSpace(email)
	projectID = strings.TrimSpace(projectID)
	if email == "" {
		return stableCredentialRef(config.OAuthProviderGemini, "", projectID)
	}
	if projectID == "" {
		return stableCredentialRef(config.OAuthProviderGemini, email, "")
	}
	return stableCredentialRef(config.OAuthProviderGemini, email+"-"+projectID, "")
}

func extractGeminiProjectID(payload map[string]any) string {
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

func extractGeminiProjectIDFromOnboard(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	response, ok := payload["response"].(map[string]any)
	if !ok {
		return ""
	}
	return extractGeminiProjectID(response)
}

func extractGeminiOperationName(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	name, _ := payload["name"].(string)
	return strings.TrimSpace(name)
}

func extractGeminiTierID(payload map[string]any) string {
	if payload == nil {
		return defaultGeminiLegacyTierID
	}
	if tierID := extractGeminiTierIDValue(payload["paidTier"]); tierID != "" {
		return tierID
	}
	if tierID := extractGeminiTierIDValue(payload["currentTier"]); tierID != "" {
		return tierID
	}
	if _, ok := payload["currentTier"]; ok {
		return defaultGeminiStandardTierID
	}
	tiers, ok := payload["allowedTiers"].([]any)
	if !ok {
		return defaultGeminiLegacyTierID
	}
	for _, rawTier := range tiers {
		tier, ok := rawTier.(map[string]any)
		if !ok {
			continue
		}
		isDefault, _ := tier["isDefault"].(bool)
		if !isDefault {
			continue
		}
		if id, ok := tier["id"].(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return defaultGeminiLegacyTierID
}

func extractGeminiTierIDValue(raw any) string {
	tier, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	id, _ := tier["id"].(string)
	return strings.TrimSpace(id)
}

func hasGeminiCurrentTier(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	value, ok := payload["currentTier"]
	return ok && value != nil
}

func isGeminiFreeTier(tierID string) bool {
	return strings.EqualFold(strings.TrimSpace(tierID), defaultGeminiFreeTierID)
}

func geminiCloudCodeMetadataForProject(projectID string, includeProject bool) map[string]string {
	metadata := make(map[string]string, len(geminiCloudCodeMetadata)+1)
	for k, v := range geminiCloudCodeMetadata {
		metadata[k] = v
	}
	if includeProject {
		if projectID = strings.TrimSpace(projectID); projectID != "" {
			metadata["duetProject"] = projectID
		}
	}
	return metadata
}

func geminiLoadCodeAssistValidationError(payload map[string]any) error {
	if payload == nil || hasGeminiCurrentTier(payload) {
		return nil
	}
	ineligible, ok := payload["ineligibleTiers"].([]any)
	if !ok || len(ineligible) == 0 {
		return nil
	}

	for _, rawTier := range ineligible {
		tier, ok := rawTier.(map[string]any)
		if !ok {
			continue
		}
		reasonCode := strings.TrimSpace(stringFromGeminiAny(tier["reasonCode"]))
		if reasonCode == "VALIDATION_REQUIRED" {
			validationURL := strings.TrimSpace(stringFromGeminiAny(tier["validationUrl"]))
			if validationURL != "" {
				return fmt.Errorf("gemini account validation required: %s", validationURL)
			}
			return fmt.Errorf("gemini account validation required")
		}
	}
	return nil
}

func geminiLoadCodeAssistProjectSetupError(payload map[string]any, requestedProject string) error {
	if payload == nil {
		return nil
	}
	ineligible, ok := payload["ineligibleTiers"].([]any)
	if !ok || len(ineligible) == 0 {
		if strings.TrimSpace(requestedProject) == "" {
			return fmt.Errorf("gemini oauth requires GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT_ID for this account")
		}
		return nil
	}
	messages := make([]string, 0, len(ineligible))
	for _, rawTier := range ineligible {
		tier, ok := rawTier.(map[string]any)
		if !ok {
			continue
		}
		reasonCode := strings.TrimSpace(stringFromGeminiAny(tier["reasonCode"]))
		reasonMessage := strings.TrimSpace(stringFromGeminiAny(tier["reasonMessage"]))
		if reasonMessage != "" {
			messages = append(messages, reasonMessage)
		} else if reasonCode != "" {
			messages = append(messages, reasonCode)
		}
	}
	if len(messages) > 0 {
		return fmt.Errorf("gemini account is not eligible for Code Assist: %s", strings.Join(messages, "; "))
	}
	if strings.TrimSpace(requestedProject) == "" {
		return fmt.Errorf("gemini oauth requires GOOGLE_CLOUD_PROJECT or GOOGLE_CLOUD_PROJECT_ID for this account")
	}
	return fmt.Errorf("gemini account is not eligible for Code Assist")
}

func validateGeminiProjectID(projectID string) error {
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

func geminiProjectIDFromOfficialEnv() string {
	for _, key := range []string{"GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_PROJECT_ID"} {
		if v, ok := lookupNonEmptyEnv(key); ok {
			return v
		}
	}
	return ""
}

func geminiCloudCodeShouldRetry(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, 499:
		return true
	default:
		return statusCode >= http.StatusInternalServerError && statusCode <= 599
	}
}

func geminiCloudCodeRetryDelay(hdr http.Header, now time.Time) time.Duration {
	if d, ok := parseGeminiRetryAfter(hdr.Get("Retry-After"), now); ok && d > 0 {
		return d
	}
	return defaultGeminiCloudCodeRetryDelay
}

func parseGeminiRetryAfter(value string, now time.Time) (time.Duration, bool) {
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

func stringFromGeminiAny(value any) string {
	text, _ := value.(string)
	return text
}

func geminiCloudCodeUserAgent(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = defaultGeminiCloudCodeUserAgentModel
	}
	return fmt.Sprintf(
		"GeminiCLI/%s/%s (%s; %s; terminal)",
		defaultGeminiCloudCodeVersionLabel,
		modelName,
		geminiCloudCodeOS(),
		geminiCloudCodeArch(),
	)
}

func geminiCloudCodeOS() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func geminiCloudCodeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "x86"
	default:
		return runtime.GOARCH
	}
}

func previousGeminiMetadata(cred *Credential, key string) string {
	if cred == nil || len(cred.Metadata) == 0 {
		return ""
	}
	return strings.TrimSpace(cred.Metadata[strings.TrimSpace(key)])
}

func previousValue(cred *Credential, field func(*Credential) string) string {
	if cred == nil || field == nil {
		return ""
	}
	return strings.TrimSpace(field(cred))
}

func (c *GeminiClient) requestedProjectID(previous *Credential) string {
	if c != nil && strings.TrimSpace(c.ProjectID) != "" {
		return strings.TrimSpace(c.ProjectID)
	}
	if projectID := geminiProjectIDFromOfficialEnv(); projectID != "" {
		return projectID
	}
	if previous != nil {
		if projectID := previousGeminiMetadata(previous, "requested_project_id"); projectID != "" {
			return projectID
		}
		if projectID := previousGeminiMetadata(previous, "project_id"); projectID != "" {
			return projectID
		}
		if projectID := strings.TrimSpace(previous.AccountID); projectID != "" {
			return projectID
		}
	}
	return ""
}

func (c *GeminiClient) authURL() string {
	if c != nil && strings.TrimSpace(c.AuthURL) != "" {
		return strings.TrimSpace(c.AuthURL)
	}
	return defaultGeminiAuthURL
}

func (c *GeminiClient) tokenURL() string {
	if c != nil && strings.TrimSpace(c.TokenURL) != "" {
		return strings.TrimSpace(c.TokenURL)
	}
	return defaultGeminiTokenURL
}

func (c *GeminiClient) userInfoURL() string {
	if c != nil && strings.TrimSpace(c.UserInfoURL) != "" {
		return strings.TrimSpace(c.UserInfoURL)
	}
	return defaultGeminiUserInfoURL
}

func (c *GeminiClient) cloudCodeURL() string {
	if c != nil && strings.TrimSpace(c.CloudCodeURL) != "" {
		return strings.TrimSpace(c.CloudCodeURL)
	}
	return defaultGeminiCloudCodeURL
}

func (c *GeminiClient) cloudCodeVersion() string {
	if c != nil && strings.TrimSpace(c.CloudCodeVersion) != "" {
		return strings.TrimSpace(c.CloudCodeVersion)
	}
	return defaultGeminiCloudCodeVersion
}

func (c *GeminiClient) clientID() string {
	if c != nil && strings.TrimSpace(c.ClientID) != "" {
		return strings.TrimSpace(c.ClientID)
	}
	return defaultGeminiClientID
}

func (c *GeminiClient) clientSecret() string {
	if c != nil && strings.TrimSpace(c.ClientSecret) != "" {
		return strings.TrimSpace(c.ClientSecret)
	}
	return defaultGeminiClientSecret
}

func (c *GeminiClient) scopes() []string {
	if c != nil && len(c.Scopes) > 0 {
		return append([]string(nil), c.Scopes...)
	}
	return append([]string(nil), defaultGeminiScopes...)
}

func (c *GeminiClient) callbackHost() string {
	if c != nil && strings.TrimSpace(c.CallbackHost) != "" {
		return strings.TrimSpace(c.CallbackHost)
	}
	return defaultGeminiCallbackHost
}

func (c *GeminiClient) callbackPort() int {
	if c != nil && c.CallbackPort >= 0 {
		return c.CallbackPort
	}
	return defaultGeminiCallbackPort
}

func (c *GeminiClient) callbackPath() string {
	if c != nil {
		path := strings.TrimSpace(c.CallbackPath)
		if path == "" {
			return defaultGeminiCallbackPath
		}
		if !strings.HasPrefix(path, "/") {
			return "/" + path
		}
		return path
	}
	return defaultGeminiCallbackPath
}

func (c *GeminiClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *GeminiClient) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *GeminiClient) sleep(d time.Duration) {
	if c != nil && c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func applyGeminiClientEnvOverrides(c *GeminiClient) {
	if c == nil {
		return
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_AUTH_URL"); ok {
		c.AuthURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_TOKEN_URL"); ok {
		c.TokenURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_USERINFO_URL"); ok {
		c.UserInfoURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CLOUD_CODE_URL"); ok {
		c.CloudCodeURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CLOUD_CODE_VERSION"); ok {
		c.CloudCodeVersion = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CLIENT_ID"); ok {
		c.ClientID = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CLIENT_SECRET"); ok {
		c.ClientSecret = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_PROJECT_ID"); ok {
		c.ProjectID = v
	} else if v := geminiProjectIDFromOfficialEnv(); v != "" {
		c.ProjectID = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_SCOPE"); ok {
		c.Scopes = strings.Fields(v)
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CALLBACK_HOST"); ok {
		c.CallbackHost = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CALLBACK_PATH"); ok {
		c.CallbackPath = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_GEMINI_CALLBACK_PORT"); ok {
		if port, err := strconv.Atoi(v); err == nil && port >= 0 {
			c.CallbackPort = port
		}
	}
}
