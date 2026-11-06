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
