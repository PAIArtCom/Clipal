package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lansespirit/Clipal/internal/config"
)

type stubProviderClient struct {
	provider       config.OAuthProvider
	startSession   *LoginSession
	startErr       error
	exchangeCred   *Credential
	exchangeErr    error
	exchangeCode   string
	exchangeState  *LoginSession
	exchangeCalls  int32
	exchangeWait   chan struct{}
	refreshCred    *Credential
	refreshErr     error
	refreshCalled  int32
	refreshStarted chan<- struct{}
	refreshWait    <-chan struct{}
}

func (c *stubProviderClient) Provider() config.OAuthProvider { return c.provider }

func (c *stubProviderClient) StartLogin(_ time.Time, _ time.Duration) (*LoginSession, error) {
	if c.startErr != nil {
		return nil, c.startErr
	}
	if c.startSession == nil {
		return nil, fmt.Errorf("start session is nil")
	}
	clone := *c.startSession
	return &clone, nil
}

func (c *stubProviderClient) ExchangeSessionCode(ctx context.Context, session *LoginSession, code string) (*Credential, error) {
	atomic.AddInt32(&c.exchangeCalls, 1)
	c.exchangeCode = strings.TrimSpace(code)
	if session != nil {
		c.exchangeState = session.Clone()
	}
	if c.exchangeWait != nil {
		select {
		case <-c.exchangeWait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.exchangeErr != nil {
		return nil, c.exchangeErr
	}
	if c.exchangeCred == nil {
		return nil, fmt.Errorf("exchange credential is nil")
	}
	return c.exchangeCred.Clone(), nil
}

func (c *stubProviderClient) Refresh(_ context.Context, cred *Credential) (*Credential, error) {
	atomic.AddInt32(&c.refreshCalled, 1)
	if c.refreshStarted != nil {
		c.refreshStarted <- struct{}{}
	}
	if c.refreshWait != nil {
		<-c.refreshWait
	}
	if c.refreshErr != nil {
		return nil, c.refreshErr
	}
	if c.refreshCred != nil {
		return c.refreshCred.Clone(), nil
	}
	return cred.Clone(), nil
}

func TestRefreshBlocksTransferUntilItsCredentialSaveCompletes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	if err := NewStore(dir).Save(&Credential{
		Ref: "account", Provider: config.OAuthProviderCodex,
		Email: "owner@example.com", AccessToken: "access-before", RefreshToken: "refresh-before",
		ExpiresAt: now.Add(-time.Minute), LastRefresh: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	refreshStarted := make(chan struct{}, 1)
	releaseRefresh := make(chan struct{})
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithRefreshSkew(30*time.Second),
		WithProviderClient(&stubProviderClient{
			provider:       config.OAuthProviderCodex,
			refreshStarted: refreshStarted,
			refreshWait:    releaseRefresh,
			refreshCred: &Credential{
				Ref: "account", Provider: config.OAuthProviderCodex,
				Email: "owner@example.com", AccessToken: "access-after", RefreshToken: "refresh-after",
				ExpiresAt: now.Add(time.Hour), LastRefresh: now,
			},
		}),
	)

	refreshDone := make(chan error, 1)
	go func() {
		_, err := svc.RefreshIfNeeded(context.Background(), config.OAuthProviderCodex, "account")
		refreshDone <- err
	}()
	select {
	case <-refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("refresh did not reach the provider")
	}

	transferAcquired := make(chan func(), 1)
	go func() {
		transferAcquired <- LockStoreForTransfer()
	}()
	select {
	case release := <-transferAcquired:
		release()
		t.Fatal("transfer acquired the store while refresh was in flight")
	case <-time.After(20 * time.Millisecond):
	}

	close(releaseRefresh)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("RefreshIfNeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh did not finish")
	}
	select {
	case release := <-transferAcquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("transfer did not resume after refresh")
	}

	credential, err := NewStore(dir).Load(config.OAuthProviderCodex, "account")
	if err != nil {
		t.Fatalf("Load refreshed credential: %v", err)
	}
	if credential.AccessToken != "access-after" || credential.RefreshToken != "refresh-after" {
		t.Fatalf("credential was not refreshed: %#v", credential)
	}
}

func TestSessionPollExpiresAutomatically(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 16, 0, 0, 0, time.UTC)
	current := now

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return current }),
		WithSessionTTL(30*time.Second),
		WithCodexClient(&CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     "https://auth.openai.com/oauth/token",
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			Now:          func() time.Time { return current },
		}),
	)

	session, err := svc.StartLogin(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}

	current = current.Add(45 * time.Second)
	got, err := svc.PollLogin(session.ID)
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if got.Status != LoginStatusExpired {
		t.Fatalf("status = %q, want %q", got.Status, LoginStatusExpired)
	}
}

