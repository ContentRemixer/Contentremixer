package auth

import (
	"testing"
	"time"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(envMap(map[string]string{
		EnvSessionKey:    testKey,
		EnvAdminPassword: "pw",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AdminUsername != DefaultAdminUsername {
		t.Fatalf("username = %q, want default", cfg.AdminUsername)
	}
	if cfg.SessionTTL != DefaultSessionTTL {
		t.Fatalf("ttl = %v, want default", cfg.SessionTTL)
	}
	if !cfg.SecureCookie {
		t.Fatal("SecureCookie should default true")
	}
	if len(cfg.AdminHash) == 0 {
		t.Fatal("password should have been hashed")
	}
}

func TestLoadConfigFailsClosed(t *testing.T) {
	cases := map[string]map[string]string{
		"no-key":          {EnvAdminPassword: "pw"},
		"short-key":       {EnvSessionKey: "short", EnvAdminPassword: "pw"},
		"no-credential":   {EnvSessionKey: testKey},
		"both-creds":      {EnvSessionKey: testKey, EnvAdminPassword: "pw", EnvAdminBcryptHash: "$2a$12$abc"},
		"bad-ttl":         {EnvSessionKey: testKey, EnvAdminPassword: "pw", EnvSessionTTL: "nonsense"},
		"nonpositive-ttl": {EnvSessionKey: testKey, EnvAdminPassword: "pw", EnvSessionTTL: "0s"},
	}
	for name, env := range cases {
		if _, err := LoadConfig(envMap(env)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestLoadConfigOverrides(t *testing.T) {
	cfg, err := LoadConfig(envMap(map[string]string{
		EnvSessionKey:      testKey,
		EnvSessionTTL:      "30m",
		EnvAdminUsername:   "owner",
		EnvAdminBcryptHash: string(mustHashForTest("x")),
		EnvInsecureCookie:  "true",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AdminUsername != "owner" {
		t.Fatalf("username = %q", cfg.AdminUsername)
	}
	if cfg.SessionTTL != 30*time.Minute {
		t.Fatalf("ttl = %v", cfg.SessionTTL)
	}
	if cfg.SecureCookie {
		t.Fatal("insecure cookie flag ignored")
	}
}

func mustHashForTest(pw string) []byte {
	h, _ := HashPassword(pw)
	return h
}

func TestBootstrapFromEnv(t *testing.T) {
	p, err := BootstrapFromEnv(envMap(map[string]string{
		EnvSessionKey:    testKey,
		EnvAdminUsername: "owner",
		EnvAdminPassword: "pw",
	}))
	if err != nil {
		t.Fatalf("BootstrapFromEnv: %v", err)
	}
	sess, err := p.Auth.Authenticate("owner", "pw")
	if err != nil {
		t.Fatalf("seeded admin cannot log in: %v", err)
	}
	if sess.Role != RoleAdmin {
		t.Fatalf("seed user role = %q, want Admin", sess.Role)
	}
}

func TestBootstrapFromEnvPropagatesConfigError(t *testing.T) {
	if _, err := BootstrapFromEnv(envMap(map[string]string{})); err == nil {
		t.Fatal("expected error for empty env")
	}
}

// TestBootstrapSeedsDevRel proves the ISI-7455 acceptance: with the DevRel seed
// env set, the demo user can log in after a (re)boot and lands in RoleDevRel,
// while the Admin seed still works unchanged.
func TestBootstrapSeedsDevRel(t *testing.T) {
	p, err := BootstrapFromEnv(envMap(map[string]string{
		EnvSessionKey:     testKey,
		EnvAdminPassword:  "admin-pw",
		EnvDevRelUsername: "henrik",
		EnvDevRelPassword: "devrel-pw",
	}))
	if err != nil {
		t.Fatalf("BootstrapFromEnv: %v", err)
	}
	sess, err := p.Auth.Authenticate("henrik", "devrel-pw")
	if err != nil {
		t.Fatalf("seeded DevRel cannot log in: %v", err)
	}
	if sess.Role != RoleDevRel {
		t.Fatalf("DevRel seed role = %q, want DevRel", sess.Role)
	}
	// Admin seed unchanged.
	if _, err := p.Auth.Authenticate(DefaultAdminUsername, "admin-pw"); err != nil {
		t.Fatalf("admin seed broken by DevRel seed: %v", err)
	}
}

// TestBootstrapNoDevRelSeedByDefault: no DevRel env => only the Admin exists.
func TestBootstrapNoDevRelSeedByDefault(t *testing.T) {
	p, err := BootstrapFromEnv(envMap(map[string]string{
		EnvSessionKey:    testKey,
		EnvAdminPassword: "admin-pw",
	}))
	if err != nil {
		t.Fatalf("BootstrapFromEnv: %v", err)
	}
	users, err := p.Store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 1 || users[0].Role != RoleAdmin {
		t.Fatalf("want exactly one Admin, got %+v", users)
	}
}

func TestDevRelSeedConfigFailsClosed(t *testing.T) {
	cases := map[string]map[string]string{
		"username-without-credential": {EnvSessionKey: testKey, EnvAdminPassword: "pw", EnvDevRelUsername: "henrik"},
		"credential-without-username": {EnvSessionKey: testKey, EnvAdminPassword: "pw", EnvDevRelPassword: "pw"},
		"both-devrel-creds":           {EnvSessionKey: testKey, EnvAdminPassword: "pw", EnvDevRelUsername: "henrik", EnvDevRelPassword: "pw", EnvDevRelBcryptHash: string(mustHashForTest("x"))},
	}
	for name, env := range cases {
		if _, err := LoadConfig(envMap(env)); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// TestBootstrapDevRelCollidesWithAdmin: a DevRel username equal to the Admin's
// is skipped (the Admin seed wins) rather than erroring on ErrUserExists.
func TestBootstrapDevRelCollidesWithAdmin(t *testing.T) {
	p, err := BootstrapFromEnv(envMap(map[string]string{
		EnvSessionKey:     testKey,
		EnvAdminUsername:  "admin",
		EnvAdminPassword:  "admin-pw",
		EnvDevRelUsername: "admin",
		EnvDevRelPassword: "devrel-pw",
	}))
	if err != nil {
		t.Fatalf("BootstrapFromEnv: %v", err)
	}
	// The Admin credential wins; the DevRel password must not have overwritten it.
	sess, err := p.Auth.Authenticate("admin", "admin-pw")
	if err != nil {
		t.Fatalf("admin login broken by colliding DevRel seed: %v", err)
	}
	if sess.Role != RoleAdmin {
		t.Fatalf("role = %q, want Admin", sess.Role)
	}
}
