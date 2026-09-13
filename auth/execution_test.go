package auth

import (
	"testing"

	"github.com/gantry-tools/gantry-core/operation"
)

func executionContract(boundary operation.Boundary) operation.Contract {
	return operation.Contract{Authorization: operation.Authorization{Boundary: boundary, Capability: "accounts.manage", TokenScopes: []string{"accounts.write"}}}
}

func humanActor() Actor {
	return Actor{Kind: HumanActor, Project: "warden", Installation: "w-1", Subject: "identity-1", AccountID: "account-1", Capabilities: []string{"accounts.manage"}}
}

func TestExecutionKeepsAccountsLocal(t *testing.T) {
	execution := Execution{Mode: LocalExecution, Project: "cortex", Installation: "c-1", Actor: humanActor()}
	if err := execution.Validate(); err == nil {
		t.Fatal("Warden account unexpectedly became a Cortex actor")
	}
}

func TestActorKindsCannotInheritAuthority(t *testing.T) {
	actors := []Actor{
		{Kind: APITokenActor, Project: "warden", Installation: "w-1", Subject: "token-1", AccountID: "account-1", TokenScopes: []string{"accounts.write"}},
		{Kind: ClusterNodeActor, Project: "warden", Installation: "w-1", Subject: "node-1", Capabilities: []string{"accounts.manage"}},
	}
	for _, actor := range actors {
		if err := actor.Validate(); err == nil {
			t.Fatalf("actor unexpectedly valid: %#v", actor)
		}
	}
}

func TestAuthorizeExecutionBoundary(t *testing.T) {
	human := humanActor()
	if err := Authorize(executionContract(operation.Capability), &human); err != nil {
		t.Fatal(err)
	}
	token := Actor{Kind: APITokenActor, Project: "warden", Installation: "w-1", Subject: "token-1", TokenScopes: []string{"accounts.write"}}
	if err := Authorize(executionContract(operation.Capability), &token); err != nil {
		t.Fatal(err)
	}
	node := Actor{Kind: ClusterNodeActor, Project: "warden", Installation: "w-1", Subject: "node-1"}
	if err := Authorize(executionContract(operation.Capability), &node); err == nil {
		t.Fatal("cluster node unexpectedly crossed capability boundary")
	}
	if err := Authorize(executionContract(operation.ClusterNode), &node); err != nil {
		t.Fatal(err)
	}
}

func TestClusterExecutionRequiresNodeIdentity(t *testing.T) {
	human := humanActor()
	human.Project, human.Installation = "watchpost", "wp-1"
	execution := Execution{Mode: ClusterExecution, Project: "watchpost", Installation: "wp-1", TargetNode: "node-2", Actor: human}
	if err := execution.Validate(); err == nil {
		t.Fatal("human unexpectedly authorized as cluster transport actor")
	}
}
