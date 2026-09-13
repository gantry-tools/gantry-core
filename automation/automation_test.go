package automation

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gantry-tools/gantry-core/cli"
	"github.com/gantry-tools/gantry-core/operation"
)

func TestSessionLoginAndMutation(t *testing.T) {
	var csrfSeen bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "demo_session", Value: "secret"})
			_ = json.NewEncoder(w).Encode(map[string]string{"csrf": "csrf-1"})
		case "/items/7":
			if c, e := r.Cookie("demo_session"); e != nil || c.Value != "secret" {
				t.Errorf("cookie=%v err=%v", c, e)
			}
			csrfSeen = r.Header.Get("X-CSRF") == "csrf-1"
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	contracts := []operation.Contract{
		{SchemaVersion: 1, ID: "demo.auth.login", Kind: operation.Mutation, Route: operation.Route{Method: "POST", Path: "/login"}, CLI: &operation.CLI{Resource: "auth", Verb: "login", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Public}, Audit: operation.Audit{Required: true, Event: "demo.auth.login"}, Automation: operation.Automatable},
		{SchemaVersion: 1, ID: "demo.items.update", Kind: operation.Mutation, Route: operation.Route{Method: "PUT", Path: "/items/{id}"}, CLI: &operation.CLI{Resource: "items", Verb: "update", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Session}, Audit: operation.Audit{Required: true, Event: "demo.items.update"}, Automation: operation.Automatable},
	}
	dir := t.TempDir()
	sf := filepath.Join(dir, "session.json")
	opts := Options{Program: "demo", DefaultURL: s.URL, CookieName: "demo_session", CSRFHeader: "X-CSRF", CSRFFields: []string{"csrf"}, Stdin: strings.NewReader(`{"email":"x"}`), Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}
	if code := Run([]string{"auth", "login", "--input", "-", "--session-file", sf}, contracts, opts); code != 0 {
		t.Fatalf("login code=%d", code)
	}
	st, err := os.Stat(sf)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("session mode=%v err=%v", st.Mode().Perm(), err)
	}
	opts.Stdin = strings.NewReader(`{"name":"x"}`)
	if code := Run([]string{"items", "update", "7", "--input", "-", "--session-file", sf}, contracts, opts); code != 0 {
		t.Fatalf("update code=%d", code)
	}
	if !csrfSeen {
		t.Fatal("csrf header not sent")
	}
}

func TestProtectedCredentialFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply")
	}
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecret(p); err == nil {
		t.Fatal("expected insecure credential mode rejection")
	}
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readSecret(p); err != nil || got != "secret" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestStableHTTPExitCodes(t *testing.T) {
	statuses := map[int]int{401: cli.ExitAuth, 403: cli.ExitAuth, 404: cli.ExitNotFound, 409: cli.ExitConflict, 503: cli.ExitUnavailable, 500: cli.ExitFailure}
	for status, want := range statuses {
		if got := exitForStatus(status); got != want {
			t.Fatalf("status %d: got %d want %d", status, got, want)
		}
	}
}

func TestIdempotencyKeyUsesRequestID(t *testing.T) {
	var got string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer s.Close()
	contracts := []operation.Contract{{SchemaVersion: 1, ID: "demo.items.create", Kind: operation.Mutation, Route: operation.Route{Method: "POST", Path: "/items"}, CLI: &operation.CLI{Resource: "items", Verb: "create", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Public}, Schemas: operation.Schemas{Input: "demo.items.request.v1", Output: "demo.items.response.v1"}, Audit: operation.Audit{Required: true, Event: "demo.items.created"}, Idempotency: operation.Idempotency{Supported: true, RetrySafe: true}, Automation: operation.Automatable}}
	code := Run([]string{"items", "create", "--request-id", "req-123"}, contracts, Options{Program: "demo", DefaultURL: s.URL, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}})
	if code != 0 || got != "req-123" {
		t.Fatalf("code=%d key=%q", code, got)
	}
}

func TestAutomationInputQueryConfirmationAndTimeout(t *testing.T) {
	var query, body string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query().Get("page")
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer s.Close()
	c := operation.Contract{SchemaVersion: 1, ID: "demo.items.delete", Kind: operation.Destructive, Route: operation.Route{Method: "DELETE", Path: "/items/{id}"}, CLI: &operation.CLI{Resource: "items", Verb: "delete", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Public}, Schemas: operation.Schemas{Input: "items.request.v1", Output: "items.response.v1"}, Audit: operation.Audit{Required: true, Event: "demo.items.deleted"}, Automation: operation.Automatable}
	errOut := &strings.Builder{}
	if code := Run([]string{"items", "delete", "7"}, []operation.Contract{c}, Options{Program: "demo", DefaultURL: s.URL, Stderr: errOut}); code != cli.ExitUsage {
		t.Fatalf("unconfirmed code=%d", code)
	}
	if !strings.Contains(errOut.String(), "requires --yes") {
		t.Fatalf("stderr=%q", errOut.String())
	}
	in := strings.NewReader(`{"reason":"cleanup"}`)
	if code := Run([]string{"items", "delete", "7", "--yes", "--input", "-", "--query", "page=2", "--timeout", "2s", "--json"}, []operation.Contract{c}, Options{Program: "demo", DefaultURL: s.URL, Stdin: in, Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if query != "2" || body != `{"reason":"cleanup"}` {
		t.Fatalf("query=%q body=%q", query, body)
	}
}

func TestAutomationNetworkFailureIsUnavailable(t *testing.T) {
	c := operation.Contract{SchemaVersion: 1, ID: "demo.items.list", Kind: operation.Read, Route: operation.Route{Method: "GET", Path: "/items"}, CLI: &operation.CLI{Resource: "items", Verb: "list", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Public}, Schemas: operation.Schemas{Output: "items.response.v1"}, Idempotency: operation.Idempotency{RetrySafe: true}, Automation: operation.Automatable}
	if code := Run([]string{"items", "list", "--timeout", "10ms"}, []operation.Contract{c}, Options{Program: "demo", DefaultURL: "http://127.0.0.1:1", Stdout: &strings.Builder{}, Stderr: &strings.Builder{}}); code != cli.ExitUnavailable {
		t.Fatalf("code=%d", code)
	}
}
