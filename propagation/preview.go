package propagation

import "fmt"

type PreviewIssueKind string

const (
	IssueIncompatibleSchema PreviewIssueKind = "incompatible-schema"
	IssueMissingDependency  PreviewIssueKind = "missing-dependency"
	IssueMissingSecret      PreviewIssueKind = "missing-secret"
	IssuePermissionDenied   PreviewIssueKind = "permission-denied"
)

type PreviewIssue struct {
	Kind   PreviewIssueKind `json:"kind"`
	Object string           `json:"object"`
	Detail string           `json:"detail"`
}
type TargetState struct {
	Existing         map[string]Existing
	SupportedSchemas map[string]int
	Dependencies     map[string]bool
	Secrets          MapSecrets
	Permissions      map[string]bool
}
type Preview struct {
	Items      []PlanItem     `json:"items"`
	Issues     []PreviewIssue `json:"issues"`
	Applicable bool           `json:"applicable"`
}

func DryRun(source []Envelope, target TargetState, allowDelete bool) (Preview, error) {
	items, err := Diff(source, target.Existing, allowDelete)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Items: items, Applicable: true}
	for _, e := range source {
		obj := e.Kind + "/" + e.ID
		if max, ok := target.SupportedSchemas[e.Kind]; !ok || e.SchemaVersion > max {
			p.Issues = append(p.Issues, PreviewIssue{Kind: IssueIncompatibleSchema, Object: obj, Detail: fmt.Sprintf("schema %d is unsupported", e.SchemaVersion)})
		}
		for _, dep := range e.Dependencies {
			if !target.Dependencies[dep] {
				p.Issues = append(p.Issues, PreviewIssue{Kind: IssueMissingDependency, Object: obj, Detail: dep})
			}
		}
		for _, s := range e.Secrets {
			if s.Required && !target.Secrets[s.Name] {
				p.Issues = append(p.Issues, PreviewIssue{Kind: IssueMissingSecret, Object: obj, Detail: s.Name})
			}
		}
		if e.Actor.Permission != "" && !target.Permissions[e.Actor.Permission] {
			p.Issues = append(p.Issues, PreviewIssue{Kind: IssuePermissionDenied, Object: obj, Detail: e.Actor.Permission})
		}
	}
	p.Applicable = len(p.Issues) == 0
	return p, nil
}
