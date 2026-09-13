// Package operation defines the product-neutral contract connecting a Gantry
// application service operation to its HTTP and CLI adapters.
package operation

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = 1

type Kind string

const (
	Read        Kind = "read"
	Mutation    Kind = "mutation"
	Destructive Kind = "destructive"
)

type Boundary string

const (
	Public      Boundary = "public"
	Session     Boundary = "session"
	Capability  Boundary = "capability"
	Service     Boundary = "service"
	ClusterNode Boundary = "cluster-node"
)

type Automation string

const (
	Automatable       Automation = "automatable"
	BrowserProtocol   Automation = "browser-protocol"
	StreamingProtocol Automation = "streaming-protocol"
)

type Route struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type CLI struct {
	Resource string `json:"resource"`
	Verb     string `json:"verb"`
}

type Authorization struct {
	Boundary    Boundary `json:"boundary"`
	Capability  string   `json:"capability,omitempty"`
	TokenScopes []string `json:"token_scopes,omitempty"`
}

type Schemas struct {
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

type Audit struct {
	Event    string `json:"event,omitempty"`
	Required bool   `json:"required"`
}

type Idempotency struct {
	Supported bool `json:"supported"`
	Required  bool `json:"required"`
	RetrySafe bool `json:"retry_safe"`
}

type Contract struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	Kind          Kind          `json:"kind"`
	Route         Route         `json:"route"`
	CLI           *CLI          `json:"cli,omitempty"`
	Authorization Authorization `json:"authorization"`
	Schemas       Schemas       `json:"schemas"`
	Audit         Audit         `json:"audit"`
	Idempotency   Idempotency   `json:"idempotency"`
	Automation    Automation    `json:"automation"`
	SecretInputs  []string      `json:"secret_inputs,omitempty"`
	SecretOutputs []string      `json:"secret_outputs,omitempty"`
}

var (
	idPattern   = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)
	wordPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

func (c Contract) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("operation %q: unsupported schema version %d", c.ID, c.SchemaVersion)
	}
	if !idPattern.MatchString(c.ID) {
		return fmt.Errorf("operation id %q must be lowercase dotted words", c.ID)
	}
	if c.Kind != Read && c.Kind != Mutation && c.Kind != Destructive {
		return fmt.Errorf("operation %q: invalid kind %q", c.ID, c.Kind)
	}
	if err := validateRoute(c.Route); err != nil {
		return fmt.Errorf("operation %q: %w", c.ID, err)
	}
	if c.CLI == nil && c.Automation == Automatable {
		return fmt.Errorf("operation %q: automatable operation has no CLI mapping", c.ID)
	}
	if c.CLI != nil && (!wordPattern.MatchString(c.CLI.Resource) || !wordPattern.MatchString(c.CLI.Verb)) {
		return fmt.Errorf("operation %q: invalid CLI resource or verb", c.ID)
	}
	if err := validateAuthorization(c.Authorization); err != nil {
		return fmt.Errorf("operation %q: %w", c.ID, err)
	}
	if c.Kind != Read && !c.Audit.Required {
		return fmt.Errorf("operation %q: mutations must require audit", c.ID)
	}
	if c.Audit.Required && !idPattern.MatchString(c.Audit.Event) {
		return fmt.Errorf("operation %q: required audit event is invalid", c.ID)
	}
	if c.Idempotency.Required && !c.Idempotency.Supported {
		return fmt.Errorf("operation %q: required idempotency is not supported", c.ID)
	}
	if c.Idempotency.RetrySafe && !c.Idempotency.Supported && c.Kind != Read {
		return fmt.Errorf("operation %q: mutation cannot be retry-safe without idempotency", c.ID)
	}
	if c.Automation != Automatable && c.Automation != BrowserProtocol && c.Automation != StreamingProtocol {
		return fmt.Errorf("operation %q: invalid automation classification %q", c.ID, c.Automation)
	}
	if err := validatePointers(append(append([]string{}, c.SecretInputs...), c.SecretOutputs...)); err != nil {
		return fmt.Errorf("operation %q: %w", c.ID, err)
	}
	return nil
}

func validateRoute(route Route) error {
	switch route.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		return fmt.Errorf("invalid HTTP method %q", route.Method)
	}
	if !strings.HasPrefix(route.Path, "/") || strings.ContainsAny(route.Path, "?# ") {
		return fmt.Errorf("invalid HTTP path %q", route.Path)
	}
	return nil
}

func validateAuthorization(auth Authorization) error {
	switch auth.Boundary {
	case Public, Session, Service, ClusterNode:
		if auth.Capability != "" {
			return errors.New("capability is only valid at the capability boundary")
		}
	case Capability:
		if !idPattern.MatchString(auth.Capability) {
			return errors.New("capability boundary requires a dotted capability")
		}
	default:
		return fmt.Errorf("invalid authorization boundary %q", auth.Boundary)
	}
	for _, scope := range auth.TokenScopes {
		if !wordPattern.MatchString(scope) && !idPattern.MatchString(scope) {
			return fmt.Errorf("invalid token scope %q", scope)
		}
	}
	return nil
}

func validatePointers(values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || value[0] != '/' || seen[value] {
			return fmt.Errorf("secret fields must be unique JSON pointers: %q", value)
		}
		seen[value] = true
	}
	return nil
}

type Registry struct {
	contracts map[string]Contract
	routes    map[string]string
	commands  map[string]string
}

func NewRegistry(contracts ...Contract) (*Registry, error) {
	r := &Registry{contracts: map[string]Contract{}, routes: map[string]string{}, commands: map[string]string{}}
	for _, contract := range contracts {
		if err := r.Register(contract); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Register(contract Contract) error {
	if err := contract.Validate(); err != nil {
		return err
	}
	if _, exists := r.contracts[contract.ID]; exists {
		return fmt.Errorf("duplicate operation id %q", contract.ID)
	}
	routeKey := contract.Route.Method + " " + contract.Route.Path
	if owner, exists := r.routes[routeKey]; exists {
		return fmt.Errorf("route %q belongs to both %q and %q", routeKey, owner, contract.ID)
	}
	if contract.CLI != nil {
		commandKey := contract.CLI.Resource + " " + contract.CLI.Verb
		if owner, exists := r.commands[commandKey]; exists {
			return fmt.Errorf("CLI command %q belongs to both %q and %q", commandKey, owner, contract.ID)
		}
		r.commands[commandKey] = contract.ID
	}
	r.contracts[contract.ID] = contract
	r.routes[routeKey] = contract.ID
	return nil
}

func (r *Registry) Get(id string) (Contract, bool) {
	contract, ok := r.contracts[id]
	return contract, ok
}

func (r *Registry) Contracts() []Contract {
	result := make([]Contract, 0, len(r.contracts))
	for _, contract := range r.contracts {
		result = append(result, contract)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