func TestStartLogin_SupersedesExistingPendingSessionOnSameCallbackPort(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener addr = %T, want *net.TCPAddr", ln.Addr())
	}
	port := tcpAddr.Port
	_ = ln.Close()

	svc := NewService(dir,
		WithCodexClient(&CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     "https://auth.openai.com/oauth/token",
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: port,
			CallbackPath: "/auth/callback",
		}),
	)

	first, err := svc.StartLogin(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("first StartLogin: %v", err)
	}
	second, err := svc.StartLogin(config.OAuthProviderCodex)
	if err != nil {
		t.Fatalf("second StartLogin: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected distinct session ids, got %q", first.ID)
	}

	got, err := svc.PollLogin(first.ID)
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if got.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", got.Status, LoginStatusError)
	}
	if !strings.Contains(got.Error, "superseded") {
		t.Fatalf("error = %q, want superseded message", got.Error)
	}
}

func TestStartLogin_ReturnsFriendlyErrorWhenCallbackPortIsBusy(t *testing.T) {
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() {
		_ = ln.Close()
	}()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener addr = %T, want *net.TCPAddr", ln.Addr())
	}
	port := tcpAddr.Port

	svc := NewService(dir,
		WithCodexClient(&CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     "https://auth.openai.com/oauth/token",
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: port,
			CallbackPath: "/auth/callback",
		}),
	)

	_, err = svc.StartLogin(config.OAuthProviderCodex)
	if err == nil {
		t.Fatalf("expected StartLogin to fail when callback port is busy")
	}
	if !strings.Contains(err.Error(), "callback port") {
		t.Fatalf("error = %q, want friendly callback port message", err.Error())
	}
}

func TestCompleteLoginWithCode_CompletesSessionAndClosesCallback(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		exchangeCred: &Credential{
			Ref:          "gemini-sean-example-com",
			Provider:     config.OAuthProviderGemini,
			Email:        "sean@example.com",
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
		},
	}
	svc := NewService(dir, WithProviderClient(client))

	svc.sessions["session-123"] = &LoginSession{
		ID:          "session-123",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-123",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	session, err := svc.CompleteLoginWithCode(context.Background(), "session-123", "manual-code")
	if err != nil {
		t.Fatalf("CompleteLoginWithCode: %v", err)
	}
	if session.Status != LoginStatusCompleted {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusCompleted)
	}
	if session.CredentialRef != "gemini-sean-example-com" {
		t.Fatalf("credential_ref = %q, want gemini-sean-example-com", session.CredentialRef)
	}
	if client.exchangeCode != "manual-code" {
		t.Fatalf("exchange code = %q, want manual-code", client.exchangeCode)
	}
	if client.exchangeState == nil {
		t.Fatalf("expected exchange session snapshot")
	}
	if client.exchangeState.redirectURI != redirectURI {
		t.Fatalf("redirect_uri = %q, want %q", client.exchangeState.redirectURI, redirectURI)
	}
	if client.exchangeState.pkce.CodeVerifier != "verifier" {
		t.Fatalf("code_verifier = %q, want verifier", client.exchangeState.pkce.CodeVerifier)
	}

	loaded, err := svc.Load(config.OAuthProviderGemini, "gemini-sean-example-com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Email != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", loaded.Email)
	}

	parsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	ln, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("net.Listen: callback port still busy after manual completion: %v", err)
	}
	_ = ln.Close()
}

