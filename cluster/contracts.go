package cluster

import "time"

type MemberView struct {
	Member               Member `json:"member"`
	Health               string `json:"health"`
	CompatibilityWarning string `json:"compatibility_warning,omitempty"`
}

type InvitationView struct {
	Invitation  Invitation `json:"invitation"`
	Fingerprint string     `json:"fingerprint,omitempty"`
}

type AuditView struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	NodeID string    `json:"node_id,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

type Overview struct {
	Local       Identity         `json:"local"`
	Members     []MemberView     `json:"members"`
	Pending     []InvitationView `json:"pending_invitations"`
	RecentAudit []AuditView      `json:"recent_audit,omitempty"`
}
