package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Token errors. Callers should treat every one as "unauthenticated" (401) and
// never leak which case occurred.
var (
	ErrInvalidToken = errors.New("auth: invalid session token")
	ErrExpiredToken = errors.New("auth: session token expired")
)

// MinSessionKeyLen is the minimum HMAC key length accepted for signing session
// tokens. 32 bytes matches the SHA-256 block size; shorter keys are rejected at
// construction so a weak BOOTH_SESSION_KEY fails closed at boot, not silently.
const MinSessionKeyLen = 32

// DefaultSessionTTL is the lifetime of an issued session token when none is
// configured. Sessions are deliberately short-lived (§6.4).
const DefaultSessionTTL = time.Hour

// claims is the signed token payload. subject is the user's stable owner key
// (booth:local:<username> until OIDC, see auth.User.Subject) (§6.2).
type claims struct {
	Subject string `json:"sub"`
	Role    Role   `json:"role"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

// TokenCodec mints and verifies short-lived HMAC-SHA256 signed session tokens.
// The format is base64url(payload) "." base64url(signature); no external JWT
// dependency (keeps the auth surface minimal — board: bcrypt is the ceiling).
type TokenCodec struct {
	key []byte
	ttl time.Duration
	now func() time.Time // injectable for tests
}

// NewTokenCodec returns a codec signing with key for ttl. It rejects a key
// shorter than MinSessionKeyLen so a weak signing secret cannot ship. A
// non-positive ttl falls back to DefaultSessionTTL.
func NewTokenCodec(key []byte, ttl time.Duration) (*TokenCodec, error) {
	if len(key) < MinSessionKeyLen {
		return nil, errors.New("auth: session signing key too short (need >= 32 bytes)")
	}
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	k := make([]byte, len(key))
	copy(k, key)
	return &TokenCodec{key: k, ttl: ttl, now: time.Now}, nil
}

// TTL returns the lifetime applied to tokens this codec mints. The login
// handler uses it to align the session cookie's Max-Age with the token expiry.
func (c *TokenCodec) TTL() time.Duration { return c.ttl }

func (c *TokenCodec) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

var b64 = base64.RawURLEncoding

// Mint returns a signed token encoding s with a fresh issued/expiry window.
func (c *TokenCodec) Mint(s Session) (string, error) {
	now := c.now()
	payload, err := json.Marshal(claims{
		Subject: s.Subject,
		Role:    s.Role,
		Issued:  now.Unix(),
		Expires: now.Add(c.ttl).Unix(),
	})
	if err != nil {
		return "", err
	}
	sig := c.sign(payload)
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(sig), nil
}

// Verify checks the signature and expiry and returns the embedded Session.
// It compares signatures in constant time and returns ErrInvalidToken or
// ErrExpiredToken on any failure.
func (c *TokenCodec) Verify(token string) (Session, error) {
	payloadB64, sigB64, ok := strings.Cut(token, ".")
	if !ok {
		return Session{}, ErrInvalidToken
	}
	payload, err := b64.DecodeString(payloadB64)
	if err != nil {
		return Session{}, ErrInvalidToken
	}
	sig, err := b64.DecodeString(sigB64)
	if err != nil {
		return Session{}, ErrInvalidToken
	}
	if !hmac.Equal(sig, c.sign(payload)) {
		return Session{}, ErrInvalidToken
	}
	var cl claims
	if err := json.Unmarshal(payload, &cl); err != nil {
		return Session{}, ErrInvalidToken
	}
	if !cl.Role.Valid() || cl.Subject == "" {
		return Session{}, ErrInvalidToken
	}
	if c.now().Unix() >= cl.Expires {
		return Session{}, ErrExpiredToken
	}
	return Session{Subject: cl.Subject, Role: cl.Role}, nil
}
