package auth

import (
	"crypto/hmac"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// AccountPersistence keeps one application's account and role documents.
// Implementations are deliberately application-local; Model never shares an
// account namespace, session, or credential between Gantry products.
type AccountPersistence interface {
	LoadAccounts() (AccountsFile, error)
	LoadRoles() (RolesFile, error)
	SaveAccounts(AccountsFile) error
	SaveRoles(RolesFile) error
}

func (model *Model) Identity(identityID string) (Account, Identity, bool) {
	model.mu.RLock()
	defer model.mu.RUnlock()
	for _, account := range model.users.Accounts {
		for _, identity := range account.Identities {
			if identity.ID == identityID {
				return cloneAccount(account), cloneIdentity(identity), true
			}
		}
	}
	return Account{}, Identity{}, false
}

func (model *Model) ProviderIdentity(identityType, subject string) (Account, Identity, bool) {
	model.mu.RLock()
	defer model.mu.RUnlock()
	for _, account := range model.users.Accounts {
		if !account.Enabled {
			continue
		}
		for _, identity := range account.Identities {
			if identity.Enabled && identity.Type == identityType && identity.ProviderSubject == subject {
				return cloneAccount(account), cloneIdentity(identity), true
			}
		}
	}
	return Account{}, Identity{}, false
}

// Model is the canonical account/identity/role implementation used by Gantry
// applications. Products supply persistence and their own capability policy.
type Model struct {
	mu     sync.RWMutex
	store  AccountPersistence
	policy AccountPolicy
	users  AccountsFile
	roles  RolesFile
}

func NewModel(store AccountPersistence, policy AccountPolicy) (*Model, error) {
	if store == nil {
		return nil, errors.New("account persistence is required")
	}
	users, err := store.LoadAccounts()
	if err != nil {
		return nil, err
	}
	roles, err := store.LoadRoles()
	if err != nil {
		return nil, err
	}
	if err := ValidateAccounts(users, roles, policy); err != nil {
		return nil, err
	}
	return &Model{store: store, policy: policy, users: cloneAccounts(users), roles: cloneRoles(roles)}, nil
}

// Reload replaces the in-memory snapshot after an application-level
// transaction changes the same persistence (for example first-run setup that
// also commits product configuration).
func (model *Model) Reload() error {
	users, err := model.store.LoadAccounts()
	if err != nil {
		return err
	}
	roles, err := model.store.LoadRoles()
	if err != nil {
		return err
	}
	if err := ValidateAccounts(users, roles, model.policy); err != nil {
		return err
	}
	model.mu.Lock()
	model.users = cloneAccounts(users)
	model.roles = cloneRoles(roles)
	model.mu.Unlock()
	return nil
}

func (model *Model) Empty() bool {
	model.mu.RLock()
	defer model.mu.RUnlock()
	return len(model.users.Accounts) == 0
}

func (model *Model) Accounts() []Account {
	model.mu.RLock()
	defer model.mu.RUnlock()
	return cloneAccounts(model.users).Accounts
}

func (model *Model) Roles() []Role {
	model.mu.RLock()
	defer model.mu.RUnlock()
	return cloneRoles(model.roles).Roles
}

func (model *Model) Account(id string) (Account, bool) {
	model.mu.RLock()
	defer model.mu.RUnlock()
	for _, account := range model.users.Accounts {
		if account.ID == id {
			return cloneAccount(account), true
		}
	}
	return Account{}, false
}

func (model *Model) AuthenticatePassword(username, password string) (Account, Identity, bool) {
	model.mu.RLock()
	defer model.mu.RUnlock()
	wanted := strings.TrimSpace(username)
	for _, account := range model.users.Accounts {
		if !account.Enabled {
			continue
		}
		for _, identity := range account.Identities {
			if identity.Enabled && identity.Type == "password" && strings.EqualFold(identity.Username, wanted) && VerifyPassword(identity.PasswordHash, password) {
				return cloneAccount(account), cloneIdentity(identity), true
			}
		}
	}
	return Account{}, Identity{}, false
}

func (model *Model) SessionPrincipalActive(accountID, identityID string) bool {
	model.mu.RLock()
	defer model.mu.RUnlock()
	for _, account := range model.users.Accounts {
		if account.ID != accountID || !account.Enabled {
			continue
		}
		for _, identity := range account.Identities {
			if identity.ID == identityID {
				return identity.Enabled
			}
		}
	}
	return false
}

func (model *Model) Capabilities(accountID string) []string {
	model.mu.RLock()
	defer model.mu.RUnlock()
	for _, account := range model.users.Accounts {
		if account.ID == accountID {
			return EffectiveCapabilities(account, model.roles.Roles)
		}
	}
	return nil
}

func (model *Model) CreateInitialAdministrator(displayName, username, password string) (Account, error) {
	model.mu.RLock()
	notEmpty := len(model.users.Accounts) != 0
	model.mu.RUnlock()
	if notEmpty {
		return Account{}, errors.New("setup is already complete")
	}
	return model.CreateAccount(displayName, username, password, []string{"administrator"})
}

func (model *Model) CreateAccount(displayName, username, password string, roles []string) (Account, error) {
	displayName, username = strings.TrimSpace(displayName), strings.TrimSpace(username)
	if displayName == "" || username == "" || len([]rune(password)) < 7 {
		return Account{}, errors.New("display name, username and a password of at least 7 characters are required")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	account := Account{
		ID: NewID("acct"), DisplayName: displayName, Enabled: true,
		Roles: DedupeStrings(roles), CreatedAt: time.Now().UTC(),
		Identities: []Identity{{ID: NewID("id"), Type: "password", Username: username, PasswordHash: hash, Enabled: true}},
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	next.Accounts = append(next.Accounts, account)
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return Account{}, err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return Account{}, err
	}
	model.users = next
	return cloneAccount(account), nil
}

func (model *Model) UpdateAccount(id, displayName string, enabled bool, roles []string) error {
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	found := false
	for index := range next.Accounts {
		if next.Accounts[index].ID == id {
			next.Accounts[index].DisplayName = strings.TrimSpace(displayName)
			next.Accounts[index].Enabled = enabled
			next.Accounts[index].Roles = DedupeStrings(roles)
			found = true
			break
		}
	}
	if !found {
		return errors.New("account not found")
	}
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return err
	}
	model.users = next
	return nil
}

func (model *Model) SetPassword(accountID, identityID, password string) error {
	if len([]rune(password)) < 7 {
		return errors.New("password must be at least 7 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	found := false
	for accountIndex := range next.Accounts {
		if next.Accounts[accountIndex].ID != accountID {
			continue
		}
		for identityIndex := range next.Accounts[accountIndex].Identities {
			identity := &next.Accounts[accountIndex].Identities[identityIndex]
			if identity.ID == identityID && identity.Type == "password" {
				identity.PasswordHash = hash
				found = true
			}
		}
	}
	if !found {
		return errors.New("password identity not found")
	}
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return err
	}
	model.users = next
	return nil
}

func (model *Model) AddIdentity(accountID string, identity Identity) (Identity, error) {
	if identity.ID == "" {
		identity.ID = NewID("id")
	}
	identity.Username = strings.TrimSpace(identity.Username)
	identity.Email = strings.TrimSpace(identity.Email)
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	found := false
	for index := range next.Accounts {
		if next.Accounts[index].ID == accountID {
			next.Accounts[index].Identities = append(next.Accounts[index].Identities, cloneIdentity(identity))
			found = true
			break
		}
	}
	if !found {
		return Identity{}, errors.New("account not found")
	}
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return Identity{}, err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return Identity{}, err
	}
	model.users = next
	return cloneIdentity(identity), nil
}

func (model *Model) AddPasswordIdentity(accountID, username, password string) (Identity, error) {
	if len([]rune(password)) < 7 {
		return Identity{}, errors.New("password must be at least 7 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Identity{}, err
	}
	return model.AddIdentity(accountID, Identity{Type: "password", Username: username, PasswordHash: hash, Enabled: true})
}

func (model *Model) SetIdentityTOTP(accountID, identityID string, enabled bool, recoveryHashes []string) error {
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	found := false
	for accountIndex := range next.Accounts {
		if next.Accounts[accountIndex].ID != accountID {
			continue
		}
		for identityIndex := range next.Accounts[accountIndex].Identities {
			identity := &next.Accounts[accountIndex].Identities[identityIndex]
			if identity.ID == identityID {
				if identity.Type != "password" {
					return errors.New("TOTP is only supported for password identities")
				}
				identity.TOTPEnabled = enabled
				identity.RecoveryCodeHashes = append([]string(nil), recoveryHashes...)
				found = true
			}
		}
	}
	if !found {
		return errors.New("identity not found")
	}
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return err
	}
	model.users = next
	return nil
}

func (model *Model) SetIdentityEnabled(accountID, identityID string, enabled bool) error {
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	found := false
	for accountIndex := range next.Accounts {
		if next.Accounts[accountIndex].ID != accountID {
			continue
		}
		for identityIndex := range next.Accounts[accountIndex].Identities {
			identity := &next.Accounts[accountIndex].Identities[identityIndex]
			if identity.ID == identityID {
				identity.Enabled = enabled
				found = true
			}
		}
	}
	if !found {
		return errors.New("identity not found")
	}
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return err
	}
	model.users = next
	return nil
}

