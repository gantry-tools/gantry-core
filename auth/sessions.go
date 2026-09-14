package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrRateLimited       = errors.New("too many attempts")
	ErrInvalidCredential = errors.New("invalid credentials")
)

type Session struct {
	AccountID  string    `json:"account_id"`
	IdentityID string    `json:"identity_id"`
	CSRF       string    `json:"csrf"`
	Created    time.Time `json:"created,omitempty"`
	Expires    time.Time `json:"expires"`
	RemoteIP   string    `json:"remote_ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

type SessionView struct {
	ID        string    `json:"id"`
	Created   time.Time `json:"created"`
	Expires   time.Time `json:"expires"`
	RemoteIP  string    `json:"remoteIp,omitempty"`
	UserAgent string    `json:"userAgent,omitempty"`
}

type Challenge struct {
	AccountID  string
	IdentityID string
	IP         string
	Expires    time.Time
}

type AccountProvider interface {
	AuthenticatePassword(username, password string) (Account, Identity, bool)
	SessionPrincipalActive(accountID, identityID string) bool
}

type SessionPersistence interface {
	LoadSessions() (map[string]Session, error)
	SaveSessions(map[string]Session) error
}

type SessionOptions struct {
	CookieName            string
	CSRFHeader            string
	SecureCookies         bool
	SessionTTL            time.Duration
	ChallengeTTL          time.Duration
	FailureWindow         time.Duration
	MaxFailures           int
	MaxSessionsPerAccount int
	ClientIP              func(*http.Request) string
	RequestScheme         func(*http.Request) string
}

func (options SessionOptions) withDefaults() SessionOptions {
	if options.CookieName == "" {
		options.CookieName = "gantry_session"
	}
	if options.CSRFHeader == "" {
		options.CSRFHeader = "X-Gantry-CSRF"
	}
	if options.SessionTTL <= 0 {
		options.SessionTTL = 12 * time.Hour
	}
	if options.ChallengeTTL <= 0 {
		options.ChallengeTTL = 5 * time.Minute
	}
	if options.FailureWindow <= 0 {
		options.FailureWindow = 10 * time.Minute
	}
	if options.MaxFailures <= 0 {
		options.MaxFailures = 8
	}
	if options.MaxSessionsPerAccount <= 0 {
		options.MaxSessionsPerAccount = 32
	}
	if options.ClientIP == nil {
		options.ClientIP = func(request *http.Request) string { return request.RemoteAddr }
	}
	if options.RequestScheme == nil {
		options.RequestScheme = func(request *http.Request) string {
			if request.TLS != nil {
				return "https"
			}
			return "http"
		}
	}
	return options
}

type SessionStore struct {
	mu         sync.Mutex
	sessions   map[string]Session
	failures   map[string][]time.Time
	challenges map[string]Challenge
	accounts   AccountProvider
	persist    SessionPersistence
	options    SessionOptions
}

func NewSessionStore(accounts AccountProvider, persistence SessionPersistence, options SessionOptions) (*SessionStore, error) {
	store := &SessionStore{
		sessions: map[string]Session{}, failures: map[string][]time.Time{}, challenges: map[string]Challenge{},
		accounts: accounts, persist: persistence, options: options.withDefaults(),
	}
	if persistence == nil {
		return store, nil
	}
	sessions, err := persistence.LoadSessions()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for id, session := range sessions {
		if session.Expires.After(now) {
			store.sessions[id] = session
		}
	}
	return store, nil
}

func (store *SessionStore) Limited(ip string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-store.options.FailureWindow)
	remaining := store.failures[ip][:0]
	for _, failedAt := range store.failures[ip] {
		if failedAt.After(cutoff) {
			remaining = append(remaining, failedAt)
		}
	}
	store.failures[ip] = remaining
	return len(remaining) >= store.options.MaxFailures
}

func (store *SessionStore) Fail(ip string) {
	store.mu.Lock()
	store.failures[ip] = append(store.failures[ip], time.Now())
	store.mu.Unlock()
}

func (store *SessionStore) AuthenticatePassword(request *http.Request, username, password string) (Account, Identity, error) {
	ip := store.options.ClientIP(request)
	if store.Limited(ip) {
		return Account{}, Identity{}, ErrRateLimited
	}
	account, identity, ok := store.accounts.AuthenticatePassword(username, password)
	if !ok {
		store.Fail(ip)
		return Account{}, Identity{}, ErrInvalidCredential
	}
	return account, identity, nil
}

func (store *SessionStore) CreateSession(writer http.ResponseWriter, request *http.Request, accountID, identityID string) (Session, error) {
	sessionID, csrf := Token(32), Token(24)
	now := time.Now().UTC()
	session := Session{AccountID: accountID, IdentityID: identityID, CSRF: csrf, Created: now, Expires: now.Add(store.options.SessionTTL), RemoteIP: store.options.ClientIP(request), UserAgent: strings.TrimSpace(request.UserAgent())}
	store.mu.Lock()
	for id, current := range store.sessions {
		if now.After(current.Expires) {
			delete(store.sessions, id)
		}
	}
	type candidate struct {
		id      string
		created time.Time
	}
	owned := []candidate{}
	for id, current := range store.sessions {
		if current.AccountID == accountID {
			owned = append(owned, candidate{id: id, created: current.Created})
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].created.Before(owned[j].created) })
	for len(owned) >= store.options.MaxSessionsPerAccount {
		delete(store.sessions, owned[0].id)
		owned = owned[1:]
	}
	store.sessions[sessionID] = session
	delete(store.failures, store.options.ClientIP(request))
	err := store.saveLocked()
	store.mu.Unlock()
	if err != nil {
		return Session{}, err
	}
	http.SetCookie(writer, &http.Cookie{Name: store.options.CookieName, Value: sessionID, Path: "/", HttpOnly: true, Secure: store.options.SecureCookies || store.options.RequestScheme(request) == "https", SameSite: http.SameSiteStrictMode, MaxAge: int(store.options.SessionTTL.Seconds())})
	return session, nil
}

func (store *SessionStore) Login(writer http.ResponseWriter, request *http.Request, username, password string) (Session, error) {
	account, identity, err := store.AuthenticatePassword(request, username, password)
	if err != nil {
		return Session{}, err
	}
	return store.CreateSession(writer, request, account.ID, identity.ID)
}

func (store *SessionStore) BeginChallenge(request *http.Request, accountID, identityID string) string {
	id := Token(32)
	store.mu.Lock()
	now := time.Now()
	for key, challenge := range store.challenges {
		if challenge.Expires.Before(now) {
			delete(store.challenges, key)
		}
	}
	store.challenges[id] = Challenge{AccountID: accountID, IdentityID: identityID, IP: store.options.ClientIP(request), Expires: now.Add(store.options.ChallengeTTL)}
	store.mu.Unlock()
	return id
}

func (store *SessionStore) TakeChallenge(request *http.Request, id string) (Challenge, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	challenge, ok := store.challenges[id]
	delete(store.challenges, id)
	if !ok || time.Now().After(challenge.Expires) || challenge.IP != store.options.ClientIP(request) {
		return Challenge{}, false
	}
	return challenge, true
}

func (store *SessionStore) Logout(writer http.ResponseWriter, request *http.Request) {
	if cookie, err := request.Cookie(store.options.CookieName); err == nil {
		store.mu.Lock()
		delete(store.sessions, cookie.Value)
		_ = store.saveLocked()
		store.mu.Unlock()
	}
	http.SetCookie(writer, &http.Cookie{Name: store.options.CookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: store.options.SecureCookies || store.options.RequestScheme(request) == "https", SameSite: http.SameSiteStrictMode})
}

func (store *SessionStore) Get(request *http.Request) (Session, bool) {
	cookie, err := request.Cookie(store.options.CookieName)
	if err != nil {
		return Session{}, false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	session, ok := store.sessions[cookie.Value]
	if !ok || time.Now().After(session.Expires) || !store.accounts.SessionPrincipalActive(session.AccountID, session.IdentityID) {
		delete(store.sessions, cookie.Value)
		_ = store.saveLocked()
		return Session{}, false
	}
	return session, true
}

func (store *SessionStore) RevokeAll() {
	store.mu.Lock()
	store.sessions = map[string]Session{}
	_ = store.saveLocked()
	store.mu.Unlock()
}

func (store *SessionStore) RevokeAccount(accountID string) {
	store.mu.Lock()
	for id, session := range store.sessions {
		if session.AccountID == accountID {
			delete(store.sessions, id)
		}
	}
	_ = store.saveLocked()
	store.mu.Unlock()
}

func (store *SessionStore) RevokeIdentity(identityID string) {
	store.RevokeIdentityExcept(identityID, "")
}

func (store *SessionStore) RevokeIdentityExcept(identityID, keepSessionID string) {
	store.mu.Lock()
	for id, session := range store.sessions {
		if session.IdentityID == identityID && id != keepSessionID {
			delete(store.sessions, id)
		}
	}
	_ = store.saveLocked()
	store.mu.Unlock()
}

func (store *SessionStore) ListSessions(accountID string) []SessionView {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	out := []SessionView{}
	for id, session := range store.sessions {
		if session.Expires.Before(now) {
			delete(store.sessions, id)
			continue
		}
		if session.AccountID == accountID {
			out = append(out, SessionView{ID: DisplaySessionID(id), Created: session.Created, Expires: session.Expires, RemoteIP: session.RemoteIP, UserAgent: session.UserAgent})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (store *SessionStore) CountSessions(accountID string) int {
	return len(store.ListSessions(accountID))
}

func (store *SessionStore) RevokeSession(accountID, sessionID string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	for id, session := range store.sessions {
		if session.AccountID == accountID && DisplaySessionID(id) == sessionID {
			delete(store.sessions, id)
			_ = store.saveLocked()
			return true
		}
	}
	return false
}

// DisplaySessionID returns the opaque identifier exposed through session
// enumeration. It is derived from the bearer credential so the raw value is
// never returned through ordinary listing (a hash is one-way, so a listed ID
// cannot be replayed as the session credential), while remaining stable enough
// for revocation and current-session indication.
func DisplaySessionID(real string) string {
	sum := sha256.Sum256([]byte(real))
	return hex.EncodeToString(sum[:8])
}

func (store *SessionStore) CurrentSessionID(request *http.Request) string {
	cookie, err := request.Cookie(store.options.CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// SnapshotSessions returns a detached copy of the active session set. Expired
// sessions are removed before the snapshot is taken.
func (store *SessionStore) SnapshotSessions() map[string]Session {
	store.mu.Lock()
	defer store.mu.Unlock()
	now := time.Now()
	changed := false
	copy := make(map[string]Session, len(store.sessions))
	for id, session := range store.sessions {
		if !session.Expires.After(now) {
			delete(store.sessions, id)
			changed = true
			continue
		}
		copy[id] = session
	}
	if changed {
		_ = store.saveLocked()
	}
	return copy
}

func (store *SessionStore) saveLocked() error {
	if store.persist == nil {
		return nil
	}
	copy := make(map[string]Session, len(store.sessions))
	for id, session := range store.sessions {
		copy[id] = session
	}
	return store.persist.SaveSessions(copy)
}

func (store *SessionStore) ValidCSRF(request *http.Request, session Session) bool {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return true
	}
	return EqualToken(request.Header.Get(store.options.CSRFHeader), session.CSRF)
}

func EqualToken(got, want string) bool {
	return got != "" && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
