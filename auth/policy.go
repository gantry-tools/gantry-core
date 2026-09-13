package auth

import (
	"errors"
	"strings"
)

// CredentialPolicy describes whether a product accepts user-supplied
// provider credentials or requires installation-managed credentials.
type CredentialPolicy string

const (
	CredentialManagedOnly     CredentialPolicy = "managed-only"
	CredentialPersonalAllowed CredentialPolicy = "personal-allowed"
	CredentialDisabled        CredentialPolicy = "disabled"
)

type ProviderGrant struct {
	Provider string           `json:"provider"`
	Policy   CredentialPolicy `json:"policy"`
}

func (policy CredentialPolicy) Valid() bool {
	return policy == CredentialManagedOnly || policy == CredentialPersonalAllowed || policy == CredentialDisabled
}

func ValidateProviderGrants(grants []ProviderGrant) error {
	seen := map[string]bool{}
	for _, grant := range grants {
		provider := strings.TrimSpace(grant.Provider)
		if provider == "" || seen[provider] {
			return errors.New("provider grants require unique non-empty provider ids")
		}
		if !grant.Policy.Valid() {
			return errors.New("provider grant has an invalid credential policy")
		}
		seen[provider] = true
	}
	return nil
}
