package auth

import (
	"regexp"
	"strings"
	"time"
)

type AuditEvent struct {
	SchemaVersion int    `json:"schemaVersion"`
	RequestID     string `json:"requestId"`
	Action        string `json:"action"`
	Target        string `json:"target"`
	Outcome       string `json:"outcome"`
	AccountID     string `json:"accountId"`
	IdentityID    string `json:"identityId"`
	RemoteIP      string `json:"remoteIp"`
	Detail        string `json:"detail"`
	CreatedAt     int64  `json:"createdAt"`
}

var (
	auditSecretPattern = regexp.MustCompile(`(?i)(password|token|secret|credential|authorization|recovery|totp|api[_-]?key|session)\s*=\s*("[^"]*"|[^\s]+)`)
	auditJSONPattern   = regexp.MustCompile(`(?i)"(password|token|secret|credential|authorization|recovery|totp|api[_-]?key|session)"\s*:\s*"[^"]*"`)
	// auditControlPattern strips C0 control characters and DEL so client-supplied
	// audit detail (for example a workspace path) can never forge audit lines or
	// corrupt the structured history with control bytes.
	auditControlPattern = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

func RedactAuditDetail(detail string) string {
	detail = strings.ToValidUTF8(strings.TrimSpace(detail), "�")
	detail = auditControlPattern.ReplaceAllString(detail, "")
	detail = auditSecretPattern.ReplaceAllString(detail, "$1=[redacted]")
	detail = auditJSONPattern.ReplaceAllString(detail, `"$1": "[redacted]"`)
	if len(detail) > 4096 {
		detail = detail[:4096] + "[truncated]"
	}
	return detail
}

func AuditOutcome(action string) string {
	if strings.Contains(action, "failed") || strings.Contains(action, "error") || strings.Contains(action, "denied") {
		return "denied"
	}
	return "success"
}

func NewAuditEvent(requestID, action, target, accountID, identityID, remoteIP, detail string) AuditEvent {
	return AuditEvent{SchemaVersion: 1, RequestID: requestID, Action: action, Target: target, Outcome: AuditOutcome(action), AccountID: accountID, IdentityID: identityID, RemoteIP: remoteIP, Detail: RedactAuditDetail(detail), CreatedAt: time.Now().UnixMilli()}
}
