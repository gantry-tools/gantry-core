package cluster

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

const PairingLifetime = 10 * time.Minute
const RotationLifetime = 10 * time.Minute
const MinimumCredentialBytes = 32

const (
	PairingPending  = "pending"
	PairingApproved = "approved"
	PairingRejected = "rejected"
	PairingExpired  = "expired"
	PairingUsed     = "used"
)

type Invitation struct {
	ID        string    `json:"id"`
	Token     string    `json:"token,omitempty"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type JoinRequest struct {
	ID              string    `json:"id"`
	NodeID          string    `json:"node_id"`
	InstallationID  string    `json:"installation_id"`
	DisplayName     string    `json:"display_name"`
	PublicEndpoint  string    `json:"public_endpoint"`
	PublicKey       string    `json:"public_key"`
	Capabilities    []string  `json:"capabilities"`
	ProtocolVersion int       `json:"protocol_version"`
	ProductVersion  string    `json:"product_version"`
	Fingerprint     string    `json:"fingerprint"`
	State           string    `json:"state"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type JoinSubmission struct {
	InvitationToken   string   `json:"invitation_token"`
	Identity          Identity `json:"identity"`
	CredentialForHost string   `json:"credential_for_host"`
}

type JoinReceipt struct {
	RequestID     string `json:"request_id"`
	RequestSecret string `json:"request_secret"`
	State         string `json:"state"`
}

type PairingResult struct {
	State      string    `json:"state"`
	Remote     *Identity `json:"remote,omitempty"`
	Credential string    `json:"credential,omitempty"`
}

func ValidateJoinSubmission(in JoinSubmission, now time.Time) error {
	if strings.TrimSpace(in.InvitationToken) == "" {
		return errors.New("cluster invitation token required")
	}
	if err := in.Identity.Validate(); err != nil {
		return err
	}
	if !strings.HasPrefix(in.Identity.PublicEndpoint, "https://") {
		return errors.New("cluster public endpoint must use https")
	}
	if len(in.CredentialForHost) < MinimumCredentialBytes {
		return errors.New("cluster credential too short")
	}
	_ = now
	return nil
}

func ValidateInvitation(inv Invitation, now time.Time) error {
	if inv.State != PairingPending {
		return errors.New("invitation unavailable")
	}
	if !inv.ExpiresAt.After(now) {
		return errors.New("invitation expired")
	}
	return nil
}

func ValidateTransition(from, to string) error {
	allowed := map[string]map[string]bool{
		PairingPending:  {PairingApproved: true, PairingRejected: true, PairingExpired: true, PairingUsed: true},
		PairingApproved: {PairingUsed: true},
	}
	if allowed[from][to] {
		return nil
	}
	return errors.New("invalid cluster pairing transition")
}

func NewSecret(n int) (string, error) {
	if n < MinimumCredentialBytes {
		n = MinimumCredentialBytes
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
