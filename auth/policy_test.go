package auth

import "testing"

func TestValidateProviderGrants(t *testing.T) {
	if err := ValidateProviderGrants([]ProviderGrant{{Provider: "openai", Policy: CredentialManagedOnly}}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]ProviderGrant{
		{{Provider: "", Policy: CredentialManagedOnly}},
		{{Provider: "openai", Policy: "legacy"}},
		{{Provider: "openai", Policy: CredentialManagedOnly}, {Provider: "openai", Policy: CredentialPersonalAllowed}},
	} {
		if ValidateProviderGrants(invalid) == nil {
			t.Fatalf("accepted invalid grants: %#v", invalid)
		}
	}
}

func TestCanonicalPasswordFormatRejectsCortexLegacySalt(t *testing.T) {
	// Cortex previously encoded the PBKDF2 salt as base64. The shared format
	// accepts only a hexadecimal salt; there is deliberately no fallback.
	legacy := "pbkdf2-sha256$310000$MDEyMzQ1Njc4OWFiY2RlZg$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if VerifyPassword(legacy, "password") {
		t.Fatal("legacy Cortex password hash was accepted")
	}
}