func TestCancelLogin_ClosesPendingCallback(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}

	svc := NewService(dir)
	svc.sessions["session-cancel"] = &LoginSession{
		ID:          "session-cancel",
		Provider:    config.OAuthProviderCodex,
		AuthURL:     "https://auth.openai.com/oauth/authorize?state=session-cancel",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		redirectURI: redirectURI,
		callback:    callback,
	}

	session, err := svc.CancelLogin("session-cancel")
	if err != nil {
		t.Fatalf("CancelLogin: %v", err)
	}
	if session.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusError)
	}
	if session.Error != "oauth session cancelled" {
		t.Fatalf("error = %q, want oauth session cancelled", session.Error)
	}

	ln, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("net.Listen: callback port still busy after cancel: %v", err)
	}
	_ = ln.Close()
}

func TestCompleteLoginWithCode_ClaudeRequiresStateInManualInput(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderClaude,
	}
	svc := NewService(dir, WithProviderClient(client))

	svc.sessions["session-claude"] = &LoginSession{
		ID:          "session-claude",
		Provider:    config.OAuthProviderClaude,
		AuthURL:     "https://claude.com/cai/oauth/authorize?state=session-claude",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	_, err = svc.CompleteLoginWithCode(context.Background(), "session-claude", "manual-code")
	if err == nil {
		t.Fatalf("expected missing-state manual Claude input to fail")
	}
	if !errors.Is(err, ErrInvalidAuthorizationResponse) {
		t.Fatalf("error = %v, want ErrInvalidAuthorizationResponse", err)
	}
	if !strings.Contains(err.Error(), "requires the full callback URL") {
		t.Fatalf("error = %q, want full callback url guidance", err.Error())
	}
	if client.exchangeCode != "" {
		t.Fatalf("exchange code = %q, want empty", client.exchangeCode)
	}
	if session := svc.sessions["session-claude"]; session == nil || session.Status != LoginStatusPending {
		t.Fatalf("session = %#v, want pending", session)
	} else if session.callback == nil {
		t.Fatalf("expected callback to remain active")
	}
}

func TestCompleteLoginWithCode_ReturnsNotFoundForUnknownSession(t *testing.T) {
	svc := NewService(t.TempDir())

	_, err := svc.CompleteLoginWithCode(context.Background(), "missing-session", "manual-code")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("error = %v, want ErrSessionNotFound", err)
	}
}

func TestSweepExpiredSessionsLocked_RemovesOldTerminalSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 16, 0, 0, 0, time.UTC)
	current := now

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return current }),
		WithTerminalSessionRetention(time.Minute),
	)
	svc.sessions["completed-old"] = &LoginSession{
		ID:         "completed-old",
		Provider:   config.OAuthProviderCodex,
		Status:     LoginStatusCompleted,
		terminalAt: now.Add(-2 * time.Minute),
	}
	svc.sessions["error-fresh"] = &LoginSession{
		ID:         "error-fresh",
		Provider:   config.OAuthProviderCodex,
		Status:     LoginStatusError,
		terminalAt: now.Add(-30 * time.Second),
	}

	svc.mu.Lock()
	svc.sweepExpiredSessionsLocked()
	svc.mu.Unlock()

	if _, ok := svc.sessions["completed-old"]; ok {
		t.Fatalf("expected old terminal session to be removed")
	}
	if _, ok := svc.sessions["error-fresh"]; !ok {
		t.Fatalf("expected fresh terminal session to remain")
	}
}

func TestCompleteLoginWithCode_MarksSessionErrorAndClosesCallbackOnExchangeFailure(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider:    config.OAuthProviderGemini,
		exchangeErr: errors.New("token exchange failed"),
	}
	svc := NewService(dir, WithProviderClient(client))

	svc.sessions["session-err"] = &LoginSession{
		ID:          "session-err",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-err",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	session, err := svc.CompleteLoginWithCode(context.Background(), "session-err", "manual-code")
	if err != nil {
		t.Fatalf("CompleteLoginWithCode: %v", err)
	}
	if session.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusError)
	}
	if !strings.Contains(session.Error, "token exchange failed") {
		t.Fatalf("error = %q, want exchange failure", session.Error)
	}

	polled, err := svc.PollLogin("session-err")
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if polled.Status != LoginStatusError {
		t.Fatalf("polled status = %q, want %q", polled.Status, LoginStatusError)
	}

	parsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	ln, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("net.Listen: callback port still busy after failure: %v", err)
	}
	_ = ln.Close()
}

