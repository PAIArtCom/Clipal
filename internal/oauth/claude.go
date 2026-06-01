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

	"github.com/lansespirit/Clipal/internal/config"
)

const (
	defaultClaudeAuthURL      = "https://platform.claude.com/oauth/authorize"
	defaultClaudeTokenURL     = "https://platform.claude.com/v1/oauth/token"
	defaultClaudeUsageURL     = "https://api.anthropic.com/api/oauth/usage"
	defaultClaudeClientID     = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	defaultClaudeScope        = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	defaultClaudeRefreshBeta  = "oauth-2025-04-20"
	defaultClaudeRefreshUA    = "anthropic-sdk-typescript/0.94.0 userOAuthProvider"
	defaultClaudeCallbackHost = "localhost"
	defaultClaudeCallbackPort = 54545
	defaultClaudeCallbackPath = "/callback"
)

type ClaudeClient struct {
	AuthURL      string
	TokenURL     string
	UsageURL     string
	ClientID     string
	Scope        string
	CallbackHost string
	CallbackPort int
	CallbackPath string
	HTTPClient   *http.Client
	Now          func() time.Time
}

type claudeTokenRequest struct {
	Code         string `json:"code,omitempty"`
	State        string `json:"state,omitempty"`
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	CodeVerifier string `json:"code_verifier,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Organization struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"organization"`
	Account struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

func (c *ClaudeClient) Provider() config.OAuthProvider {
	return config.OAuthProviderClaude
}

func (c *ClaudeClient) WithHTTPClient(httpClient *http.Client) ProviderClient {
	if c == nil || httpClient == nil {
		return c
	}
	clone := *c
	clone.HTTPClient = httpClient
	return &clone
}

func NewClaudeClient() *ClaudeClient {
	client := &ClaudeClient{
		AuthURL:      defaultClaudeAuthURL,
		TokenURL:     defaultClaudeTokenURL,
		UsageURL:     defaultClaudeUsageURL,
		ClientID:     defaultClaudeClientID,
		Scope:        defaultClaudeScope,
		CallbackHost: defaultClaudeCallbackHost,
		CallbackPort: defaultClaudeCallbackPort,
		CallbackPath: defaultClaudeCallbackPath,
		HTTPClient:   newAnthropicHTTPClient(30 * time.Second),
		Now:          time.Now,
	}
	applyClaudeClientEnvOverrides(client)
	return client
}

func (c *ClaudeClient) GenerateAuthURL(state string, redirectURI string, pkce PKCECodes) (string, error) {
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
		"code":                  {"true"},
		"client_id":             {c.clientID()},
		"response_type":         {"code"},
		"redirect_uri":          {strings.TrimSpace(redirectURI)},
		"scope":                 {c.scope()},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {strings.TrimSpace(state)},
	}
	return strings.TrimSpace(c.authURL()) + "?" + params.Encode(), nil
}

func (c *ClaudeClient) StartLogin(now time.Time, ttl time.Duration) (*LoginSession, error) {
	return startLoginSession(
		config.OAuthProviderClaude,
		now,
		ttl,
		c.callbackHost(),
		c.callbackPort(),
		c.callbackPath(),
		c.GenerateAuthURL,
	)
}

func (c *ClaudeClient) ExchangeCode(ctx context.Context, code string, state string, redirectURI string, pkce PKCECodes) (*Credential, error) {
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("code is required")
	}
	if strings.TrimSpace(state) == "" {
		return nil, fmt.Errorf("state is required")
	}
	if strings.TrimSpace(redirectURI) == "" {
		return nil, fmt.Errorf("redirect_uri is required")
	}
	if strings.TrimSpace(pkce.CodeVerifier) == "" {
		return nil, fmt.Errorf("code_verifier is required")
	}

	parsedCode, parsedState := parseClaudeCodeAndState(code)
	if parsedState != "" {
		state = parsedState
	}

	token, err := c.exchange(ctx, claudeTokenRequest{
		Code:         parsedCode,
		State:        strings.TrimSpace(state),
		GrantType:    "authorization_code",
		ClientID:     c.clientID(),
		RedirectURI:  strings.TrimSpace(redirectURI),
		CodeVerifier: pkce.CodeVerifier,
	})
	if err != nil {
		return nil, err
	}
	return c.credentialFromToken(token, nil), nil
}

func (c *ClaudeClient) ExchangeSessionCode(ctx context.Context, session *LoginSession, code string) (*Credential, error) {
	if session == nil {
		return nil, fmt.Errorf("login session is nil")
	}
	return c.ExchangeCode(ctx, code, session.ID, session.redirectURI, session.pkce)
}

func (c *ClaudeClient) Refresh(ctx context.Context, cred *Credential) (*Credential, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	if strings.TrimSpace(cred.RefreshToken) == "" {
		return cred.Clone(), nil
	}

	token, err := c.exchange(ctx, claudeTokenRequest{
		ClientID:     c.clientID(),
		GrantType:    "refresh_token",
		RefreshToken: strings.TrimSpace(cred.RefreshToken),
	})
	if err != nil {
		return nil, err
	}
	return c.credentialFromToken(token, cred), nil
}

