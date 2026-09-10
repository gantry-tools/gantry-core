// Package auth provides product-neutral account, capability, session, CSRF and
// audit primitives for self-hosted Gantry applications.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Identity struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	Username           string   `json:"username,omitempty"`
	Email              string   `json:"email,omitempty"`
	ProviderSubject    string   `json:"provider_subject,omitempty"`
	PasswordHash       string   `json:"password_hash,omitempty"`
	TOTPEnabled        bool     `json:"totp_enabled,omitempty"`
	RecoveryCodeHashes []string `json:"recovery_code_hashes,omitempty"`
	Enabled            bool     `json:"enabled"`
}

type Account struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"display_name"`
	Enabled     bool       `json:"enabled"`
	Roles       []string   `json:"roles"`
	Identities  []Identity `json:"identities"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AccountsFile struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

type Role struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	BuiltIn      bool     `json:"built_in,omitempty"`
}

type RolesFile struct {
	Version int    `json:"version"`
	Roles   []Role `json:"roles"`
}

type CapabilityInfo struct {
	Key   string `json:"key"`
	Group string `json:"group"`
	Label string `json:"label"`
}

type AccountPolicy struct {
	SchemaVersion   int
	ProductName     string
	KnownCapability func(string) bool
}

func ValidateAccounts(users AccountsFile, roles RolesFile, policy AccountPolicy) error {
	product := strings.TrimSpace(policy.ProductName)
	if product == "" {
		product = "application"
	}
	if users.Version != policy.SchemaVersion || roles.Version != policy.SchemaVersion {
		return errors.New("unsupported users/roles schema version")
	}
	roleIDs := map[string]bool{}
	for _, currentRole := range roles.Roles {
		if currentRole.ID == "" || roleIDs[currentRole.ID] {
			return fmt.Errorf("duplicate/empty role id %q", currentRole.ID)
		}
		roleIDs[currentRole.ID] = true
		if currentRole.ID == "administrator" && (len(currentRole.Capabilities) != 1 || currentRole.Capabilities[0] != "*") {
			return errors.New("administrator role must retain all capabilities")
		}
		for _, capability := range currentRole.Capabilities {
			if capability != "*" && policy.KnownCapability != nil && !policy.KnownCapability(capability) {
				return fmt.Errorf("role %s has unknown capability %q", currentRole.ID, capability)
			}
	}
	if !roleIDs["administrator"] {
		return errors.New("administrator role is required")
	}
	accountIDs := map[string]bool{}
	identityIDs := map[string]bool{}
	usernames := map[string]bool{}
	providerSubjects := map[string]bool{}
	passwordBackedAdmins := 0
	for _, account := range users.Accounts {
		if account.ID == "" || accountIDs[account.ID] {
			return fmt.Errorf("duplicate/empty account id %q", account.ID)
		}
		accountIDs[account.ID] = true
		if strings.TrimSpace(account.DisplayName) == "" {
			return fmt.Errorf("account %s has no display_name", account.ID)
		}
		isAdmin := false
		for _, roleID := range account.Roles {
			if !roleIDs[roleID] {
				return fmt.Errorf("account %s references unknown role %s", account.ID, roleID)
			}
			isAdmin = isAdmin || roleID == "administrator"
		}
		hasEnabledPassword := false
		for _, identity := range account.Identities {
			if identity.ID == "" || identityIDs[identity.ID] {
				return fmt.Errorf("duplicate/empty identity id %q", identity.ID)
			}
			identityIDs[identity.ID] = true
			switch identity.Type {
			case "password":
				username := strings.ToLower(strings.TrimSpace(identity.Username))
				if account.Enabled && identity.Enabled {
					hasEnabledPassword = true
				}
				if username == "" || identity.PasswordHash == "" {
					return fmt.Errorf("password identity %s is incomplete", identity.ID)
				}
				if usernames[username] {
					return fmt.Errorf("duplicate username %q", identity.Username)
				}
				usernames[username] = true
			case "email":
				if strings.TrimSpace(identity.Email) == "" {
					return fmt.Errorf("email identity %s has no email", identity.ID)
				}
			case "google":
				if identity.ProviderSubject == "" {
					return fmt.Errorf("google identity %s has no provider_subject", identity.ID)
				}
				key := identity.Type + "\x00" + identity.ProviderSubject
				if providerSubjects[key] {
					return fmt.Errorf("duplicate %s subject", identity.Type)
				}
				providerSubjects[key] = true
			default:
				return fmt.Errorf("identity %s has unsupported type %q", identity.ID, identity.Type)
			}
		}
		if account.Enabled && isAdmin && hasEnabledPassword {
			passwordBackedAdmins++
		}
	}
	if len(users.Accounts) > 0 && passwordBackedAdmins == 0 {
		return fmt.Errorf("%s must retain at least one enabled administrator with a password login", product)
	}
	return nil
}

func EffectiveCapabilities(account Account, roles []Role) []string {
	if !account.Enabled {
		return nil
	}
	set := map[string]bool{}
	for _, roleID := range account.Roles {
		for _, currentRole := range roles {
			if currentRole.ID != roleID {
				continue
			}
			for _, capability := range currentRole.Capabilities {
				if capability == "*" {
					return []string{"*"}
				}
				set[capability] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for capability := range set {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

func HasCapability(capabilities []string, key string) bool {
	for _, capability := range capabilities {
		if capability == "*" || capability == key {
			return true
		}
	}
	return false
}

func DedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func Token(bytes int) string {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func NewID(prefix string) string { return prefix + "_" + Token(12) }

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const iterations = 310000
	derived := pbkdf2([]byte(password), salt, iterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", iterations, hex.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(derived)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 100000 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func pbkdf2(password, salt []byte, iterations, size int) []byte {
	out := make([]byte, 0, size)
	for block := 1; len(out) < size; block++ {
		counter := []byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)}
		u := hmacSHA256(password, append(append([]byte{}, salt...), counter...))
		value := append([]byte{}, u...)
		for iteration := 1; iteration < iterations; iteration++ {
			u = hmacSHA256(password, u)
			for index := range value {
				value[index] ^= u[index]
			}
		}
		out = append(out, value...)
	}
	return out[:size]
}

func hmacSHA256(key, message []byte) []byte {
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(message)
	return digest.Sum(nil)
}
