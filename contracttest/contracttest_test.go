package contracttest

import (
	"strings"
	"testing"

	"github.com/gantry-tools/gantry-core/operation"
)

func op(id, method, path, resource, verb string) operation.Contract {
	kind := operation.Read
	audit := operation.Audit{}
	if method != "GET" {
		kind = operation.Mutation
		audit = operation.Audit{Required: true, Event: id}
	}
	return operation.Contract{SchemaVersion: 1, ID: id, Kind: kind, Route: operation.Route{Method: method, Path: path}, CLI: &operation.CLI{Resource: resource, Verb: verb}, Authorization: operation.Authorization{Boundary: operation.Session}, Audit: audit, Idempotency: operation.Idempotency{RetrySafe: kind == operation.Read}, Automation: operation.Automatable}
}

func validManifest() Manifest {
	return Manifest{SchemaVersion: 1, Project: "sample", Operations: []operation.Contract{
		op("items.list", "GET", "/api/items", "items", "list"),
		op("items.create", "POST", "/api/items", "items", "create"),
	}, ObservedRoutes: []operation.Route{{Method: "GET", Path: "/api/items"}, {Method: "POST", Path: "/api/items"}}, WebsiteOperations: []string{"items.list", "items.create"}}
}

func TestValidManifest(t *testing.T) {
	if err := Require(validManifest()); err != nil {
		t.Fatal(err)
	}
}

func TestDetectsRouteAndWebsiteDrift(t *testing.T) {
	manifest := validManifest()
	manifest.ObservedRoutes = append(manifest.ObservedRoutes, operation.Route{Method: "DELETE", Path: "/api/items/{id}"})
	manifest.WebsiteOperations = append(manifest.WebsiteOperations, "items.delete")
	err := Require(manifest)
	if err == nil || !strings.Contains(err.Error(), "route.undeclared") || !strings.Contains(err.Error(), "website.undeclared") {
		t.Fatalf("error = %v", err)
	}
}

func TestDocumentedExceptionMustRemainLive(t *testing.T) {
	manifest := validManifest()
	manifest.WebsiteOperations = append(manifest.WebsiteOperations, "auth.oauth.callback")
	manifest.Exceptions = []Exception{{Operation: "auth.oauth.callback", Reason: "browser redirect protocol"}}
	if err := Require(manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Operations = append(manifest.Operations, op("auth.oauth.callback", "GET", "/api/oauth/callback", "auth", "callback"))
	manifest.ObservedRoutes = append(manifest.ObservedRoutes, operation.Route{Method: "GET", Path: "/api/oauth/callback"})
	if err := Require(manifest); err == nil || !strings.Contains(err.Error(), "exception.stale") {
		t.Fatalf("error = %v", err)
	}
}

func TestFindingsAreDeterministic(t *testing.T) {
	manifest := validManifest()
	manifest.ObservedRoutes = nil
	report := Check(manifest)
	if len(report.Findings) != 2 || report.Findings[0].Subject != "items.create" || report.Findings[1].Subject != "items.list" {
		t.Fatalf("findings = %#v", report.Findings)
	}
}
