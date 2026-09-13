package auth

import (
	"errors"
	"fmt"
	"sort"

	"github.com/gantry-tools/gantry-core/operation"
)

type ActorKind string

const (
	HumanActor       ActorKind = "human"
	APITokenActor    ActorKind = "api-token"
	ServiceActor     ActorKind = "service"
	ClusterNodeActor ActorKind = "cluster-node"
)

type ExecutionMode string

const (
	LocalExecution   ExecutionMode = "local"
	RemoteExecution  ExecutionMode = "remote"
	ClusterExecution ExecutionMode = "cluster"
)

// Actor is an authenticated principal. Project and Installation are mandatory
// isolation dimensions: matching account IDs in two installations do not
// identify the same human, and cluster nodes do not acquire account IDs.
type Actor struct {
	Kind         ActorKind `json:"kind"`
	Project      string    `json:"project"`
	Installation string    `json:"installation"`
	Subject      string    `json:"subject"`
	AccountID    string    `json:"account_id,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	TokenScopes  []string  `json:"token_scopes,omitempty"`
}

type Execution struct {
	Mode           ExecutionMode `json:"mode"`
	Project        string        `json:"project"`
	Installation   string        `json:"installation"`
	TargetNode     string        `json:"target_node,omitempty"`
	Actor          Actor         `json:"actor"`
	RequestID      string        `json:"request_id,omitempty"`
	IdempotencyKey string        `json:"idempotency_key,omitempty"`
}

func (a Actor) Validate() error {
	if a.Project == "" || a.Installation == "" || a.Subject == "" {
		return errors.New("actor project, installation and subject are required")
	}
	switch a.Kind {
	case HumanActor:
		if a.AccountID == "" {
			return errors.New("human actor requires a local account id")
		}
		if len(a.TokenScopes) != 0 {
			return errors.New("human actor cannot carry API token scopes")
		}
	case APITokenActor:
		if a.AccountID != "" || len(a.Capabilities) != 0 || len(a.TokenScopes) == 0 {
			return errors.New("API token actor carries scopes, never an account or human capabilities")
		}
	case ServiceActor, ClusterNodeActor:
		if a.AccountID != "" || len(a.Capabilities) != 0 || len(a.TokenScopes) != 0 {
			return errors.New("service and cluster-node actors cannot inherit human or token authority")
		}
	default:
		return fmt.Errorf("invalid actor kind %q", a.Kind)
	}
	return nil
}

func (e Execution) Validate() error {
	if err := e.Actor.Validate(); err != nil {
		return err
	}
	if e.Project == "" || e.Installation == "" {
		return errors.New("execution project and installation are required")
	}
	if e.Project != e.Actor.Project || e.Installation != e.Actor.Installation {
		return errors.New("actor is not local to the execution installation")
	}
	switch e.Mode {
	case LocalExecution, RemoteExecution:
		if e.TargetNode != "" {
			return errors.New("target node is only valid for cluster execution")
		}
	case ClusterExecution:
		if e.TargetNode == "" || e.Actor.Kind != ClusterNodeActor {
			return errors.New("cluster execution requires a cluster-node actor and target")
		}
	default:
		return fmt.Errorf("invalid execution mode %q", e.Mode)
	}
	return nil
}

// Authorize evaluates only the product-neutral boundary. Products resolve
// roles into capabilities and token grants before constructing an Actor.
func Authorize(contract operation.Contract, actor *Actor) error {
	if contract.Authorization.Boundary == operation.Public {
		return nil
	}
	if actor == nil {
		return errors.New("authentication required")
	}
	if err := actor.Validate(); err != nil {
		return err
	}
	switch contract.Authorization.Boundary {
	case operation.Session:
		if actor.Kind != HumanActor {
			return errors.New("human session required")
		}
	case operation.Capability:
		switch actor.Kind {
		case HumanActor:
			if !contains(actor.Capabilities, contract.Authorization.Capability) {
				return errors.New("capability denied")
			}
		case APITokenActor:
			if !containsAny(actor.TokenScopes, contract.Authorization.TokenScopes) {
				return errors.New("token scope denied")
			}
		default:
			return errors.New("human or API token actor required")
		}
	case operation.Service:
		if actor.Kind != ServiceActor {
			return errors.New("service identity required")
		}
	case operation.ClusterNode:
		if actor.Kind != ClusterNodeActor {
			return errors.New("cluster-node identity required")
		}
	default:
		return errors.New("unsupported authorization boundary")
	}
	return nil
}

func contains(values []string, want string) bool {
	values = append([]string(nil), values...)
	sort.Strings(values)
	index := sort.SearchStrings(values, want)
	return index < len(values) && values[index] == want
}

func containsAny(have, required []string) bool {
	for _, value := range required {
		if contains(have, value) {
			return true
		}
	}
	return false
}
