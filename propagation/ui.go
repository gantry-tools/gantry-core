package propagation

import "time"

type UIKind struct {
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Reversible bool   `json:"reversible"`
	Mergeable  bool   `json:"mergeable"`
}
type UITarget struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Compatible bool   `json:"compatible"`
	Reason     string `json:"reason,omitempty"`
}
type UIProgress struct {
	Node   string `json:"node"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}
type UIHistoryEntry struct {
	PlanID     string    `json:"plan_id"`
	Actor      string    `json:"actor"`
	CreatedAt  time.Time `json:"created_at"`
	Target     string    `json:"target"`
	Applied    int       `json:"applied"`
	Failed     int       `json:"failed"`
	RolledBack int       `json:"rolled_back"`
}
type UIView struct {
	Kinds               []UIKind         `json:"kinds"`
	Targets             []UITarget       `json:"targets"`
	Preview             *Preview         `json:"preview,omitempty"`
	Progress            []UIProgress     `json:"progress,omitempty"`
	History             []UIHistoryEntry `json:"history,omitempty"`
	RequireConfirmation bool             `json:"require_confirmation"`
}

func PresentResult(r TransactionResult) []UIProgress {
	out := make([]UIProgress, 0, len(r.Results))
	for _, n := range r.Results {
		state := "pending"
		switch {
		case n.RolledBack:
			state = "rolled-back"
		case n.Error != "":
			state = "failed"
		case n.Applied:
			state = "applied"
		case n.Validated:
			state = "validated"
		}
		out = append(out, UIProgress{Node: n.Node, State: state, Detail: n.Error})
	}
	return out
}
