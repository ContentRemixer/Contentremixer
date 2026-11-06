package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is the work factor for new hashes. 12 is above bcrypt's default
// (10) and a sensible ceiling for a homelab Booth (board: bcrypt is the chosen
// ceiling). Raise here if the deployment can afford it.
const bcryptCost = 12

// HashPassword returns a bcrypt digest of password. Used to hash a first-run
// seed password (§8); operators may instead supply a precomputed hash.
func HashPassword(password string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
}

// verifyPassword reports whether password matches hash. It wraps bcrypt's
// constant-time comparison; a mismatch (or malformed hash) returns false.
func verifyPassword(hash []byte, password string) bool {
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}
