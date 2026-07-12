package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

var (
	ErrOAuthImportNotCredential      = errors.New("not a supported oauth credential")
	ErrOAuthImportUnsupportedType    = errors.New("unsupported oauth credential type")
	ErrOAuthImportDisabledCredential = errors.New("oauth credential is disabled")

	ErrCLIProxyAPINotCredential      = ErrOAuthImportNotCredential
	ErrCLIProxyAPIUnsupportedType    = ErrOAuthImportUnsupportedType
	ErrCLIProxyAPIDisabledCredential = ErrOAuthImportDisabledCredential
)

type cliProxyAPICredentialFile struct {
	Type             string         `json:"type"`
	Email            string         `json:"email"`
	AccountID        string         `json:"account_id"`
	ChatGPTAccountID string         `json:"chatgpt_account_id"`
	ChatGPTUserID    string         `json:"chatgpt_user_id"`
	OrganizationID   string         `json:"organization_id"`
	PlanType         string         `json:"plan_type"`
	ClientID         string         `json:"client_id"`
	ProjectID        string         `json:"project_id"`
	AccessToken      string         `json:"access_token"`
	RefreshToken     string         `json:"refresh_token"`
	IDToken          string         `json:"id_token"`
	Expired          any            `json:"expired"`
	ExpiresAt        any            `json:"expires_at"`
	Expiry           any            `json:"expiry"`
	LastRefresh      any            `json:"last_refresh"`
	TokenType        string         `json:"token_type"`
	Token            map[string]any `json:"token"`
	Auto             bool           `json:"auto"`
	Checked          bool           `json:"checked"`
	Disabled         bool           `json:"disabled"`
	Account          struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Organization struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"organization"`
}

type codexNativeAuthFile struct {
	AuthMode     string  `json:"auth_mode"`
	OpenAIAPIKey *string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh any `json:"last_refresh"`
}

type ParsedImportCredential struct {
	Entry      string
	Credential *Credential
	Err        error
}

type sub2APIExportFile struct {
	ExportedAt string               `json:"exported_at"`
	Accounts   []sub2APIExportEntry `json:"accounts"`
}

type sub2APIExportEntry struct {
	Name        string         `json:"name"`
	Platform    string         `json:"platform"`
	Type        string         `json:"type"`
	Credentials map[string]any `json:"credentials"`
}

func ParseCLIProxyAPICredential(data []byte) (*Credential, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, ErrCLIProxyAPINotCredential
	}

	var raw cliProxyAPICredentialFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse cli-proxy-api credential json: %w", err)
	}

	provider := normalizeProvider(config.OAuthProvider(raw.Type))
	if provider == "" {
		return nil, ErrCLIProxyAPINotCredential
	}
	if raw.Disabled {
		return nil, ErrCLIProxyAPIDisabledCredential
	}

	switch provider {
	case config.OAuthProviderCodex:
		return parseCLIProxyAPICodexCredential(raw)
	case config.OAuthProviderGemini:
		return parseCLIProxyAPIGeminiCredential(raw)
	case config.OAuthProviderClaude:
		return parseCLIProxyAPIClaudeCredential(raw)
	default:
		return nil, fmt.Errorf("%w: %s", ErrCLIProxyAPIUnsupportedType, provider)
	}
}

func ParseOAuthImportEntries(data []byte) ([]ParsedImportCredential, error) {
	cred, err := ParseCLIProxyAPICredential(data)
	if err == nil {
		return []ParsedImportCredential{{Credential: cred}}, nil
	}
	if !errors.Is(err, ErrCLIProxyAPINotCredential) {
		return []ParsedImportCredential{{Err: err}}, nil
	}
	cred, err = ParseCodexNativeAuthCredential(data)
	if err == nil {
		return []ParsedImportCredential{{Credential: cred}}, nil
	}
	if !errors.Is(err, ErrCLIProxyAPINotCredential) {
		return []ParsedImportCredential{{Err: err}}, nil
	}
	return parseSub2APIExportEntries(data)
}

// ParseSub2APIExportEntries parses a sub2api account export without applying
// the generic import format detection used by ParseOAuthImportEntries.
func ParseSub2APIExportEntries(data []byte) ([]ParsedImportCredential, error) {
	return parseSub2APIExportEntries(data)
}

func ParseCodexNativeAuthCredential(data []byte) (*Credential, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, ErrCLIProxyAPINotCredential
	}

	var raw codexNativeAuthFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse codex auth json: %w", err)
	}
	if !isCodexNativeAuthFile(raw) {
		return nil, ErrCLIProxyAPINotCredential
	}
	if strings.TrimSpace(raw.Tokens.AccessToken) == "" {
		return nil, fmt.Errorf("codex auth.json missing tokens.access_token")
	}

	email, tokenAccountID := parseCodexIdentityToken(raw.Tokens.IDToken)
	if accessTokenEmail, accessTokenAccountID := parseCodexIdentityToken(raw.Tokens.AccessToken); accessTokenEmail != "" || accessTokenAccountID != "" {
		if email == "" {
			email = accessTokenEmail
		}
		if tokenAccountID == "" {
			tokenAccountID = accessTokenAccountID
		}
	}
	accountID := strings.TrimSpace(firstNonEmpty(raw.Tokens.AccountID, tokenAccountID))
	if email == "" && accountID == "" {
		return nil, fmt.Errorf("codex auth.json missing email/account_id")
	}
	expiresAt := parseJWTExpiresAt(raw.Tokens.AccessToken)
	lastRefresh, err := parseCLIProxyAPITimeValue(raw.LastRefresh)
	if err != nil {
		return nil, fmt.Errorf("parse codex auth.json last_refresh time: %w", err)
	}

	cred := &Credential{
		Ref:          stableCredentialRef(config.OAuthProviderCodex, email, accountID),
		Provider:     config.OAuthProviderCodex,
		Email:        email,
		AccountID:    accountID,
		AccessToken:  strings.TrimSpace(raw.Tokens.AccessToken),
		RefreshToken: strings.TrimSpace(raw.Tokens.RefreshToken),
		ExpiresAt:    expiresAt,
		LastRefresh:  lastRefresh,
		Metadata: map[string]string{
			"auth_mode": strings.TrimSpace(raw.AuthMode),
		},
	}
	if idToken := strings.TrimSpace(raw.Tokens.IDToken); idToken != "" {
		cred.Metadata["id_token"] = idToken
	}
	if accountID != "" {
		cred.Metadata["chatgpt_account_id"] = accountID
	}
	if planType := parseCodexPlanType(raw.Tokens.IDToken); planType != "" {
		cred.Metadata["plan_type"] = planType
	}
	return cred, nil
}

func isCodexNativeAuthFile(raw codexNativeAuthFile) bool {
	authMode := strings.ToLower(strings.TrimSpace(raw.AuthMode))
	if authMode != "chatgpt" {
		return false
	}
	return strings.TrimSpace(raw.Tokens.AccessToken) != "" ||
		strings.TrimSpace(raw.Tokens.RefreshToken) != "" ||
		strings.TrimSpace(raw.Tokens.IDToken) != "" ||
		strings.TrimSpace(raw.Tokens.AccountID) != ""
}

func parseCLIProxyAPICodexCredential(raw cliProxyAPICredentialFile) (*Credential, error) {
	email := strings.TrimSpace(raw.Email)
	accountID := strings.TrimSpace(firstNonEmpty(raw.AccountID, raw.ChatGPTAccountID))
	if tokenEmail, tokenAccountID := parseCodexIdentityToken(raw.IDToken); tokenEmail != "" || tokenAccountID != "" {
		if email == "" {
			email = tokenEmail
		}
		if accountID == "" {
			accountID = tokenAccountID
		}
	}

	if strings.TrimSpace(raw.AccessToken) == "" {
		return nil, fmt.Errorf("codex credential missing access_token")
	}
	if email == "" && accountID == "" {
		return nil, fmt.Errorf("codex credential missing email/account_id")
	}

	expiresAt, err := parseCLIProxyAPITimeValue(firstNonNil(raw.Expired, raw.ExpiresAt))
	if err != nil {
		return nil, fmt.Errorf("parse codex credential expired time: %w", err)
	}
	lastRefresh, err := parseCLIProxyAPITimeValue(raw.LastRefresh)
	if err != nil {
		return nil, fmt.Errorf("parse codex credential last_refresh time: %w", err)
	}

	cred := &Credential{
		Ref:          stableCredentialRef(config.OAuthProviderCodex, email, accountID),
		Provider:     config.OAuthProviderCodex,
		Email:        email,
		AccountID:    accountID,
		AccessToken:  strings.TrimSpace(raw.AccessToken),
		RefreshToken: strings.TrimSpace(raw.RefreshToken),
		ExpiresAt:    expiresAt,
		LastRefresh:  lastRefresh,
	}
	if idToken := strings.TrimSpace(raw.IDToken); idToken != "" ||
		strings.TrimSpace(raw.OrganizationID) != "" ||
		strings.TrimSpace(raw.PlanType) != "" ||
		strings.TrimSpace(raw.ClientID) != "" ||
		strings.TrimSpace(raw.ChatGPTUserID) != "" ||
		strings.TrimSpace(raw.ChatGPTAccountID) != "" {
		cred.Metadata = map[string]string{}
		if idToken != "" {
			cred.Metadata["id_token"] = idToken
		}
		if organizationID := strings.TrimSpace(raw.OrganizationID); organizationID != "" {
			cred.Metadata["organization_id"] = organizationID
		}
		if planType := strings.TrimSpace(raw.PlanType); planType != "" {
			cred.Metadata["plan_type"] = planType
		}
		if clientID := strings.TrimSpace(raw.ClientID); clientID != "" {
			cred.Metadata["client_id"] = clientID
		}
		if userID := strings.TrimSpace(raw.ChatGPTUserID); userID != "" {
			cred.Metadata["chatgpt_user_id"] = userID
		}
		if chatGPTAccountID := strings.TrimSpace(raw.ChatGPTAccountID); chatGPTAccountID != "" {
			cred.Metadata["chatgpt_account_id"] = chatGPTAccountID
		}
	}
	return cred, nil
}

func parseCLIProxyAPIClaudeCredential(raw cliProxyAPICredentialFile) (*Credential, error) {
	email := strings.TrimSpace(firstNonEmpty(raw.Email, raw.Account.EmailAddress))
	accountID := strings.TrimSpace(firstNonEmpty(raw.AccountID, raw.Account.UUID))
	organizationID := strings.TrimSpace(raw.Organization.UUID)
	if strings.TrimSpace(raw.AccessToken) == "" {
		return nil, fmt.Errorf("claude credential missing access_token")
	}
	if email == "" && accountID == "" && organizationID == "" {
		return nil, fmt.Errorf("claude credential missing email/account_id/organization_id")
	}

	expiresAt, err := parseCLIProxyAPITimeValue(firstNonNil(raw.Expired, raw.ExpiresAt))
	if err != nil {
		return nil, fmt.Errorf("parse claude credential expired time: %w", err)
	}
	lastRefresh, err := parseCLIProxyAPITimeValue(raw.LastRefresh)
	if err != nil {
		return nil, fmt.Errorf("parse claude credential last_refresh time: %w", err)
	}

	cred := &Credential{
		Ref:          stableCredentialRef(config.OAuthProviderClaude, email, firstNonEmpty(accountID, organizationID)),
		Provider:     config.OAuthProviderClaude,
		Email:        email,
		AccountID:    accountID,
		AccessToken:  strings.TrimSpace(raw.AccessToken),
		RefreshToken: strings.TrimSpace(raw.RefreshToken),
		ExpiresAt:    expiresAt,
		LastRefresh:  lastRefresh,
	}
	if idToken := strings.TrimSpace(raw.IDToken); idToken != "" || accountID != "" || organizationID != "" || strings.TrimSpace(raw.Organization.Name) != "" {
		cred.Metadata = map[string]string{}
		if idToken != "" {
			cred.Metadata["id_token"] = idToken
		}
		if accountID != "" {
			cred.Metadata["account_id"] = accountID
		}
		if organizationID != "" {
			cred.Metadata["organization_id"] = organizationID
		}
		if organizationName := strings.TrimSpace(raw.Organization.Name); organizationName != "" {
			cred.Metadata["organization_name"] = organizationName
		}
		if accountEmail := strings.TrimSpace(raw.Account.EmailAddress); accountEmail != "" {
			cred.Metadata["account_email"] = accountEmail
		}
	}
	return cred, nil
}

func parseCLIProxyAPIGeminiCredential(raw cliProxyAPICredentialFile) (*Credential, error) {
	email := strings.TrimSpace(raw.Email)
	projectID := strings.TrimSpace(raw.ProjectID)
	accessToken := strings.TrimSpace(firstNonEmpty(raw.AccessToken, stringValue(raw.Token, "access_token")))
	refreshToken := strings.TrimSpace(firstNonEmpty(raw.RefreshToken, stringValue(raw.Token, "refresh_token")))
	tokenType := strings.TrimSpace(firstNonEmpty(raw.TokenType, stringValue(raw.Token, "token_type")))
	expiryValue := firstNonNil(raw.Expired, raw.ExpiresAt, raw.Expiry, stringValue(raw.Token, "expiry"))

	if accessToken == "" {
		return nil, fmt.Errorf("gemini credential missing access_token")
	}
	if projectID == "" {
		return nil, fmt.Errorf("gemini credential missing project_id")
	}

	expiresAt, err := parseCLIProxyAPITimeValue(expiryValue)
	if err != nil {
		return nil, fmt.Errorf("parse gemini credential expiry time: %w", err)
	}
	lastRefresh, err := parseCLIProxyAPITimeValue(raw.LastRefresh)
	if err != nil {
		return nil, fmt.Errorf("parse gemini credential last_refresh time: %w", err)
	}

	cred := &Credential{
		Ref:          geminiCredentialRef(email, projectID),
		Provider:     config.OAuthProviderGemini,
		Email:        email,
		AccountID:    projectID,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		LastRefresh:  lastRefresh,
		Metadata: map[string]string{
			"project_id":   projectID,
			"checked":      strconv.FormatBool(raw.Checked),
			"auto_project": strconv.FormatBool(raw.Auto),
		},
	}
	if tokenType != "" {
		cred.Metadata["token_type"] = tokenType
	}
	if len(raw.Token) > 0 {
		if encodedToken, err := json.Marshal(raw.Token); err == nil {
			cred.Metadata["token_json"] = string(encodedToken)
		}
	}
	return cred, nil
}

func parseSub2APIExportEntries(data []byte) ([]ParsedImportCredential, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return []ParsedImportCredential{{Err: fmt.Errorf("parse oauth credential json: %w", err)}}, nil
	}
	if len(root["accounts"]) == 0 {
		return nil, ErrCLIProxyAPINotCredential
	}

	var raw sub2APIExportFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return []ParsedImportCredential{{Err: fmt.Errorf("parse sub2api export json: %w", err)}}, nil
	}
	if len(raw.Accounts) == 0 {
		return nil, ErrCLIProxyAPINotCredential
	}

	results := make([]ParsedImportCredential, 0, len(raw.Accounts))
	for i, account := range raw.Accounts {
		entry := fmt.Sprintf("accounts[%d]", i)
		parseErr := parseSub2APIExportCredentialError(account)
		if parseErr != nil {
			results = append(results, ParsedImportCredential{Entry: entry, Err: parseErr})
			continue
		}

		normalized, err := normalizeSub2APIAccountCredential(account)
		if err != nil {
			results = append(results, ParsedImportCredential{Entry: entry, Err: err})
			continue
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			results = append(results, ParsedImportCredential{
				Entry: entry,
				Err:   fmt.Errorf("marshal sub2api account credential: %w", err),
			})
			continue
		}
		cred, err := ParseCLIProxyAPICredential(encoded)
		results = append(results, ParsedImportCredential{
			Entry:      entry,
			Credential: cred,
			Err:        err,
		})
	}
	return results, nil
}

func parseSub2APIExportCredentialError(account sub2APIExportEntry) error {
	accountType := strings.ToLower(strings.TrimSpace(account.Type))
	if accountType != "oauth" {
		return ErrCLIProxyAPINotCredential
	}
	if len(account.Credentials) == 0 {
		return fmt.Errorf("sub2api account missing credentials")
	}
	return nil
}

func normalizeSub2APIAccountCredential(account sub2APIExportEntry) (map[string]any, error) {
	normalized := cloneStringAnyMap(account.Credentials)
	provider := sub2APIAccountProvider(account)
	if provider == "" {
		return nil, ErrCLIProxyAPINotCredential
	}
	normalized["type"] = string(provider)
	if provider == config.OAuthProviderCodex {
		if strings.TrimSpace(stringValueAny(normalized, "account_id")) == "" {
			if accountID := strings.TrimSpace(stringValueAny(normalized, "chatgpt_account_id")); accountID != "" {
				normalized["account_id"] = accountID
			}
		}
	}
	return normalized, nil
}

func sub2APIAccountProvider(account sub2APIExportEntry) config.OAuthProvider {
	if provider := normalizeProvider(config.OAuthProvider(stringValueAny(account.Credentials, "type"))); provider != "" {
		return provider
	}
	switch strings.ToLower(strings.TrimSpace(account.Platform)) {
	case "openai":
		return config.OAuthProviderCodex
	case "gemini":
		return config.OAuthProviderGemini
	case "anthropic":
		return config.OAuthProviderClaude
	default:
		return ""
	}
}

func parseCLIProxyAPITimeValue(value any) (time.Time, error) {
	switch typed := value.(type) {
	case nil:
		return time.Time{}, nil
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}, nil
		}
		if ts, ok := parseUnixText(text); ok {
			return unixTimestampToTime(ts), nil
		}
		return time.Parse(time.RFC3339Nano, text)
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return time.Time{}, fmt.Errorf("invalid numeric timestamp %v", typed)
		}
		return unixTimestampToTime(int64(typed)), nil
	case int:
		return unixTimestampToTime(int64(typed)), nil
	case int64:
		return unixTimestampToTime(typed), nil
	case json.Number:
		if ts, err := typed.Int64(); err == nil {
			return unixTimestampToTime(ts), nil
		}
		return time.Time{}, fmt.Errorf("invalid numeric timestamp %q", typed.String())
	default:
		return time.Time{}, fmt.Errorf("unsupported time value type %T", value)
	}
}

func parseUnixText(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	if !isIntegerText(value) {
		return 0, false
	}
	ts, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return ts, true
}

func isIntegerText(value string) bool {
	for i, ch := range value {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if i == 0 && (ch == '+' || ch == '-') {
			continue
		}
		return false
	}
	return value != "" && value != "+" && value != "-"
}

func unixTimestampToTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	if value > 1_000_000_000_000 || value < -1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

func parseJWTExpiresAt(token string) time.Time {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return time.Time{}
	}
	raw, ok := claims["exp"]
	if !ok {
		return time.Time{}
	}
	expiresAt, err := parseCLIProxyAPITimeValue(raw)
	if err != nil {
		return time.Time{}
	}
	return expiresAt
}

func parseJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("jwt has no payload")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
		}
		return value
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func cloneStringAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[strings.TrimSpace(key)]
	if !ok {
		return ""
	}
	value, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func stringValueAny(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[strings.TrimSpace(key)]
	if !ok || raw == nil {
		return ""
	}
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return ""
		}
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return ""
	}
}
