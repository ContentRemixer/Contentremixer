package auth

import (
	"errors"
	"sync"
)

// ErrUserNotFound is returned by a UserStore when no row matches the username.
var ErrUserNotFound = errors.New("auth: user not found")

// ErrUserExists is returned when creating a user whose username is already taken.
var ErrUserExists = errors.New("auth: user already exists")

// ErrLastAdmin is returned when a delete or role change would leave the platform
// with no Admin — a lock-out the store refuses structurally so no admin CRUD
// path (API or otherwise) can orphan the install (board decision #8b: there is
// always at least one Admin).
var ErrLastAdmin = errors.New("auth: operation would remove the last admin")

// User is one row of the minimal built-in user table: (username, bcrypt_hash,
// role). The hash is a bcrypt digest — never a plaintext password (§8).
type User struct {
	Username   string
	BcryptHash []byte
	Role       Role
}

// UserStore is the persistence seam for the user table. The in-memory
// implementation ships now; a swap to a backing store later keeps the same
// interface (and the same Session contract downstream).
type UserStore interface {
	// Get returns the user by username, or ErrUserNotFound.
	Get(username string) (User, error)
	// Create inserts a user, or ErrUserExists if the username is taken.
	Create(u User) error
	// List returns every user row (the admin Users & Roles surface, W6). The
	// BcryptHash field is populated; callers that expose users over the wire MUST
	// project it away (never serialise a hash, §8).
	List() ([]User, error)
	// SetRole changes a user's role. Returns ErrUserNotFound if the username is
	// unknown, or ErrLastAdmin if it would demote the only remaining Admin.
	SetRole(username string, role Role) error
	// Delete removes a user. Returns ErrUserNotFound if unknown, or ErrLastAdmin
	// if it would remove the only remaining Admin.
	Delete(username string) error
}

// MemoryStore is a concurrency-safe in-memory UserStore. The seed Admin is the
// only row in Phase 3 (board decision #8b); DevRel rows arrive with personas.
type MemoryStore struct {
	mu    sync.RWMutex
	users map[string]User
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: make(map[string]User)}
}

// Get implements UserStore.
func (s *MemoryStore) Get(username string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok {
		return User{}, ErrUserNotFound
	}
	return u, nil
}

// Create implements UserStore.
func (s *MemoryStore) Create(u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.Username]; ok {
		return ErrUserExists
	}
	s.users[u.Username] = u
	return nil
}

// List implements UserStore.
func (s *MemoryStore) List() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]User, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	return out, nil
}

// SetRole implements UserStore. It refuses to demote the last Admin.
func (s *MemoryStore) SetRole(username string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return ErrUserNotFound
	}
	if u.Role == RoleAdmin && role != RoleAdmin && s.adminCountLocked() == 1 {
		return ErrLastAdmin
	}
	u.Role = role
	s.users[username] = u
	return nil
}

// Delete implements UserStore. It refuses to remove the last Admin.
func (s *MemoryStore) Delete(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[username]
	if !ok {
		return ErrUserNotFound
	}
	if u.Role == RoleAdmin && s.adminCountLocked() == 1 {
		return ErrLastAdmin
	}
	delete(s.users, username)
	return nil
}

// adminCountLocked counts Admin rows. Callers must hold s.mu.
func (s *MemoryStore) adminCountLocked() int {
	n := 0
	for _, u := range s.users {
		if u.Role == RoleAdmin {
			n++
		}
	}
	return n
}
