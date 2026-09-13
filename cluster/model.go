package cluster

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const ProtocolVersion = 1

const (
	MemberActive   = "active"
	MemberDisabled = "disabled"
	MemberRevoked  = "revoked"
)

const (
	HealthOnline   = "online"
	HealthOffline  = "offline"
	HealthDegraded = "degraded"
	HealthDisabled = "disabled"
	HealthRevoked  = "revoked"
)

type Identity struct {
	NodeID          string     `json:"node_id"`
	InstallationID  string     `json:"installation_id"`
	DisplayName     string     `json:"display_name"`
	PublicEndpoint  string     `json:"public_endpoint"`
	PublicKey       string     `json:"public_key"`
	Capabilities    []string   `json:"capabilities"`
	ProtocolVersion int        `json:"protocol_version"`
	ProductVersion  string     `json:"product_version"`
	CreatedAt       time.Time  `json:"created_at"`
	PairedAt        *time.Time `json:"paired_at,omitempty"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
}

func (i Identity) Fingerprint() string {
	key, _ := base64.RawURLEncoding.DecodeString(i.PublicKey)
	if len(key) == 0 {
		return ""
	}
	sum := sha256.Sum256(key)
	return fmt.Sprintf("%x", sum[:8])
}

func (i Identity) Validate() error {
	if strings.TrimSpace(i.NodeID) == "" || strings.TrimSpace(i.InstallationID) == "" {
		return errors.New("cluster identity requires node and installation ids")
	}
	if i.ProtocolVersion <= 0 {
		return errors.New("cluster identity requires protocol version")
	}
	if i.PublicEndpoint != "" && !strings.HasPrefix(i.PublicEndpoint, "https://") {
		return errors.New("cluster public endpoint must use https")
	}
	if i.PublicKey == "" {
		return errors.New("cluster identity requires public key")
	}
	return nil
}

type Member struct {
	Identity
	State             string     `json:"state"`
	LastLatencyMS     int64      `json:"last_latency_ms,omitempty"`
	DisabledAt        *time.Time `json:"disabled_at,omitempty"`
	CredentialVersion int        `json:"credential_version"`
}

func (m Member) Health(now time.Time, offlineAfter time.Duration) string {
	switch m.State {
	case MemberDisabled:
		return HealthDisabled
	case MemberRevoked:
		return HealthRevoked
	case MemberActive:
	default:
		return HealthDegraded
	}
	if m.LastSeenAt == nil {
		return HealthOffline
	}
	if offlineAfter > 0 && now.Sub(*m.LastSeenAt) > offlineAfter {
		return HealthOffline
	}
	return HealthOnline
}

func NormalizeCapabilities(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func HasCapability(values []string, required string) bool {
	if required == "" {
		return true
	}
	for _, v := range values {
		if v == required {
			return true
		}
	}
	return false
}

func Compatible(localProtocol, remoteProtocol int, localCaps, remoteCaps []string, required string) error {
	if localProtocol <= 0 || remoteProtocol <= 0 || localProtocol != remoteProtocol {
		return errors.New("incompatible cluster protocol")
	}
	if required != "" && (!HasCapability(localCaps, required) || !HasCapability(remoteCaps, required)) {
		return errors.New("cluster capability unavailable")
	}
	return nil
}
