package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Environment variable names for the AuthN config. Every value here originates
// from a k8s Secret mounted as env (§8) — no credential is ever committed or
// baked into an image. deploy/secrets/booth-auth.example.yaml documents them.
const (
	// EnvSessionKey is the HMAC key that signs session tokens (>= 32 bytes).
	EnvSessionKey = "BOOTH_SESSION_KEY"
	// EnvSessionTTL optionally overrides the session lifetime (Go duration,
	// e.g. "30m"). Defaults to DefaultSessionTTL.
	EnvSessionTTL = "BOOTH_SESSION_TTL"
	// EnvAdminUsername is the seed Admin's username (board decision #8b records
	// the owner's choice here). Defaults to "admin".
	EnvAdminUsername = "BOOTH_ADMIN_USERNAME"
	// EnvAdminBcryptHash is a precomputed bcrypt hash for the seed Admin
	// (preferred: no plaintext ever reaches the cluster).
	EnvAdminBcryptHash = "BOOTH_ADMIN_BCRYPT_HASH"
	// EnvAdminPassword is a first-run plaintext password, hashed at boot. Use
	// only if a precomputed hash is impractical; rotate it to a hash after.
	EnvAdminPassword = "BOOTH_ADMIN_PASSWORD"
	// EnvInsecureCookie, when "true", drops the session cookie's Secure flag for
	// local plaintext development. Never set it in-cluster (served over TLS, §7).
	EnvInsecureCookie = "BOOTH_INSECURE_COOKIE"

	// EnvDevRelUsername optionally seeds a DevRel demo user on boot alongside the
	// Admin (ISI-7455). Booth's user store is in-memory, so every restart/redeploy
	// wipes manually-created accounts; seeding the demo user here makes the board/
	// DevRel demo login survive a redeploy without a manual re-seed. Unset => no
	// DevRel seed (behaviour unchanged).
	EnvDevRelUsername = "BOOTH_SEED_DEVREL_USERNAME"
	// EnvDevRelBcryptHash / EnvDevRelPassword are the DevRel seed credential,
	// mirroring the Admin pair: provide EXACTLY ONE (a precomputed hash is
	// preferred so no plaintext reaches the cluster). Required only when
	// EnvDevRelUsername is set.
	EnvDevRelBcryptHash = "BOOTH_SEED_DEVREL_BCRYPT_HASH"
	EnvDevRelPassword   = "BOOTH_SEED_DEVREL_PASSWORD"
)

// DefaultAdminUsername is the seed Admin username when EnvAdminUsername is unset.
const DefaultAdminUsername = "admin"

// Config is the resolved, validated AuthN configuration.
type Config struct {
	SessionKey    []byte
	SessionTTL    time.Duration
	AdminUsername string
	AdminHash     []byte
	SecureCookie  bool
	// DevRelUsername/DevRelHash seed an optional DevRel demo user (ISI-7455).
	// DevRelHash is nil when no DevRel seed is configured — the Admin seed path
	// is unaffected.
	DevRelUsername string
	DevRelHash     []byte
}

// LoadConfig reads and validates the AuthN config from getenv (pass os.Getenv).
// It fails closed: a missing signing key, or a seed Admin with no credential,
// is an error — never a silent default credential (§8).
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{SecureCookie: !strings.EqualFold(getenv(EnvInsecureCookie), "true")}

	key := getenv(EnvSessionKey)
	if len(key) < MinSessionKeyLen {
		return Config{}, fmt.Errorf("%s must be set to >= %d bytes", EnvSessionKey, MinSessionKeyLen)
	}
	cfg.SessionKey = []byte(key)

	cfg.SessionTTL = DefaultSessionTTL
	if raw := getenv(EnvSessionTTL); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", EnvSessionTTL, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("%s must be positive", EnvSessionTTL)
		}
		cfg.SessionTTL = d
	}

	cfg.AdminUsername = getenv(EnvAdminUsername)
	if cfg.AdminUsername == "" {
		cfg.AdminUsername = DefaultAdminUsername
	}

	hash, err := resolveSeedHash(getenv(EnvAdminBcryptHash), getenv(EnvAdminPassword), EnvAdminBcryptHash, EnvAdminPassword)
	if err != nil {
		return Config{}, fmt.Errorf("seed Admin credential required: %w", err)
	}
	cfg.AdminHash = hash

	cfg.DevRelUsername, cfg.DevRelHash, err = resolveDevRelSeed(getenv)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// resolveSeedHash picks the bcrypt digest for a seed user from its (hash,