func (model *Model) ConsumeRecoveryCode(accountID, identityID, hash string) bool {
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	for accountIndex := range next.Accounts {
		if next.Accounts[accountIndex].ID != accountID {
			continue
		}
		for identityIndex := range next.Accounts[accountIndex].Identities {
			identity := &next.Accounts[accountIndex].Identities[identityIndex]
			if identity.ID != identityID {
				continue
			}
			for index, saved := range identity.RecoveryCodeHashes {
				if hmac.Equal([]byte(saved), []byte(hash)) {
					identity.RecoveryCodeHashes = append(identity.RecoveryCodeHashes[:index:index], identity.RecoveryCodeHashes[index+1:]...)
					if model.store.SaveAccounts(next) != nil {
						return false
					}
					model.users = next
					return true
				}
			}
		}
	}
	return false
}

func (model *Model) RemoveIdentity(accountID, identityID string) (Identity, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	for accountIndex := range next.Accounts {
		account := &next.Accounts[accountIndex]
		if account.ID != accountID {
			continue
		}
		identityIndex, loginCapable := -1, 0
		var removed Identity
		for index, identity := range account.Identities {
			if identity.Type == "password" || identity.Type == "google" {
				loginCapable++
			}
			if identity.ID == identityID {
				identityIndex, removed = index, identity
			}
		}
		if identityIndex < 0 {
			return Identity{}, errors.New("identity not found")
		}
		if (removed.Type == "password" || removed.Type == "google") && loginCapable <= 1 {
			return Identity{}, errors.New("account must retain at least one login identity")
		}
		account.Identities = append(account.Identities[:identityIndex:identityIndex], account.Identities[identityIndex+1:]...)
		if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
			return Identity{}, err
		}
		if err := model.store.SaveAccounts(next); err != nil {
			return Identity{}, err
		}
		model.users = next
		return cloneIdentity(removed), nil
	}
	return Identity{}, errors.New("account not found")
}

