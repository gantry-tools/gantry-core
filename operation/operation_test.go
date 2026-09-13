package operation

import (
	"encoding/json"
	"os"
	"testing"
)

func fixture(t *testing.T) []Contract {
	t.Helper()
	body, err := os.ReadFile("testdata/compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	var contracts []Contract
	if err := json.Unmarshal(body, &contracts); err != nil {
		t.Fatal(err)
	}
	return contracts
}

func TestCompatibilityFixture(t *testing.T) {
	contracts := fixture(t)
	registry, err := NewRegistry(contracts...)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Contracts()) != 3 {
		t.Fatalf("contracts = %d", len(registry.Contracts()))
	}
	if got := registry.Contracts()[0].ID; got != "accounts.delete" {
		t.Fatalf("first sorted operation = %q", got)
	}
}

func TestRejectsMissingCLIForAutomatableOperation(t *testing.T) {
	contract := fixture(t)[1]
	contract.CLI = nil
	if err := contract.Validate(); err == nil {
		t.Fatal("expected validation failure")
	}
}

func TestCLIImplementationStateIsExplicit(t *testing.T) {
	contract := fixture(t)[1]
	if contract.CLI == nil || contract.CLI.Implemented {
		t.Fatalf("fixture CLI = %#v", contract.CLI)
	}
	contract.CLI.Implemented = true
	if err := contract.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsDuplicateRouteAndCommand(t *testing.T) {
	contracts := fixture(t)
	duplicateRoute := contracts[0]
	duplicateRoute.ID = "accounts.destroy"
	duplicateRoute.CLI = &CLI{Resource: "accounts", Verb: "destroy"}
	if _, err := NewRegistry(contracts[0], duplicateRoute); err == nil {
		t.Fatal("expected duplicate route failure")
	}
	duplicateCommand := contracts[0]
	duplicateCommand.ID = "accounts.destroy"
	duplicateCommand.Route.Path = "/api/accounts/{id}/destroy"
	if _, err := NewRegistry(contracts[0], duplicateCommand); err == nil {
		t.Fatal("expected duplicate command failure")
	}
}

func TestHumanAndNodeBoundariesStayDistinct(t *testing.T) {
	contract := fixture(t)[0]
	contract.Authorization = Authorization{Boundary: ClusterNode, Capability: "accounts.manage"}
	if err := contract.Validate(); err == nil {
		t.Fatal("cluster node must not inherit a human capability")
	}
}

func TestContractAcceptsColonSeparatedTokenScope(t *testing.T) {
	c := Contract{SchemaVersion: SchemaVersion, ID: "test.items.list", Kind: Read, Route: Route{Method: "GET", Path: "/items"}, CLI: &CLI{Resource: "items", Verb: "list", Implemented: true}, Authorization: Authorization{Boundary: Session, TokenScopes: []string{"sites:read"}}, Schemas: Schemas{Output: "items.response.v1"}, Idempotency: Idempotency{RetrySafe: true}, Automation: Automatable}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
