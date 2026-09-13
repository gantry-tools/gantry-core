package cli

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestParseAutomationInvocation(t *testing.T) {
	got, err := Parse([]string{"accounts", "create", "--input", "-", "--json", "--yes", "--timeout=5s", "--request-id", "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := Invocation{Resource: "accounts", Verb: "create", Input: "-", Output: JSON, Confirm: true, Timeout: 5 * time.Second, RequestID: "req-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("invocation\n got: %#v\nwant: %#v", got, want)
	}
}

func TestOptionsMayPrecedeCommand(t *testing.T) {
	got, err := Parse([]string{"--url", "https://host.example/", "--token-file=/run/token", "status", "get"})
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://host.example" || got.TokenFile != "/run/token" {
		t.Fatalf("invocation = %#v", got)
	}
}

func TestRejectsUnsafeOrAmbiguousForms(t *testing.T) {
	for _, args := range [][]string{
		{"accounts", "list", "--token", "secret"},
		{"accounts", "list", "--token-file", "-"},
		{"accounts", "list", "--json", "--output", "table"},
		{"accounts"},
		{"Accounts", "list"},
		{"accounts", "list", "--timeout", "0s"},
	} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", args)
		}
	}
}

func TestHelpIsTyped(t *testing.T) {
	if _, err := Parse([]string{"--help"}); !errors.Is(err, ErrHelp) {
		t.Fatalf("error = %v", err)
	}
}

func TestExitCodesRemainStable(t *testing.T) {
	got := []int{ExitOK, ExitFailure, ExitUsage, ExitAuth, ExitNotFound, ExitConflict, ExitUnavailable, ExitPartial}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exit codes = %v", got)
	}
}