// password) env pair: exactly one must be set. hashEnv/passwordEnv name the
// variables for actionable error messages.
func resolveSeedHash(bcryptHash, password, hashEnv, passwordEnv string) ([]byte, error) {
	switch {
	case bcryptHash != "" && password != "":
		return nil, fmt.Errorf("set only one of %s or %s, not both", hashEnv, passwordEnv)
	case bcryptHash != "":
		return []byte(bcryptHash), nil
	case password != "":
		return HashPassword(password)
	default:
		return nil, fmt.Errorf("set %s or %s", hashEnv, passwordEnv)
	}
}

// resolveDevRelSeed reads the optional DevRel demo-user seed (ISI-7455). It
// returns ("", nil, nil) when none of the three env vars is set — the common
// case where no DevRel seed is wired. If any is set, a username AND exactly one
// credential are required (fail closed on partial config).
func resolveDevRelSeed(getenv func(string) string) (string, []byte, error) {
	username := getenv(EnvDevRelUsername)
	bcryptHash := getenv(EnvDevRelBcryptHash)
	password := getenv(EnvDevRelPassword)
	if username == "" && bcryptHash == "" && password == "" {
		return "", nil, nil
	}
	if username == "" {
		return "", nil, fmt.Errorf("%s required to seed a DevRel user", EnvDevRelUsername)
	}
	hash, err := resolveSeedHash(bcryptHash, password, EnvDevRelBcryptHash, EnvDevRelPassword)
	if err != nil {
		return "", nil, fmt.Errorf("DevRel seed credential: %w", err)
	}
	return username, hash, nil
}

// Provider bundles the wired AuthN components. The Booth API (P3-1) mounts
// Handlers.Login/Logout as public routes and wraps its CR routes with
// Middleware, then reads the Session via SessionFromContext.
type Provider struct {
	Store      UserStore
	Auth       Authenticator
	Codec      *TokenCodec
	Middleware *Middleware
	Handlers   *Handlers
}

// Bootstrap wires the AuthN provider from cfg and seeds the sole initial Admin
// (board decision #8b). It is safe to call once at Booth startup.
func Bootstrap(cfg Config) (*Provider, error) {
	codec, err := NewTokenCodec(cfg.SessionKey, cfg.SessionTTL)
	if err != nil {
		return nil, err
	}
	store := NewMemoryStore()
	if err := store.Create(User{
		Username:   cfg.AdminUsername,
		BcryptHash: cfg.AdminHash,
		Role:       RoleAdmin,
	}); err != nil {
		return nil, fmt.Errorf("seed admin: %w", err)
	}
	// Optional DevRel demo-user seed (ISI-7455): survives Booth redeploys so the
	// board/DevRel demo login no longer needs a manual re-seed. Skip when it
	// collides with the Admin username (the Admin seed wins) — idempotent.
	if cfg.DevRelUsername != "" && cfg.DevRelUsername != cfg.AdminUsername {
		if err := store.Create(User{
			Username:   cfg.DevRelUsername,
			BcryptHash: cfg.DevRelHash,
			Role:       RoleDevRel,
		}); err != nil && !errors.Is(err, ErrUserExists) {
			return nil, fmt.Errorf("seed devrel: %w", err)
		}
	}
	auth := NewBcryptAuthenticator(store)
	return &Provider{
		Store:      store,
		Auth:       auth,
		Codec:      codec,
		Middleware: NewMiddleware(codec),
		Handlers:   NewHandlers(auth, codec, Options{SecureCookie: cfg.SecureCookie}),
	}, nil
}

// BootstrapFromEnv is the one-call startup path: LoadConfig(getenv) then
// Bootstrap. The Booth main passes os.Getenv.
func BootstrapFromEnv(getenv func(string) string) (*Provider, error) {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return nil, err
	}
	return Bootstrap(cfg)
}