func TestCompleteLoginWithCode_AcceptsCallbackURLInput(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		exchangeCred: &Credential{
			Ref:          "gemini-sean-example-com",
			Provider:     config.OAuthProviderGemini,
			Email:        "sean@example.com",
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
		},
	}
	svc := NewService(dir, WithProviderClient(client))

	svc.sessions["session-url"] = &LoginSession{
		ID:          "session-url",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-url",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	callbackURL := "http://127.0.0.1:4444/oauth2callback?code=manual-code&state=session-url"
	session, err := svc.CompleteLoginWithCode(context.Background(), "session-url", callbackURL)
	if err != nil {
		t.Fatalf("CompleteLoginWithCode: %v", err)
	}
	if session.Status != LoginStatusCompleted {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusCompleted)
	}
	if client.exchangeCode != "manual-code" {
		t.Fatalf("exchange code = %q, want manual-code", client.exchangeCode)
	}
}

func TestCompleteLoginWithCode_ReturnsInvalidAuthorizationResponseWithoutMutatingSession(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	svc := NewService(dir, WithProviderClient(&stubProviderClient{provider: config.OAuthProviderGemini}))
	svc.sessions["session-invalid"] = &LoginSession{
		ID:          "session-invalid",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-invalid",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	_, err = svc.CompleteLoginWithCode(context.Background(), "session-invalid", "http://127.0.0.1:4444/oauth2callback?foo=bar")
	if !errors.Is(err, ErrInvalidAuthorizationResponse) {
		t.Fatalf("error = %v, want ErrInvalidAuthorizationResponse", err)
	}

	session := svc.sessions["session-invalid"]
	if session.Status != LoginStatusPending {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusPending)
	}
	if session.callback == nil {
		t.Fatalf("expected callback to remain active")
	}
}

func TestCompleteLoginWithCode_MarksSessionErrorOnManualStateMismatch(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		exchangeCred: &Credential{
			Ref:      "gemini-sean-example-com",
			Provider: config.OAuthProviderGemini,
			Email:    "sean@example.com",
		},
	}
	svc := NewService(dir, WithProviderClient(client))
	svc.sessions["session-mismatch"] = &LoginSession{
		ID:          "session-mismatch",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-mismatch",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	session, err := svc.CompleteLoginWithCode(context.Background(), "session-mismatch", "code=manual-code&state=other-session")
	if err != nil {
		t.Fatalf("CompleteLoginWithCode: %v", err)
	}
	if session.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusError)
	}
	if session.Error != "oauth state mismatch" {
		t.Fatalf("error = %q, want oauth state mismatch", session.Error)
	}
	if client.exchangeCode != "" {
		t.Fatalf("exchange code = %q, want empty", client.exchangeCode)
	}

	parsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	ln, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("net.Listen: callback port still busy after state mismatch: %v", err)
	}
	_ = ln.Close()
}

func TestRefreshIfNeededCoalescesConcurrentCallers(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 18, 0, 0, 0, time.UTC)
	var refreshCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req codexRefreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got := req.GrantType; got != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", got)
		}
		if got := req.RefreshToken; got != "refresh-1" {
			t.Fatalf("refresh_token = %q, want refresh-1", got)
		}
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-2","refresh_token":"refresh-2","id_token":"%s","expires_in":3600}`, testJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	store := NewStore(dir)
	if err := store.Save(&Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(10 * time.Second),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithRefreshSkew(30*time.Second),
		WithCodexClient(&CodexClient{
			AuthURL:      "https://auth.openai.com/oauth/authorize",
			TokenURL:     tokenServer.URL,
			ClientID:     "test-client",
			CallbackHost: "127.0.0.1",
			CallbackPort: 0,
			CallbackPath: "/auth/callback",
			HTTPClient:   tokenServer.Client(),
			Now:          func() time.Time { return now },
		}),
	)

	const callers = 6
	var wg sync.WaitGroup
	results := make(chan *Credential, callers)
	errors := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cred, err := svc.RefreshIfNeeded(context.Background(), config.OAuthProviderCodex, "codex-sean-example-com")
			if err != nil {
				errors <- err
				return
			}
			results <- cred
		}()
	}
	wg.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Fatalf("RefreshIfNeeded: %v", err)
	}
	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	for cred := range results {
		if cred.AccessToken != "access-2" {
			t.Fatalf("access token = %q, want access-2", cred.AccessToken)
		}
		if cred.RefreshToken != "refresh-2" {
			t.Fatalf("refresh token = %q, want refresh-2", cred.RefreshToken)
		}
	}

	loaded, err := svc.Load(config.OAuthProviderCodex, "codex-sean-example-com")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != "access-2" {
		t.Fatalf("stored access token = %q, want access-2", loaded.AccessToken)
	}
}

