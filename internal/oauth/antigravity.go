package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

const (
	defaultAntigravityCloudCodeURL       = "https://cloudcode-pa.googleapis.com"
	defaultAntigravityDailyCloudCodeURL  = "https://daily-cloudcode-pa.googleapis.com"
	defaultAntigravityCloudCodeVersion   = "v1internal"
	defaultAntigravityCallbackHost       = "127.0.0.1"
	defaultAntigravityCallbackPort       = 0
	defaultAntigravityCallbackPath       = "/oauth2callback"
	defaultAntigravityScopeCCLog         = "https://www.googleapis.com/auth/cclog"
	defaultAntigravityScopeExperiments   = "https://www.googleapis.com/auth/experimentsandconfigs"
	defaultAntigravityFreeTierID         = "free-tier"
	defaultAntigravityCloudCodeRetry     = defaultGeminiCloudCodeRetryAttempts
	defaultAntigravityCloudCodeRetryWait = defaultGeminiCloudCodeRetryDelay
	defaultAntigravityVersionLabel       = "2.2.1"
	defaultAntigravityInfoPlist          = "/Applications/Antigravity.app/Contents/Info.plist"
)

var (
	antigravityVersionOnce sync.Once
	antigravityVersion     string
	antigravityPlistRE     = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)
)

var (
	defaultAntigravityClientID = strings.Join([]string{
		"1071006060591",
		"-tmhssin2h21lcre235",
		"vtolojh4g403ep",
		".apps.googleusercontent.com",
	}, "")
	defaultAntigravityClientSecret = strings.Join([]string{
		"GO",
		"CSPX-",
		"K58FWR",
		"486LdLJ1mLB",
		"8sXC4z6qDAf",
	}, "")
)

var defaultAntigravityScopes = []string{
	defaultGeminiScopeCloudPlatform,
	defaultGeminiScopeUserInfoEmail,
	defaultGeminiScopeUserInfoProfile,
	defaultAntigravityScopeCCLog,
	defaultAntigravityScopeExperiments,
}

type AntigravityClient struct {
	AuthURL           string
	TokenURL          string
	UserInfoURL       string
	CloudCodeURL      string
	DailyCloudCodeURL string
	CloudCodeVersion  string
	ClientID          string
	ClientSecret      string
	Scopes            []string
	ProjectID         string
	CallbackHost      string
	CallbackPort      int
	CallbackPath      string
	HTTPClient        *http.Client
	Now               func() time.Time
	Sleep             func(time.Duration)
}

var _ ProviderClient = (*AntigravityClient)(nil)

type antigravityTierMetadata struct {
	TierID                          string
	CurrentTierID                   string
	CurrentTierName                 string
	PaidTierID                      string
	PaidTierName                    string
	PaidCreditType                  string
	PaidMinimumCreditAmountForUsage string
	AllowedDefaultTierID            string
	AllowedDefaultTierName          string
}

func NewAntigravityClient() *AntigravityClient {
	client := &AntigravityClient{
		AuthURL:           defaultGeminiAuthURL,
		TokenURL:          defaultGeminiTokenURL,
		UserInfoURL:       defaultGeminiUserInfoURL,
		CloudCodeURL:      defaultAntigravityCloudCodeURL,
		DailyCloudCodeURL: defaultAntigravityDailyCloudCodeURL,
		CloudCodeVersion:  defaultAntigravityCloudCodeVersion,
		ClientID:          defaultAntigravityClientID,
		ClientSecret:      defaultAntigravityClientSecret,
		Scopes:            append([]string(nil), defaultAntigravityScopes...),
		CallbackHost:      defaultAntigravityCallbackHost,
		CallbackPort:      defaultAntigravityCallbackPort,
		CallbackPath:      defaultAntigravityCallbackPath,
		HTTPClient:        &http.Client{Timeout: 30 * time.Second},
		Now:               time.Now,
		Sleep:             time.Sleep,
	}
	applyAntigravityClientEnvOverrides(client)
	return client
}

