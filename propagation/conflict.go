package propagation

import "fmt"

type Ownership struct {
	ObjectOwner string `json:"object_owner,omitempty"`
	SourceNode  string `json:"source_node"`
}

func ResolveConflict(e Envelope, d Existing) (Change, string) { return resolveConflict(e, d) }

func ValidateOwnership(policy ConflictPolicy, ownership Ownership) error {
	if policy != ConflictAuthoritative {
		return nil
	}
	if ownership.SourceNode == "" {
		return fmt.Errorf("source node is required for authoritative ownership")
	}
	if ownership.ObjectOwner != "" && ownership.ObjectOwner != ownership.SourceNode {
		return fmt.Errorf("object is owned by %s, not %s", ownership.ObjectOwner, ownership.SourceNode)
	}
	return nil
}
