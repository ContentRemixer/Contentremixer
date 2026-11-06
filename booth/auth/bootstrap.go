package auth

import (
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

	hash, err := resolveAdminHash(getenv(EnvAdminBcryptHash), getenv(EnvAdminPassword))
	if err != nil {
		return Config{}, err
	}
	cfg.AdminHash = hash
	return cfg, nil
}

func resolveAdminHash(bcryptHash, password string) ([]byte, error) {
	switch {
	case bcryptHash != "" && password != "":
		return nil, fmt.Errorf("set only one of %s or %s, not both", EnvAdminBcryptHash, EnvAdminPassword)
	case bcryptHash != "":
		return []byte(bcryptHash), nil
	case password != "":
		return HashPassword(password)
	default:
		return nil, fmt.Errorf("seed Admin credential required: set %s or %s", EnvAdminBcryptHash, EnvAdminPassword)
	}
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