func (c *AntigravityClient) WithHTTPClient(httpClient *http.Client) ProviderClient {
	if c == nil || httpClient == nil {
		return c
	}
	clone := *c
	clone.HTTPClient = httpClient
	clone.Scopes = append([]string(nil), c.Scopes...)
	return &clone
}

func (c *AntigravityClient) Provider() config.OAuthProvider {
	return config.OAuthProviderAntigravity
}

func (c *AntigravityClient) StartLogin(now time.Time, ttl time.Duration) (*LoginSession, error) {
	return startLoginSession(
		config.OAuthProviderAntigravity,
		now,
		ttl,
		c.callbackHost(),
		c.callbackPort(),
		c.callbackPath(),
		c.GenerateAuthURL,
	)
}

func (c *AntigravityClient) GenerateAuthURL(state string, redirectURI string, pkce PKCECodes) (string, error) {
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

func (c *AntigravityClient) ExchangeSessionCode(ctx context.Context, session *LoginSession, code string) (*Credential, error) {
	if session == nil {
		return nil, fmt.Errorf("login session is nil")
	}
	return c.ExchangeCode(ctx, code, session.redirectURI, session.pkce)
}

func (c *AntigravityClient) ExchangeCode(ctx context.Context, code string, redirectURI string, pkce PKCECodes) (*Credential, error) {
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

func (c *AntigravityClient) Refresh(ctx context.Context, cred *Credential) (*Credential, error) {
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

func (c *AntigravityClient) exchange(ctx context.Context, form url.Values) (*geminiTokenResponse, error) {
	gemini := &GeminiClient{TokenURL: c.tokenURL(), HTTPClient: c.httpClient()}
	return gemini.exchange(ctx, form)
}

func (c *AntigravityClient) credentialFromToken(ctx context.Context, token *geminiTokenResponse, previous *Credential) (*Credential, error) {
	now := c.now()
	email, err := c.fetchUserEmail(ctx, token.AccessToken)
	if err != nil && strings.TrimSpace(previousValue(previous, func(v *Credential) string { return v.Email })) == "" {
		return nil, err
	}
	projectID, tiers, autoProject, err := c.resolveProjectID(ctx, token.AccessToken, previous)
	if err != nil {
		return nil, err
	}
	email = strings.TrimSpace(firstNonEmpty(email, previousValue(previous, func(v *Credential) string { return v.Email })))
	projectID = strings.TrimSpace(firstNonEmpty(projectID, c.requestedProjectID(previous), previousValue(previous, func(v *Credential) string { return v.AccountID })))

	metadata := c.metadataFromToken(token, projectID, tiers, autoProject)
	cred := &Credential{
		Ref:          antigravityCredentialRef(email, projectID),
		Provider:     config.OAuthProviderAntigravity,
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
		for k, v := range previous.Metadata {
			if volatileCredentialMetadataKey(config.OAuthProviderAntigravity, k) {
				continue
			}
			if _, exists := cred.Metadata[k]; !exists {
				cred.Metadata[k] = v
			}
		}
	}
	if cred.Ref == "" {
		cred.Ref = stableCredentialRef(config.OAuthProviderAntigravity, email, projectID)
	}
	if cred.Metadata != nil {
		cred.Metadata["project_id"] = strings.TrimSpace(firstNonEmpty(cred.AccountID, cred.Metadata["project_id"]))
	}
	return cred, nil
}

func (c *AntigravityClient) fetchUserEmail(ctx context.Context, accessToken string) (string, error) {
	gemini := &GeminiClient{UserInfoURL: c.userInfoURL(), HTTPClient: c.httpClient()}
	return gemini.fetchUserEmail(ctx, accessToken)
}

func (c *AntigravityClient) resolveProjectID(ctx context.Context, accessToken string, previous *Credential) (string, antigravityTierMetadata, bool, error) {
	requestedProject := c.requestedProjectID(previous)
	if err := validateGeminiProjectID(requestedProject); err != nil {
		return "", antigravityTierMetadata{}, false, err
	}
	loadResp, err := c.callCloudCode(ctx, accessToken, c.cloudCodeURL(), "loadCodeAssist", map[string]any{
		"metadata": antigravityClientMetadata(),
	})
	if err != nil {
		if fallbackProject := previousGeminiMetadata(previous, "project_id"); fallbackProject != "" {
			return fallbackProject, antigravityTierMetadataFromCredential(previous), previousGeminiMetadata(previous, "auto_project") == "true", nil
		}
		return "", antigravityTierMetadata{}, false, fmt.Errorf("load code assist: %w", err)
	}
	tiers := extractAntigravityTierMetadata(loadResp)
	projectID := strings.TrimSpace(firstNonEmpty(extractGeminiProjectID(loadResp), requestedProject, previousGeminiMetadata(previous, "project_id")))
	if projectID != "" {
		return projectID, tiers, requestedProject == "", nil
	}
	if requestedProject != "" {
		return requestedProject, tiers, false, nil
	}
	projectID, err = c.onboardUser(ctx, accessToken, tiers.onboardTierID())
	if err != nil {
		if fallbackProject := previousGeminiMetadata(previous, "project_id"); fallbackProject != "" {
			if tiers.isZero() {
				tiers = antigravityTierMetadataFromCredential(previous)
			}
			return fallbackProject, tiers, previousGeminiMetadata(previous, "auto_project") == "true", nil
		}
		return "", tiers, false, fmt.Errorf("onboard user: %w", err)
	}
	return strings.TrimSpace(projectID), tiers, requestedProject == "", nil
}

func (c *AntigravityClient) onboardUser(ctx context.Context, accessToken string, tierID string) (string, error) {
	tierID = strings.TrimSpace(firstNonEmpty(tierID, defaultAntigravityFreeTierID))
	metadata := antigravityClientMetadata()
	resp, err := c.callCloudCode(ctx, accessToken, c.dailyCloudCodeURL(), "onboardUser", map[string]any{
		"tier_id":  tierID,
		"metadata": metadata,
	})
	if err != nil && strings.Contains(err.Error(), "status 400") {
		resp, err = c.callCloudCode(ctx, accessToken, c.dailyCloudCodeURL(), "onboardUser", map[string]any{
			"tierId":   tierID,
			"metadata": metadata,
		})
	}
	if err != nil {
		return "", err
	}
	if done, _ := resp["done"].(bool); !done {
		return "", fmt.Errorf("project onboarding did not complete")
	}
	return extractGeminiProjectIDFromOnboard(resp), nil
}

func extractAntigravityTierID(payload map[string]any) string {
	return extractAntigravityTierMetadata(payload).TierID
}

func extractAntigravityTierMetadata(payload map[string]any) antigravityTierMetadata {
	if payload == nil {
		return antigravityTierMetadata{TierID: defaultAntigravityFreeTierID}
	}
	var metadata antigravityTierMetadata
	metadata.PaidTierID, metadata.PaidTierName = extractAntigravityTierIdentity(payload["paidTier"])
	metadata.PaidCreditType, metadata.PaidMinimumCreditAmountForUsage = extractAntigravityPaidCredit(payload["paidTier"])
	metadata.CurrentTierID, metadata.CurrentTierName = extractAntigravityTierIdentity(payload["currentTier"])
	if tiers, ok := payload["allowedTiers"].([]any); ok {
		for _, rawTier := range tiers {
			tier, ok := rawTier.(map[string]any)
			if !ok {
				continue
			}
			isDefault, _ := tier["isDefault"].(bool)
			if !isDefault {
				continue
			}
			metadata.AllowedDefaultTierID, metadata.AllowedDefaultTierName = extractAntigravityTierIdentity(tier)
			break
		}
	}
	metadata.TierID = firstNonEmpty(
		metadata.PaidTierID,
		metadata.CurrentTierID,
		metadata.AllowedDefaultTierID,
		defaultAntigravityFreeTierID,
	)
	return metadata
}

func extractAntigravityTierIdentity(raw any) (string, string) {
	tier, ok := raw.(map[string]any)
	if !ok {
		return "", ""
	}
	id, _ := tier["id"].(string)
	name, _ := tier["name"].(string)
	return strings.TrimSpace(id), strings.TrimSpace(name)
}

func extractAntigravityPaidCredit(raw any) (string, string) {
	tier, ok := raw.(map[string]any)
	if !ok {
		return "", ""
	}
	credits, ok := tier["availableCredits"].([]any)
	if !ok {
		return "", ""
	}
	for _, rawCredit := range credits {
		credit, ok := rawCredit.(map[string]any)
		if !ok {
			continue
		}
		creditType, _ := credit["creditType"].(string)
		minimum, _ := credit["minimumCreditAmountForUsage"].(string)
		creditType = strings.TrimSpace(creditType)
		minimum = strings.TrimSpace(minimum)
		if creditType != "" || minimum != "" {
			return creditType, minimum
		}
	}
	return "", ""
}

func antigravityTierMetadataFromCredential(cred *Credential) antigravityTierMetadata {
	if cred == nil || cred.Metadata == nil {
		return antigravityTierMetadata{}
	}
	return antigravityTierMetadata{
		TierID:                          strings.TrimSpace(cred.Metadata["tier_id"]),
		CurrentTierID:                   strings.TrimSpace(cred.Metadata["current_tier_id"]),
		CurrentTierName:                 strings.TrimSpace(cred.Metadata["current_tier_name"]),
		PaidTierID:                      strings.TrimSpace(cred.Metadata["paid_tier_id"]),
		PaidTierName:                    strings.TrimSpace(cred.Metadata["paid_tier_name"]),
		PaidCreditType:                  strings.TrimSpace(cred.Metadata["paid_credit_type"]),
		PaidMinimumCreditAmountForUsage: strings.TrimSpace(cred.Metadata["paid_minimum_credit_amount_for_usage"]),
		AllowedDefaultTierID:            strings.TrimSpace(cred.Metadata["allowed_default_tier_id"]),
		AllowedDefaultTierName:          strings.TrimSpace(cred.Metadata["allowed_default_tier_name"]),
	}
}

func (m antigravityTierMetadata) onboardTierID() string {
	return firstNonEmpty(m.CurrentTierID, m.AllowedDefaultTierID, defaultAntigravityFreeTierID)
}

func (m antigravityTierMetadata) isZero() bool {
	return m.TierID == "" &&
		m.CurrentTierID == "" &&
		m.CurrentTierName == "" &&
		m.PaidTierID == "" &&
		m.PaidTierName == "" &&
		m.PaidCreditType == "" &&
		m.PaidMinimumCreditAmountForUsage == "" &&
		m.AllowedDefaultTierID == "" &&
		m.AllowedDefaultTierName == ""
}

func (c *AntigravityClient) callCloudCode(ctx context.Context, accessToken string, baseURL string, action string, body map[string]any) (map[string]any, error) {
	gemini := &GeminiClient{
		CloudCodeURL:     baseURL,
		CloudCodeVersion: c.cloudCodeVersion(),
		HTTPClient:       c.httpClient(),
		Now:              c.Now,
		Sleep:            c.Sleep,
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(gemini.cloudCodeURL(), "/") + "/" + strings.Trim(gemini.cloudCodeVersion(), "/") + ":" + strings.TrimSpace(action)
	var lastErr error
	for attempt := 0; attempt < defaultAntigravityCloudCodeRetry; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(rawBody)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", antigravityUserAgent())
		if action == "onboardUser" {
			req.Header.Set("X-Goog-Api-Client", "gl-node/22.21.1")
		}
		resp, err := c.httpClient().Do(req)
		if err != nil {
			lastErr = err
			if attempt+1 < defaultAntigravityCloudCodeRetry {
				c.sleep(defaultAntigravityCloudCodeRetryWait)
				continue
			}
			return nil, err
		}
		respBody, readErr := ioReadAllAndClose(resp)
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			lastErr = fmt.Errorf("request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
			if attempt+1 < defaultAntigravityCloudCodeRetry && geminiCloudCodeShouldRetry(resp.StatusCode) {
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

func ioReadAllAndClose(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

func (c *AntigravityClient) metadataFromToken(token *geminiTokenResponse, projectID string, tiers antigravityTierMetadata, autoProject bool) map[string]string {
	tokenJSON := map[string]any{
		"access_token":  strings.TrimSpace(token.AccessToken),
		"client_id":     c.clientID(),
		"client_secret": c.clientSecret(),
		"expires_in":    token.ExpiresIn,
		"refresh_token": strings.TrimSpace(token.RefreshToken),
		"scope":         strings.TrimSpace(token.Scope),
		"scopes":        c.scopes(),
		"token_type":    strings.TrimSpace(token.TokenType),
		"token_uri":     c.tokenURL(),
	}
	if strings.TrimSpace(token.IDToken) != "" {
		tokenJSON["id_token"] = strings.TrimSpace(token.IDToken)
	}
	encodedToken, _ := json.Marshal(tokenJSON)
	metadata := map[string]string{
		"auto_project": strconv.FormatBool(autoProject),
		"project_id":   strings.TrimSpace(projectID),
		"scopes":       strings.Join(c.scopes(), " "),
		"tier_id":      strings.TrimSpace(firstNonEmpty(tiers.TierID, defaultAntigravityFreeTierID)),
		"token_json":   string(encodedToken),
		"token_type":   strings.TrimSpace(token.TokenType),
	}
	for key, value := range map[string]string{
		"current_tier_id":                      tiers.CurrentTierID,
		"current_tier_name":                    tiers.CurrentTierName,
		"paid_tier_id":                         tiers.PaidTierID,
		"paid_tier_name":                       tiers.PaidTierName,
		"paid_credit_type":                     tiers.PaidCreditType,
		"paid_minimum_credit_amount_for_usage": tiers.PaidMinimumCreditAmountForUsage,
		"allowed_default_tier_id":              tiers.AllowedDefaultTierID,
		"allowed_default_tier_name":            tiers.AllowedDefaultTierName,
	} {
		if value = strings.TrimSpace(value); value != "" {
			metadata[key] = value
		}
	}
	if scope := strings.TrimSpace(token.Scope); scope != "" {
		metadata["granted_scope"] = scope
	}
	if requestedProject := strings.TrimSpace(c.requestedProjectID(nil)); requestedProject != "" {
		metadata["requested_project_id"] = requestedProject
	}
	return metadata
}

func antigravityCredentialRef(email string, projectID string) string {
	if strings.TrimSpace(email) == "" {
		return stableCredentialRef(config.OAuthProviderAntigravity, "", projectID)
	}
	if strings.TrimSpace(projectID) == "" {
		return stableCredentialRef(config.OAuthProviderAntigravity, email, "")
	}
	return stableCredentialRef(config.OAuthProviderAntigravity, email+"-"+projectID, "")
}

func antigravityUserAgent() string {
	return fmt.Sprintf("antigravity/cli/%s (aidev_client; os_type=%s; arch=%s)", antigravityVersionLabel(), runtime.GOOS, runtime.GOARCH)
}

func antigravityClientMetadata() map[string]any {
	version := antigravityVersionLabel()
	return map[string]any{
		"ide_type":       "ANTIGRAVITY",
		"ide_version":    version,
		"plugin_version": version,
		"platform":       antigravityPlatform(),
		"update_channel": "STABLE",
		"plugin_type":    "CLOUD_CODE",
		"ide_name":       "Antigravity",
	}
}

func antigravityVersionLabel() string {
	if value := strings.TrimSpace(os.Getenv("CLIPAL_OAUTH_ANTIGRAVITY_VERSION")); value != "" {
		return value
	}
	antigravityVersionOnce.Do(func() {
		antigravityVersion = defaultAntigravityVersionLabel
		if data, err := os.ReadFile(defaultAntigravityInfoPlist); err == nil {
			if match := antigravityPlistRE.FindSubmatch(data); len(match) == 2 {
				if value := strings.TrimSpace(string(match[1])); value != "" {
					antigravityVersion = value
				}
			}
		}
	})
	return antigravityVersion
}

func antigravityPlatform() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/amd64":
		return "DARWIN_AMD64"
	case "darwin/arm64":
		return "DARWIN_ARM64"
	case "linux/amd64":
		return "LINUX_AMD64"
	case "linux/arm64":
		return "LINUX_ARM64"
	case "windows/amd64":
		return "WINDOWS_AMD64"
	default:
		return "PLATFORM_UNSPECIFIED"
	}
}

func (c *AntigravityClient) requestedProjectID(previous *Credential) string {
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

func (c *AntigravityClient) authURL() string {
	if c != nil && strings.TrimSpace(c.AuthURL) != "" {
		return strings.TrimSpace(c.AuthURL)
	}
	return defaultGeminiAuthURL
}

func (c *AntigravityClient) tokenURL() string {
	if c != nil && strings.TrimSpace(c.TokenURL) != "" {
		return strings.TrimSpace(c.TokenURL)
	}
	return defaultGeminiTokenURL
}

func (c *AntigravityClient) userInfoURL() string {
	if c != nil && strings.TrimSpace(c.UserInfoURL) != "" {
		return strings.TrimSpace(c.UserInfoURL)
	}
	return defaultGeminiUserInfoURL
}

func (c *AntigravityClient) cloudCodeURL() string {
	if c != nil && strings.TrimSpace(c.CloudCodeURL) != "" {
		return strings.TrimSpace(c.CloudCodeURL)
	}
	return defaultAntigravityCloudCodeURL
}

func (c *AntigravityClient) dailyCloudCodeURL() string {
	if c != nil && strings.TrimSpace(c.DailyCloudCodeURL) != "" {
		return strings.TrimSpace(c.DailyCloudCodeURL)
	}
	return defaultAntigravityDailyCloudCodeURL
}

func (c *AntigravityClient) cloudCodeVersion() string {
	if c != nil && strings.TrimSpace(c.CloudCodeVersion) != "" {
		return strings.TrimSpace(c.CloudCodeVersion)
	}
	return defaultAntigravityCloudCodeVersion
}

func (c *AntigravityClient) clientID() string {
	if c != nil && strings.TrimSpace(c.ClientID) != "" {
		return strings.TrimSpace(c.ClientID)
	}
	return defaultAntigravityClientID
}

func (c *AntigravityClient) clientSecret() string {
	if c != nil && strings.TrimSpace(c.ClientSecret) != "" {
		return strings.TrimSpace(c.ClientSecret)
	}
	return defaultAntigravityClientSecret
}

func (c *AntigravityClient) scopes() []string {
	if c != nil && len(c.Scopes) > 0 {
		return append([]string(nil), c.Scopes...)
	}
	return append([]string(nil), defaultAntigravityScopes...)
}

func (c *AntigravityClient) callbackHost() string {
	if c != nil && strings.TrimSpace(c.CallbackHost) != "" {
		return strings.TrimSpace(c.CallbackHost)
	}
	return defaultAntigravityCallbackHost
}

func (c *AntigravityClient) callbackPort() int {
	if c != nil && c.CallbackPort >= 0 {
		return c.CallbackPort
	}
	return defaultAntigravityCallbackPort
}

func (c *AntigravityClient) callbackPath() string {
	if c != nil {
		path := strings.TrimSpace(c.CallbackPath)
		if path == "" {
			return defaultAntigravityCallbackPath
		}
		if !strings.HasPrefix(path, "/") {
			return "/" + path
		}
		return path
	}
	return defaultAntigravityCallbackPath
}

func (c *AntigravityClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *AntigravityClient) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *AntigravityClient) sleep(d time.Duration) {
	if c != nil && c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func applyAntigravityClientEnvOverrides(c *AntigravityClient) {
	if c == nil {
		return
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_AUTH_URL"); ok {
		c.AuthURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_TOKEN_URL"); ok {
		c.TokenURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_USERINFO_URL"); ok {
		c.UserInfoURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_CLOUD_CODE_URL"); ok {
		c.CloudCodeURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_DAILY_CLOUD_CODE_URL"); ok {
		c.DailyCloudCodeURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_CLIENT_ID"); ok {
		c.ClientID = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_CLIENT_SECRET"); ok {
		c.ClientSecret = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_PROJECT_ID"); ok {
		c.ProjectID = v
	} else if v := geminiProjectIDFromOfficialEnv(); v != "" {
		c.ProjectID = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_ANTIGRAVITY_SCOPE"); ok {
		c.Scopes = strings.Fields(v)
	}
}
