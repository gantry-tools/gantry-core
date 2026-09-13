package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	coreauth "github.com/gantry-tools/gantry-core/auth"
	"github.com/gantry-tools/gantry-core/operation"
)

func testContract(kind operation.Kind) operation.Contract {
	return operation.Contract{SchemaVersion: 1, ID: "items.get", Kind: kind, Route: operation.Route{Method: "GET", Path: "/api/items/{id}"}, CLI: &operation.CLI{Resource: "items", Verb: "get"}, Authorization: operation.Authorization{Boundary: operation.Capability, Capability: "items.read"}, Audit: operation.Audit{Event: "items.get", Required: kind != operation.Read}, Idempotency: operation.Idempotency{Supported: kind != operation.Read, RetrySafe: kind == operation.Read}, Automation: operation.Automatable}
}

func TestHTTPCallPathTokenAndRetry(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/items/a%2Fb" && r.URL.EscapedPath() != "/api/items/a%2Fb" {
			t.Errorf("path=%q escaped=%q", r.URL.Path, r.URL.EscapedPath())
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Request-ID") != "req-1" {
			t.Errorf("headers=%v", r.Header)
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a/b"}`))
	}))
	defer server.Close()
	client, err := New(Options{BaseURL: server.URL, MaxAttempts: 2, TokenSource: TokenSourceFunc(func(context.Context) (string, error) { return "token", nil })})
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]string
	if err := client.Call(context.Background(), testContract(operation.Read), Request{PathValues: map[string]string{"id": "a/b"}, RequestID: "req-1"}, &output); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 || output["id"] != "a/b" {
		t.Fatalf("attempts=%d output=%v", attempts.Load(), output)
	}
}

func TestMutationDoesNotRetryWithoutIdempotency(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "unavailable", 503)
	}))
	defer server.Close()
	contract := testContract(operation.Mutation)
	contract.ID = "items.update"
	contract.Route.Method = "PUT"
	contract.CLI.Verb = "update"
	client, _ := New(Options{BaseURL: server.URL, MaxAttempts: 3})
	_ = client.Call(context.Background(), contract, Request{PathValues: map[string]string{"id": "one"}}, nil)
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
}

func TestProtectedFileTokenSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := FileTokenSource(path).Token(context.Background())
	if err != nil || token != "secret" {
		t.Fatalf("token=%q err=%v", token, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := FileTokenSource(path).Token(context.Background()); err == nil {
		t.Fatal("world-readable token unexpectedly accepted")
	}
}

func TestLocalExecutorUsesSameAuthorization(t *testing.T) {
	contract := testContract(operation.Read)
	executor := NewLocalExecutor()
	if err := executor.Register(contract, func(_ context.Context, _ coreauth.Execution, input json.RawMessage) (any, error) {
		return string(input), nil
	}); err != nil {
		t.Fatal(err)
	}
	actor := coreauth.Actor{Kind: coreauth.HumanActor, Project: "cortex", Installation: "one", Subject: "identity", AccountID: "account", Capabilities: []string{"items.read"}}
	execution := coreauth.Execution{Mode: coreauth.LocalExecution, Project: "cortex", Installation: "one", Actor: actor}
	output, err := executor.Call(context.Background(), contract, execution, json.RawMessage(`{"id":1}`))
	if err != nil || output != `{"id":1}` {
		t.Fatalf("output=%v err=%v", output, err)
	}
}
