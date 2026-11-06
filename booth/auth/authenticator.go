package auth

import "errors"

// ErrInvalidCredentials is returned for any failed login. It is deliberately
// the same for unknown-user and wrong-password so the API cannot be used to
// enumerate usernames.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// dummyHash is a valid bcrypt digest of a random string. On an unknown
// username we still run a bcrypt comparison against it so login latency does
// not reveal whether a username exists.
var dummyHash, _ = HashPassword("not-a-real-password-timing-equalizer")

// Authenticator turns a credential into a Session. It is the swap point for the
// "ponytail" upgrade path: replace the bcrypt implementation with an OIDC one
// behind this same interface and nothing downstream (Session, Middleware, the
// §6.3 predicate) changes.
type Authenticator interface {
	// Authenticate returns the Session for valid credentials, or
	// ErrInvalidCredentials. The returned subject == username.
	Authenticate(username, password string) (Session, error)
}

// BcryptAuthenticator authenticates against the built-in bcrypt user table
// (board decision #8a).
type BcryptAuthenticator struct {
	store UserStore
}

// NewBcryptAuthenticator returns an Authenticator backed by store.
func NewBcryptAuthenticator(store UserStore) *BcryptAuthenticator {
	return &BcryptAuthenticator{store: store}
}

// Authenticate implements Authenticator.
func (a *BcryptAuthenticator) Authenticate(username, password string) (Session, error) {
	u, err := a.store.Get(username)
	if err != nil {
		// Equalize timing against the wrong-password path; discard the result.
		_ = verifyPassword(dummyHash, password)
		return Session{}, ErrInvalidCredentials
	}
	if !verifyPassword(u.BcryptHash, password) {
		return Session{}, ErrInvalidCredentials
	}
	return Session{Subject: u.Username, Role: u.Role}, nil
}