func (model *Model) DeleteAccount(accountID string) ([]Identity, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneAccounts(model.users)
	index := -1
	var identities []Identity
	for candidate, account := range next.Accounts {
		if account.ID == accountID {
			index = candidate
			identities = append([]Identity(nil), account.Identities...)
			break
		}
	}
	if index < 0 {
		return nil, errors.New("account not found")
	}
	next.Accounts = append(next.Accounts[:index:index], next.Accounts[index+1:]...)
	if len(next.Accounts) == 0 {
		return nil, errors.New("application must retain at least one account")
	}
	if err := ValidateAccounts(next, model.roles, model.policy); err != nil {
		return nil, err
	}
	if err := model.store.SaveAccounts(next); err != nil {
		return nil, err
	}
	model.users = next
	return identities, nil
}

func (model *Model) SetRole(id, name string, capabilities []string, builtIn bool) error {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" || name == "" {
		return errors.New("role id and name are required")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	next := cloneRoles(model.roles)
	found := false
	for index := range next.Roles {
		if next.Roles[index].ID == id {
			next.Roles[index].Name = name
			next.Roles[index].Capabilities = DedupeStrings(capabilities)
			found = true
			break
		}
	}
	if !found {
		next.Roles = append(next.Roles, Role{ID: id, Name: name, Capabilities: DedupeStrings(capabilities), BuiltIn: builtIn})
	}
	sort.Slice(next.Roles, func(i, j int) bool { return next.Roles[i].ID < next.Roles[j].ID })
	if err := ValidateAccounts(model.users, next, model.policy); err != nil {
		return err
	}
	if err := model.store.SaveRoles(next); err != nil {
		return err
	}
	model.roles = next
	return nil
}

func cloneAccounts(value AccountsFile) AccountsFile {
	out := AccountsFile{Version: value.Version, Accounts: make([]Account, len(value.Accounts))}
	for index, account := range value.Accounts {
		out.Accounts[index] = cloneAccount(account)
	}
	return out
}

func cloneAccount(value Account) Account {
	value.Roles = append([]string(nil), value.Roles...)
	value.Identities = append([]Identity(nil), value.Identities...)
	for index := range value.Identities {
		value.Identities[index] = cloneIdentity(value.Identities[index])
	}
	return value
}

func cloneIdentity(value Identity) Identity {
	value.RecoveryCodeHashes = append([]string(nil), value.RecoveryCodeHashes...)
	return value
}

func cloneRoles(value RolesFile) RolesFile {
	out := RolesFile{Version: value.Version, Roles: make([]Role, len(value.Roles))}
	for index, role := range value.Roles {
		role.Capabilities = append([]string(nil), role.Capabilities...)
		out.Roles[index] = role
	}
	return out
}