func (c *ClaudeClient) exchange(ctx context.Context, requestBody claudeTokenRequest) (*claudeTokenResponse, error) {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if requestBody.GrantType == "refresh_token" {
		req.Header.Set("Anthropic-Beta", defaultClaudeRefreshBeta)
		req.Header.Set("User-Agent", defaultClaudeRefreshUA)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var token claudeTokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func (c *ClaudeClient) credentialFromToken(token *claudeTokenResponse, previous *Credential) *Credential {
	now := c.now()
	email := strings.TrimSpace(token.Account.EmailAddress)
	accountID := strings.TrimSpace(token.Account.UUID)
	organizationID := strings.TrimSpace(token.Organization.UUID)

	refIdentity := accountID
	if refIdentity == "" {
		refIdentity = organizationID
	}

	cred := &Credential{
		Ref:          stableCredentialRef(config.OAuthProviderClaude, email, refIdentity),
		Provider:     config.OAuthProviderClaude,
		Email:        email,
		AccountID:    accountID,
		AccessToken:  strings.TrimSpace(token.AccessToken),
		RefreshToken: strings.TrimSpace(token.RefreshToken),
		ExpiresAt:    now.Add(time.Duration(token.ExpiresIn) * time.Second),
		LastRefresh:  now,
		Metadata:     claudeMetadataFromToken(token),
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
	}

	return cred
}

func claudeMetadataFromToken(token *claudeTokenResponse) map[string]string {
	if token == nil {
		return nil
	}

	metadata := map[string]string{}
	if v := strings.TrimSpace(token.TokenType); v != "" {
		metadata["token_type"] = v
	}
	if v := strings.TrimSpace(token.Organization.UUID); v != "" {
		metadata["organization_id"] = v
	}
	if v := strings.TrimSpace(token.Organization.Name); v != "" {
		metadata["organization_name"] = v
	}
	if v := strings.TrimSpace(token.Account.UUID); v != "" {
		metadata["account_id"] = v
	}
	if v := strings.TrimSpace(token.Account.EmailAddress); v != "" {
		metadata["account_email"] = v
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func parseClaudeCodeAndState(code string) (string, string) {
	parts := strings.Split(strings.TrimSpace(code), "#")
	parsedCode := strings.TrimSpace(parts[0])
	if len(parts) < 2 {
		return parsedCode, ""
	}
	return parsedCode, strings.TrimSpace(parts[1])
}

func (c *ClaudeClient) authURL() string {
	if c != nil && strings.TrimSpace(c.AuthURL) != "" {
		return strings.TrimSpace(c.AuthURL)
	}
	return defaultClaudeAuthURL
}

func (c *ClaudeClient) tokenURL() string {
	if c != nil && strings.TrimSpace(c.TokenURL) != "" {
		return strings.TrimSpace(c.TokenURL)
	}
	return defaultClaudeTokenURL
}

func (c *ClaudeClient) clientID() string {
	if c != nil && strings.TrimSpace(c.ClientID) != "" {
		return strings.TrimSpace(c.ClientID)
	}
	return defaultClaudeClientID
}

func (c *ClaudeClient) usageURL() string {
	if c != nil && strings.TrimSpace(c.UsageURL) != "" {
		return strings.TrimSpace(c.UsageURL)
	}
	return defaultClaudeUsageURL
}

func (c *ClaudeClient) scope() string {
	if c != nil && strings.TrimSpace(c.Scope) != "" {
		return strings.TrimSpace(c.Scope)
	}
	return defaultClaudeScope
}

func (c *ClaudeClient) callbackHost() string {
	if c != nil && strings.TrimSpace(c.CallbackHost) != "" {
		return strings.TrimSpace(c.CallbackHost)
	}
	return defaultClaudeCallbackHost
}

func (c *ClaudeClient) callbackPort() int {
	if c != nil && c.CallbackPort >= 0 {
		return c.CallbackPort
	}
	return defaultClaudeCallbackPort
}

func (c *ClaudeClient) callbackPath() string {
	if c != nil && strings.TrimSpace(c.CallbackPath) != "" {
		path := strings.TrimSpace(c.CallbackPath)
		if !strings.HasPrefix(path, "/") {
			return "/" + path
		}
		return path
	}
	return defaultClaudeCallbackPath
}

func (c *ClaudeClient) httpClient() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *ClaudeClient) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func applyClaudeClientEnvOverrides(c *ClaudeClient) {
	if c == nil {
		return
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_AUTH_URL"); ok {
		c.AuthURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_TOKEN_URL"); ok {
		c.TokenURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_USAGE_URL"); ok {
		c.UsageURL = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_CLIENT_ID"); ok {
		c.ClientID = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_SCOPE"); ok {
		c.Scope = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_CALLBACK_HOST"); ok {
		c.CallbackHost = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_CALLBACK_PATH"); ok {
		c.CallbackPath = v
	}
	if v, ok := lookupNonEmptyEnv("CLIPAL_OAUTH_CLAUDE_CALLBACK_PORT"); ok {
		if port, err := strconv.Atoi(v); err == nil && port >= 0 {
			c.CallbackPort = port
		}
	}
}
