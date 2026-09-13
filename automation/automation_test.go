package automation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
