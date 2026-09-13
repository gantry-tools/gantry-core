package contracttest

import (
	"strings"
	"testing"

	"github.com/gantry-tools/gantry-core/operation"
)

func TestBuildMatrixAndCertification(t *testing.T) {
	c := operation.Contract{SchemaVersion: 1, ID: "items.create", Kind: operation.Mutation, Route: operation.Route{Method: "POST", Path: "/api/items"}, CLI: &operation.CLI{Resource: "items", Verb: "create", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Capability, Capability: "items.write"}, Schemas: operation.Schemas{Input: "items.create.request.v1", Output: "items.item.v1"}, Audit: operation.Audit{Event: "items.created", Required: true}, Idempotency: operation.Idempotency{Supported: true}, Automation: operation.Automatable}
	m := Manifest{SchemaVersion: 1, Project: "demo", Operations: []operation.Contract{c}, ObservedRoutes: []operation.Route{c.Route}, ObservedCommands: []operation.CLI{*c.CLI}, WebsiteOperations: []string{c.ID}, Evidence: map[string]Evidence{c.ID: {Website: true, Tests: []string{"TestCreate"}}}}
	if findings := CertificationFindings(m); len(findings) != 0 {
		t.Fatalf("findings=%v", findings)
	}
	md := BuildMatrix(m).Markdown()
	for _, want := range []string{"items.create", "POST /api/items", "items create", "items.write", "TestCreate"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestCertificationRejectsUnprovenAutomation(t *testing.T) {
	c := operation.Contract{SchemaVersion: 1, ID: "items.list", Kind: operation.Read, Route: operation.Route{Method: "GET", Path: "/api/items"}, CLI: &operation.CLI{Resource: "items", Verb: "list"}, Authorization: operation.Authorization{Boundary: operation.Session}, Audit: operation.Audit{}, Idempotency: operation.Idempotency{RetrySafe: true}, Automation: operation.Automatable}
	m := Manifest{SchemaVersion: 1, Project: "demo", Operations: []operation.Contract{c}, ObservedRoutes: []operation.Route{c.Route}}
	got := CertificationFindings(m)
	if len(got) < 3 {
		t.Fatalf("expected evidence/CLI/schema findings, got %v", got)
	}
}

func TestSecurityFindingsRejectPublicDestructive(t *testing.T) {
	m := Manifest{Project: "demo", Operations: []operation.Contract{{SchemaVersion: 1, ID: "demo.reset.apply", Kind: operation.Destructive, Route: operation.Route{Method: "POST", Path: "/reset"}, CLI: &operation.CLI{Resource: "reset", Verb: "apply", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Public}, Schemas: operation.Schemas{Input: "reset.request.v1", Output: "reset.response.v1"}, Audit: operation.Audit{Required: true, Event: "demo.reset.applied"}, Automation: operation.Automatable}}, ObservedRoutes: []operation.Route{{Method: "POST", Path: "/reset"}}, ObservedCommands: []operation.CLI{{Resource: "reset", Verb: "apply", Implemented: true}}, Evidence: map[string]Evidence{"demo.reset.apply": {Tests: []string{"reset_test"}}}}
	if len(SecurityFindings(m)) == 0 {
		t.Fatal("expected security finding")
	}
}

func TestAutomationFindingsDogfoodGrammar(t *testing.T) {
	m := Manifest{Project: "demo", Operations: []operation.Contract{{SchemaVersion: 1, ID: "demo.items.delete", Kind: operation.Destructive, Route: operation.Route{Method: "DELETE", Path: "/items/{id}"}, CLI: &operation.CLI{Resource: "items", Verb: "delete", Implemented: true}, Authorization: operation.Authorization{Boundary: operation.Session}, Schemas: operation.Schemas{Input: "items.request.v1", Output: "items.response.v1"}, Audit: operation.Audit{Required: true, Event: "demo.items.deleted"}, Automation: operation.Automatable}}}
	if f := AutomationFindings(m); len(f) != 0 {
		t.Fatalf("findings=%+v", f)
	}
}
