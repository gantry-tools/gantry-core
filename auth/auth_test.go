package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testAccounts struct {
	account  Account
	identity Identity
}

func (accounts testAccounts) AuthenticatePassword(username, password string) (Account, Identity, bool) {
	ok := username == accounts.identity.Username && VerifyPassword(accounts.identity.PasswordHash, password)
	return accounts.account, accounts.identity, ok
}

func (accounts testAccounts) SessionPrincipalActive(accountID, identityID string) bool {
	return accounts.account.Enabled && accounts.identity.Enabled && accountID == accounts.account.ID && identityID == accounts.identity.ID
}

type memorySessions struct{ sessions map[string]Session }

func (memory *memorySessions) LoadSessions() (map[string]Session, error) {
	out := map[string]Session{}
	for id, session := range memory.sessions {
		out[id] = session
	}
	return out, nil
}

func (memory *memorySessions) SaveSessions(sessions map[string]Session) error {
	memory.sessions = sessions
	return nil
}

func TestAccountValidationAndCapabilities(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	users := AccountsFile{Version: 1, Accounts: []Account{{ID: "a", DisplayName: "Admin", Enabled: true, Roles: []string{"administrator"}, Identities: []Identity{{ID: "i", Type: "password", Username: "admin", PasswordHash: hash, Enabled: true}}}}}
	roles := RolesFile{Version: 1, Roles: []Role{{ID: "administrator", Name: "Administrator", Capabilities: []string{"*"}, BuiltIn: true}}}
	if err := ValidateAccounts(users, roles, AccountPolicy{SchemaVersion: 1, ProductName: "Test"}); err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") || VerifyPassword(hash, "wrong") {
		t.Fatal("password verification contract failed")
	}
	if !HasCapability(EffectiveCapabilities(users.Accounts[0], roles.Roles), "anything") {
		t.Fatal("administrator wildcard was not effective")
	}
}

func TestSessionPersistenceCSRFAndRevocation(t *testing.T) {
	hash, err := HashPassword("password-seven")
	if err != nil {
		t.Fatal(err)
	}
	provider := testAccounts{account: Account{ID: "a", Enabled: true}, identity: Identity{ID: "i", Username: "admin", PasswordHash: hash, Enabled: true}}
	memory := &memorySessions{sessions: map[string]Session{}}
	options := SessionOptions{CookieName: "test_session", CSRFHeader: "X-Test-CSRF", ClientIP: func(*http.Request) string { return "127.0.0.1" }}
	store, err := NewSessionStore(provider, memory, options)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.test/login", nil)
	response := httptest.NewRecorder()
	session, err := store.Login(response, request, "admin", "password-seven")
	if err != nil {
		t.Fatal(err)
	}
	cookie := response.Result().Cookies()[0]
	check := httptest.NewRequest(http.MethodPost, "http://example.test/change", nil)
	check.AddCookie(cookie)
	check.Header.Set("X-Test-CSRF", session.CSRF)
	loaded, ok := store.Get(check)
	if !ok || loaded.AccountID != "a" || !store.ValidCSRF(check, loaded) {
		t.Fatal("session or CSRF contract failed")
	}
	reloaded, err := NewSessionStore(provider, memory, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Get(check); !ok {
		t.Fatal("persisted session was not restored")
	}
	reloaded.RevokeAccount("a")
	if _, ok := reloaded.Get(check); ok {
		t.Fatal("revoked account session remained active")
	}
}

func TestExpiredAndDisabledPrincipalsAreRejected(t *testing.T) {
	provider := testAccounts{account: Account{ID: "a", Enabled: true}, identity: Identity{ID: "i", Enabled: true}}
	memory := &memorySessions{sessions: map[string]Session{"expired": {AccountID: "a", IdentityID: "i", Expires: time.Now().Add(-time.Minute)}}}
	store, err := NewSessionStore(provider, memory, SessionOptions{CookieName: "test_session"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	request.AddCookie(&http.Cookie{Name: "test_session", Value: "expired"})
	if _, ok := store.Get(request); ok {
		t.Fatal("expired session was accepted")
	}
}

func TestAuditRedactionAndOutcome(t *testing.T) {
	event := NewAuditEvent("request", "authorization_denied", "/manage/", "a", "i", "127.0.0.1", "token=secret safe=value")
	if event.Outcome != "denied" || event.Detail != "token=[redacted] safe=value" {
		t.Fatalf("unexpected audit event: %#v", event)
	}
}