func TestRefreshIfNeededCoalescesConcurrentCallersAcrossHTTPClients(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 18, 0, 0, 0, time.UTC)
	var refreshCalls int32

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&refreshCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"access_token":"access-2","refresh_token":"refresh-2","id_token":"%s","expires_in":3600}`, testJWT("sean@example.com", "acct_123")))
	}))
	defer tokenServer.Close()

	if err := NewStore(dir).Save(&Credential{
		Ref:          "codex-sean-example-com",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(10 * time.Second),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithRefreshSkew(30*time.Second),
		WithCodexClient(&CodexClient{
			TokenURL:   tokenServer.URL,
			ClientID:   "test-client",
			HTTPClient: tokenServer.Client(),
			Now:        func() time.Time { return now },
		}),
	)

	clients := []*http.Client{
		{Transport: tokenServer.Client().Transport},
		{Transport: tokenServer.Client().Transport},
	}
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(client *http.Client) {
			defer wg.Done()
			cred, err := svc.RefreshIfNeededWithHTTPClient(context.Background(), config.OAuthProviderCodex, "codex-sean-example-com", client)
			if err != nil {
				t.Errorf("RefreshIfNeededWithHTTPClient: %v", err)
				return
			}
			if cred.AccessToken != "access-2" {
				t.Errorf("access token = %q, want access-2", cred.AccessToken)
			}
		}(client)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&refreshCalls); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

func TestRefreshIfNeeded_SkipsUnreadableNeighborCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 18, 18, 0, 0, 0, time.UTC)
	store := NewStore(dir)
	if err := store.Save(&Credential{
		Ref:          "codex-good-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccountID:    "acct_123",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(-time.Minute),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	badPath := store.preferredPath(config.OAuthProviderCodex, "broken@example.com", "codex-bad-ref")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithRefreshSkew(30*time.Second),
		WithProviderClient(&stubProviderClient{
			provider: config.OAuthProviderCodex,
			refreshCred: &Credential{
				Ref:          "codex-good-ref",
				Provider:     config.OAuthProviderCodex,
				Email:        "sean@example.com",
				AccountID:    "acct_123",
				AccessToken:  "access-2",
				RefreshToken: "refresh-2",
				ExpiresAt:    now.Add(time.Hour),
				LastRefresh:  now,
			},
		}),
	)

	cred, err := svc.RefreshIfNeeded(context.Background(), config.OAuthProviderCodex, "codex-good-ref")
	if err != nil {
		t.Fatalf("RefreshIfNeeded: %v", err)
	}
	if cred.AccessToken != "access-2" {
		t.Fatalf("access_token = %q, want access-2", cred.AccessToken)
	}
}

func TestStartCallbackServer_CallbackPageClosesWithoutOpenerDependency(t *testing.T) {
	server, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}
	defer func() {
		_ = server.Close()
	}()

	resp, err := http.Get(redirectURI + "?code=code-123&state=session-1")
	if err != nil {
		t.Fatalf("http.Get: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	if got := string(body); !strings.Contains(got, "window.close();") {
		t.Fatalf("callback page = %q, want window.close()", got)
	}
	if got := string(body); strings.Contains(got, "window.opener") {
		t.Fatalf("callback page = %q, did not expect opener dependency", got)
	}
}

func TestStartLogin_CallbackHandlerRendersFinalSuccessPage(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/oauth2callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		startSession: &LoginSession{
			ID:          "session-success",
			Provider:    config.OAuthProviderGemini,
			AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?redirect_uri=" + url.QueryEscape(redirectURI) + "&state=session-success",
			Status:      LoginStatusPending,
			ExpiresAt:   time.Now().Add(5 * time.Minute),
			pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
			redirectURI: redirectURI,
			callback:    callback,
		},
		exchangeCred: &Credential{
			Ref:          "gemini-sean-example-com",
			Provider:     config.OAuthProviderGemini,
			Email:        "sean@example.com",
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
		},
	}
	svc := NewService(dir, WithProviderClient(client))

	session, err := svc.StartLogin(config.OAuthProviderGemini)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}

	resp, err := http.Get(redirectURI + "?code=auth-code&state=" + url.QueryEscape(session.ID))
	if err != nil {
		t.Fatalf("http.Get callback: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	if got := string(body); !strings.Contains(got, "Gemini authorized") {
		t.Fatalf("callback page = %q, want Gemini authorized", got)
	}
	if got := string(body); !strings.Contains(got, "sean@example.com") {
		t.Fatalf("callback page = %q, want authorized email", got)
	}
	if got := string(body); strings.Contains(got, "window.opener") {
		t.Fatalf("callback page = %q, did not expect opener dependency", got)
	}

	completed, err := svc.PollLogin(session.ID)
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if completed.Status != LoginStatusCompleted {
		t.Fatalf("status = %q, want %q", completed.Status, LoginStatusCompleted)
	}
	if completed.Email != "sean@example.com" {
		t.Fatalf("email = %q, want sean@example.com", completed.Email)
	}
}

func TestStartLogin_CallbackHandlerRendersFinalFailurePage(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/oauth2callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		startSession: &LoginSession{
			ID:          "session-failure",
			Provider:    config.OAuthProviderGemini,
			AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?redirect_uri=" + url.QueryEscape(redirectURI) + "&state=session-failure",
			Status:      LoginStatusPending,
			ExpiresAt:   time.Now().Add(5 * time.Minute),
			pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
			redirectURI: redirectURI,
			callback:    callback,
		},
		exchangeErr: errors.New("token exchange failed"),
	}
	svc := NewService(dir, WithProviderClient(client))

	session, err := svc.StartLogin(config.OAuthProviderGemini)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}

	resp, err := http.Get(redirectURI + "?code=auth-code&state=" + url.QueryEscape(session.ID))
	if err != nil {
		t.Fatalf("http.Get callback: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	if got := string(body); !strings.Contains(got, "Authorization failed") {
		t.Fatalf("callback page = %q, want Authorization failed", got)
	}
	if got := string(body); !strings.Contains(got, "token exchange failed") {
		t.Fatalf("callback page = %q, want exchange failure detail", got)
	}

	failed, err := svc.PollLogin(session.ID)
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if failed.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", failed.Status, LoginStatusError)
	}
	if failed.Error != "token exchange failed" {
		t.Fatalf("error = %q, want token exchange failed", failed.Error)
	}
}

func TestStartLogin_CallbackHandlerRendersProviderErrorDescription(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/oauth2callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		startSession: &LoginSession{
			ID:          "session-provider-error",
			Provider:    config.OAuthProviderGemini,
			AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?redirect_uri=" + url.QueryEscape(redirectURI) + "&state=session-provider-error",
			Status:      LoginStatusPending,
			ExpiresAt:   time.Now().Add(5 * time.Minute),
			pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
			redirectURI: redirectURI,
			callback:    callback,
		},
	}
	svc := NewService(dir, WithProviderClient(client))

	session, err := svc.StartLogin(config.OAuthProviderGemini)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}

	resp, err := http.Get(redirectURI + "?error=access_denied&error_description=" + url.QueryEscape("User denied access") + "&state=" + url.QueryEscape(session.ID))
	if err != nil {
		t.Fatalf("http.Get callback: %v", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("io.ReadAll: %v", err)
	}
	if got := string(body); !strings.Contains(got, "User denied access") {
		t.Fatalf("callback page = %q, want provider error description", got)
	}

	failed, err := svc.PollLogin(session.ID)
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if failed.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", failed.Status, LoginStatusError)
	}
	if failed.Error != "User denied access" {
		t.Fatalf("error = %q, want provider error description", failed.Error)
	}
}

func TestStartLogin_UsesRegisteredProviderClient(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 22, 8, 0, 0, 0, time.UTC)
	client := &stubProviderClient{
		provider: config.OAuthProviderClaude,
		startSession: &LoginSession{
			ID:        "session-123",
			Provider:  config.OAuthProviderClaude,
			AuthURL:   "https://claude.com/cai/oauth/authorize?state=session-123",
			Status:    LoginStatusPending,
			ExpiresAt: now.Add(5 * time.Minute),
		},
	}

	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(client),
	)

	session, err := svc.StartLogin(config.OAuthProviderClaude)
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	if session.Provider != config.OAuthProviderClaude {
		t.Fatalf("provider = %q, want %q", session.Provider, config.OAuthProviderClaude)
	}
	if session.AuthURL == "" {
		t.Fatalf("expected auth url, got %#v", session)
	}
}

func TestRefreshIfNeeded_ReturnsRefreshErrorWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 22, 8, 30, 0, 0, time.UTC)
	if err := NewStore(dir).Save(&Credential{
		Ref:          "claude-sean-example-com",
		Provider:     config.OAuthProviderClaude,
		Email:        "sean@example.com",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    now.Add(-time.Minute),
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	client := &stubProviderClient{
		provider:   config.OAuthProviderClaude,
		refreshErr: errors.New("refresh failed"),
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(client),
	)

	_, err := svc.RefreshIfNeeded(context.Background(), config.OAuthProviderClaude, "claude-sean-example-com")
	if err == nil || !strings.Contains(err.Error(), "refresh failed") {
		t.Fatalf("RefreshIfNeeded error = %v, want refresh failure", err)
	}
}

func TestCompleteLoginWithCode_SerializesConcurrentCompletionAttempts(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/oauth2callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderGemini,
		exchangeCred: &Credential{
			Ref:      "gemini-sean-example-com-project-123",
			Provider: config.OAuthProviderGemini,
			Email:    "sean@example.com",
		},
		exchangeWait: make(chan struct{}),
	}
	svc := NewService(dir, WithProviderClient(client))
	svc.sessions["session-race"] = &LoginSession{
		ID:          "session-race",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-race",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	type result struct {
		session *LoginSession
		err     error
	}
	manualResult := make(chan result, 1)
	go func() {
		session, err := svc.CompleteLoginWithCode(context.Background(), "session-race", "manual-code")
		manualResult <- result{session: session, err: err}
	}()

	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&client.exchangeCalls) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("exchange calls = %d, want 1", atomic.LoadInt32(&client.exchangeCalls))
		}
		time.Sleep(10 * time.Millisecond)
	}

	pageResult := make(chan callbackPage, 1)
	go func() {
		pageResult <- svc.handleCallbackResult("session-race", callbackResult{
			Code:  "manual-code",
			State: "session-race",
		})
	}()

	select {
	case page := <-pageResult:
		t.Fatalf("callback page returned before active completion finished: %#v", page)
	case <-time.After(50 * time.Millisecond):
	}
	if got := atomic.LoadInt32(&client.exchangeCalls); got != 1 {
		t.Fatalf("exchange calls = %d, want 1", got)
	}

	close(client.exchangeWait)

	gotManual := <-manualResult
	if gotManual.err != nil {
		t.Fatalf("CompleteLoginWithCode error = %v", gotManual.err)
	}
	if gotManual.session == nil || gotManual.session.Status != LoginStatusCompleted {
		t.Fatalf("manual session = %#v, want completed", gotManual.session)
	}

	page := <-pageResult
	if page.Title != "Gemini authorized" {
		t.Fatalf("page title = %q, want Gemini authorized", page.Title)
	}
	if got := atomic.LoadInt32(&client.exchangeCalls); got != 1 {
		t.Fatalf("exchange calls = %d, want 1", got)
	}
}

func TestCompleteLoginWithCode_ReusesHistoricalRefWhenIdentityBecomesMoreSpecific(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 4, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(dir)
	if err := store.Save(&Credential{
		Ref:          "codex-legacy-ref",
		Provider:     config.OAuthProviderCodex,
		Email:        "sean@example.com",
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		LastRefresh:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Save legacy credential: %v", err)
	}

	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/auth/callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider: config.OAuthProviderCodex,
		exchangeCred: &Credential{
			Ref:          "codex-sean-example-com",
			Provider:     config.OAuthProviderCodex,
			Email:        "sean@example.com",
			AccountID:    "acct_123",
			AccessToken:  "access-new",
			RefreshToken: "refresh-new",
			ExpiresAt:    now.Add(time.Hour),
			LastRefresh:  now,
		},
	}
	svc := NewService(dir,
		WithNowFunc(func() time.Time { return now }),
		WithProviderClient(client),
	)

	svc.sessions["session-upgrade"] = &LoginSession{
		ID:          "session-upgrade",
		Provider:    config.OAuthProviderCodex,
		AuthURL:     "https://auth.openai.com/oauth/authorize?state=session-upgrade",
		Status:      LoginStatusPending,
		ExpiresAt:   now.Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	session, err := svc.CompleteLoginWithCode(context.Background(), "session-upgrade", "manual-code")
	if err != nil {
		t.Fatalf("CompleteLoginWithCode: %v", err)
	}
	if session.CredentialRef != "codex-legacy-ref" {
		t.Fatalf("credential_ref = %q, want codex-legacy-ref", session.CredentialRef)
	}

	loaded, err := svc.Load(config.OAuthProviderCodex, "codex-legacy-ref")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccountID != "acct_123" {
		t.Fatalf("account_id = %q, want acct_123", loaded.AccountID)
	}
	if loaded.AccessToken != "access-new" {
		t.Fatalf("access_token = %q, want access-new", loaded.AccessToken)
	}
}

func TestHandleCallbackResult_UsesBoundedExchangeTimeout(t *testing.T) {
	dir := t.TempDir()
	callback, redirectURI, err := startCallbackServer("127.0.0.1", 0, "/oauth2callback")
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	client := &stubProviderClient{
		provider:     config.OAuthProviderGemini,
		exchangeWait: make(chan struct{}),
	}
	svc := NewService(dir,
		WithProviderClient(client),
		WithLoginExchangeTimeout(25*time.Millisecond),
	)
	svc.sessions["session-timeout"] = &LoginSession{
		ID:          "session-timeout",
		Provider:    config.OAuthProviderGemini,
		AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth?state=session-timeout",
		Status:      LoginStatusPending,
		ExpiresAt:   time.Now().Add(5 * time.Minute),
		pkce:        PKCECodes{CodeVerifier: "verifier", CodeChallenge: "challenge"},
		redirectURI: redirectURI,
		callback:    callback,
	}

	start := time.Now()
	page := svc.handleCallbackResult("session-timeout", callbackResult{
		Code:  "manual-code",
		State: "session-timeout",
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("handleCallbackResult took too long: %s", elapsed)
	}
	if page.Title != "Authorization failed" {
		t.Fatalf("page title = %q, want Authorization failed", page.Title)
	}
	if !strings.Contains(page.Message, "context deadline exceeded") {
		t.Fatalf("page message = %q, want context deadline exceeded", page.Message)
	}

	session, err := svc.PollLogin("session-timeout")
	if err != nil {
		t.Fatalf("PollLogin: %v", err)
	}
	if session.Status != LoginStatusError {
		t.Fatalf("status = %q, want %q", session.Status, LoginStatusError)
	}
}

func testJWT(email string, accountID string) string {
	header := `{"alg":"none","typ":"JWT"}`
	payload := fmt.Sprintf(`{"email":"%s","sub":"sub_123","https://api.openai.com/auth":{"chatgpt_account_id":"%s"}}`, email, accountID)
	return encodeSegment(header) + "." + encodeSegment(payload) + "."
}

func encodeSegment(v string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(v))
}
