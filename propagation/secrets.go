package propagation

import (
	"errors"
	"fmt"
)

type SecretStatus struct {
	Name     string `json:"name"`
	Present  bool   `json:"present"`
	Required bool   `json:"required"`
}
type SecretProvider interface {
	Has(name string) (bool, error)
}

func CheckSecrets(refs []SecretRef, provider SecretProvider) ([]SecretStatus, error) {
	if provider == nil && len(refs) > 0 {
		return nil, errors.New("secret provider is required")
	}
	out := make([]SecretStatus, 0, len(refs))
	for _, ref := range refs {
		ok, err := provider.Has(ref.Name)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", ref.Name, err)
		}
		out = append(out, SecretStatus{Name: ref.Name, Present: ok, Required: ref.Required})
		if ref.Required && !ok {
			return out, fmt.Errorf("required secret %s is unavailable", ref.Name)
		}
	}
	return out, nil
}

type MapSecrets map[string]bool

func (m MapSecrets) Has(name string) (bool, error) { return m[name], nil }
