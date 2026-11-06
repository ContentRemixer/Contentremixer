package session

import (
	"net/http"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
)

// User is one row of the Phase-1 built-in user table (board decision #8a).
// The password is stored only as a bcrypt hash; the plaintext never lives in
// the table. Subject is the stable identity fed to the §6.3 predicate and the
// audit log — for the bcrypt backend it is an opaque, admin-assigned id so that
// swapping in OIDC later (which supplies its own `sub`) does not require
// re-owning every resource.
type User struct {
	Subject      string
	Role         string
	PasswordHash []byte
}

// BcryptAuthenticator authenticates HTTP Basic credentials against an in-memory
// bcrypt user table. It implements session.Authenticator.
//
// HTTP Basic is the Phase-1 transport: minimal, stateless, and trivially
// replaced by the OIDC session flow behind the same interface. Credentials MUST
// only ever travel over TLS (terminated at the gateway / ingress).
type BcryptAuthenticator struct {
	mu    sync.RWMutex
	users map[string]User // keyed by username
	// dummyHash is compared against when a username is unknown, so the response
	// time does not reveal whether a username exists (timing-oblivious).
	dummyHash []byte
}

// NewBcryptAuthenticator builds an authenticator from a username→User map. The
// map is copied; later mutation of the caller's map does not affect it.
func NewBcryptAuthenticator(users map[string]User) *BcryptAuthenticator {
	cp := make(map[string]User, len(users))
	for k, v := range users {
		cp[k] = v
	}
	// A fixed, valid bcrypt hash of a random throwaway string. Comparing against
	// it on the unknown-user path keeps CPU cost ~constant across hit/miss.
	dummy, _ := bcrypt.GenerateFromPassword([]byte("booth-timing-equalizer"), bcrypt.DefaultCost)
	return &BcryptAuthenticator{users: cp, dummyHash: dummy}
}

// Authenticate validates HTTP Basic credentials. On success it returns a
// Session with the user's Subject and Role; otherwise ErrUnauthenticated.
func (a *BcryptAuthenticator) Authenticate(r *http.Request) (Session, error) {
	username, password, ok := r.BasicAuth()
	if !ok {
		return Session{}, ErrUnauthenticated
	}

	a.mu.RLock()
	u, known := a.users[username]
	a.mu.RUnlock()

	hash := u.PasswordHash
	if !known {
		hash = a.dummyHash
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil || !known {
		return Session{}, ErrUnauthenticated
	}

	return Session{Subject: u.Subject, Role: u.Role}, nil
}

// SetUser inserts or replaces a user row. Intended for admin user management
// (design §6.1: Admin "manage users/roles"); the HTTP surface for it is out of
// scope for ISI-7149, which owns the authz core, not the user-admin endpoints.
func (a *BcryptAuthenticator) SetUser(username string, u User) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users[username] = u
}

// HashPassword is a convenience wrapper used by the bootstrap path and tests to
// produce bcrypt hashes for the user table.
func HashPassword(plaintext string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
}

// compile-time assertions.
var (
	_ Authenticator = (*BcryptAuthenticator)(nil)
	_               = authz.RoleAdmin
)
