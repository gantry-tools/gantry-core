package auth

import (
	"sync"
	"testing"
)

type memoryAccounts struct {
	users AccountsFile
	roles RolesFile
}

func (store *memoryAccounts) LoadAccounts() (AccountsFile, error) { return store.users, nil }
func (store *memoryAccounts) LoadRoles() (RolesFile, error)       { return store.roles, nil }
func (store *memoryAccounts) SaveAccounts(value AccountsFile) error {
	store.users = value
	return nil
}
func (store *memoryAccounts) SaveRoles(value RolesFile) error {
	store.roles = value
	return nil
}

func TestModelProvidesCanonicalIsolatedAccountLifecycle(t *testing.T) {
	store := &memoryAccounts{
		users: AccountsFile{Version: 1, Accounts: []Account{}},
		roles: RolesFile{Version: 1, Roles: []Role{
			{ID: "administrator", Name: "Administrator", Capabilities: []string{"*"}, BuiltIn: true},
			{ID: "reader", Name: "Reader", Capabilities: []string{"items.read"}, BuiltIn: true},
		}},
	}
	model, err := NewModel(store, AccountPolicy{SchemaVersion: 1, ProductName: "Test", KnownCapability: func(key string) bool { return key == "items.read" }})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := model.CreateInitialAdministrator("Admin", "admin", "admin@example.com", "password-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, identity, ok := model.AuthenticatePassword("ADMIN", "password-one"); !ok || identity.Type != "password" {
		t.Fatal("canonical password identity did not authenticate")
	}
	if _, _, ok := model.AuthenticatePassword("admin@example.com", "password-one"); !ok {
		t.Fatal("password identity did not authenticate by email")
	}
	if !HasCapability(model.Capabilities(admin.ID), "anything") {
		t.Fatal("administrator wildcard capability missing")
	}
	identityID := admin.Identities[0].ID
	if err := model.SetPassword(admin.ID, identityID, "password-two"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := model.AuthenticatePassword("admin", "password-one"); ok {
		t.Fatal("old password remained valid")
	}
	if _, _, ok := model.AuthenticatePassword("admin", "password-two"); !ok {
		t.Fatal("new password did not authenticate")
	}
	if err := model.UpdateAccount(admin.ID, "Admin", false, []string{"administrator"}); err == nil {
		t.Fatal("disabled last password-backed administrator was accepted")
	}
}

func TestModelsDoNotShareApplicationAccounts(t *testing.T) {
	roles := RolesFile{Version: 1, Roles: []Role{{ID: "administrator", Name: "Administrator", Capabilities: []string{"*"}, BuiltIn: true}}}
	firstStore := &memoryAccounts{users: AccountsFile{Version: 1, Accounts: []Account{}}, roles: roles}
	secondStore := &memoryAccounts{users: AccountsFile{Version: 1, Accounts: []Account{}}, roles: roles}
	policy := AccountPolicy{SchemaVersion: 1, ProductName: "Test"}
	first, err := NewModel(firstStore, policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewModel(secondStore, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.CreateInitialAdministrator("First", "first", "first@example.com", "password-one"); err != nil {
		t.Fatal(err)
	}
	if !second.Empty() {
		t.Fatal("independent application model observed another application's account")
	}
}

func TestCreateInitialAdministratorIsAtomicUnderConcurrency(t *testing.T) {
	store := &memoryAccounts{
		users: AccountsFile{Version: 1, Accounts: []Account{}},
		roles: RolesFile{Version: 1, Roles: []Role{{ID: "administrator", Name: "Administrator", Capabilities: []string{"*"}, BuiltIn: true}}},
	}
	model, err := NewModel(store, AccountPolicy{SchemaVersion: 1, ProductName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	created := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := model.CreateInitialAdministrator("Admin", "admin", "admin@example.com", "password-one")
			created <- err
		}(i)
	}
	wg.Wait()
	close(created)
	successes := 0
	for err := range created {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one successful setup, got %d", successes)
	}
	if len(model.Accounts()) != 1 {
		t.Fatalf("expected exactly one account, got %d", len(model.Accounts()))
	}
}

func TestPasswordVerifierAcceptsOnlyCanonicalWriterParameters(t *testing.T) {
	hash, err := HashPassword("canonical-password")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "canonical-password") {
		t.Fatal("canonical password hash was rejected")
	}
	for _, malformed := range []string{
		"pbkdf2-sha256$100000$00112233445566778899aabbccddeeff$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$1000000000$00112233445566778899aabbccddeeff$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$310000$0011$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"pbkdf2-sha256$310000$00112233445566778899aabbccddeeff$AA",
	} {
		if VerifyPassword(malformed, "canonical-password") {
			t.Fatalf("non-canonical hash accepted: %s", malformed)
		}
	}
}

func TestDuplicateEmailRejectedAcrossAccounts(t *testing.T) {
	store := &memoryAccounts{
		users: AccountsFile{Version: 1, Accounts: []Account{}},
		roles: RolesFile{Version: 1, Roles: []Role{
			{ID: "administrator", Name: "Administrator", Capabilities: []string{"*"}, BuiltIn: true},
			{ID: "user", Name: "User", Capabilities: []string{"items.read"}, BuiltIn: true},
		}},
	}
	model, err := NewModel(store, AccountPolicy{SchemaVersion: 1, ProductName: "Test", KnownCapability: func(key string) bool { return key == "items.read" }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.CreateInitialAdministrator("Admin", "admin", "admin@example.com", "password-one"); err != nil {
		t.Fatal(err)
	}
	// Distinct username but the same (case-insensitive) email must be rejected.
	if _, err := model.CreateAccount("Other", "other", "ADMIN@example.com", "password-two", []string{"user"}); err == nil {
		t.Fatal("duplicate normalized email accepted")
	}
	// Distinct username and distinct email must be accepted.
	if _, err := model.CreateAccount("Other", "other", "other@example.com", "password-two", []string{"user"}); err != nil {
		t.Fatalf("distinct account rejected: %v", err)
	}
	// Duplicate username (case-insensitive) must be rejected.
	if _, err := model.CreateAccount("Third", "ADMIN", "third@example.com", "password-three", []string{"user"}); err == nil {
		t.Fatal("duplicate normalized username accepted")
	}
}
