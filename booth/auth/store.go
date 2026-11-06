package auth

import (
	"errors"
	"sync"
)

// ErrUserNotFound is returned by a UserStore when no row matches the username.
var ErrUserNotFound = errors.New("auth: user not found")

// ErrUserExists is returned when creating a user whose username is already taken.
var ErrUserExists = errors.New("auth: user already exists")

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
