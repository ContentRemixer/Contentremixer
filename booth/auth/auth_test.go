package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustHash(t *testing.T, pw string) []byte {
	t.Helper()
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return h
}

const testKey = "0123456789abcdef0123456789abcdef" // 32 bytes

func newTestCodec(t *testing.T) *TokenCodec {
	t.Helper()
	c, err := NewTokenCodec([]byte(testKey), time.Hour)
	if err != nil {
		t.Fatalf("NewTokenCodec: %v", err)
	}
	return c
}

func TestBcryptRoundTrip(t *testing.T) {
	h := mustHash(t, "s3cret")
	if !verifyPassword(h, "s3cret") {
		t.Fatal("correct password rejected")
	}
	if verifyPassword(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if verifyPassword([]byte("not-a-hash"), "s3cret") {
		t.Fatal("malformed hash accepted")
	}
}

func TestRoleValid(t *testing.T) {
	if !RoleAdmin.Valid() || !RoleDevRel.Valid() {
		t.Fatal("known roles should be valid")
	}
	if Role("Root").Valid() {
		t.Fatal("unknown role should be invalid")
	}
}

func TestTokenCodecKeyTooShort(t *testing.T) {
	if _, err := NewTokenCodec([]byte("short"), time.Hour); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestTokenCodecDefaultTTL(t *testing.T) {
	c, err := NewTokenCodec([]byte(testKey), 0)
	if err != nil {
		t.Fatalf("NewTokenCodec: %v", err)
	}
	if c.TTL() != DefaultSessionTTL {
		t.Fatalf("TTL = %v, want %v", c.TTL(), DefaultSessionTTL)
	}
}

func TestTokenMintVerify(t *testing.T) {
	c := newTestCodec(t)
	want := Session{Subject: "alice", Role: RoleAdmin}
	tok, err := c.Mint(want)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got, err := c.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTokenVerifyRejectsTamper(t *testing.T) {
	c := newTestCodec(t)
	tok, _ := c.Mint(Session{Subject: "alice", Role: RoleAdmin})

	cases := map[string]string{
		"no-dot":      "abcdef",
		"bad-payload": "!!!." + strings.SplitN(tok, ".", 2)[1],
		"bad-sig-b64": strings.SplitN(tok, ".", 2)[0] + ".!!!",
		"wrong-sig":   strings.SplitN(tok, ".", 2)[0] + ".AAAA",
		"empty":       "",
	}
	for name, bad := range cases {
		if _, err := c.Verify(bad); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestTokenVerifyWrongKey(t *testing.T) {
	c := newTestCodec(t)
	tok, _ := c.Mint(Session{Subject: "alice", Role: RoleAdmin})
	other, _ := NewTokenCodec([]byte("ffffffffffffffffffffffffffffffff"), time.Hour)
	if _, err := other.Verify(tok); err != ErrInvalidToken {
		t.Fatalf("got %v, want ErrInvalidToken", err)
	}
}

func TestTokenExpiry(t *testing.T) {
	c := newTestCodec(t)
	base := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return base }
	tok, _ := c.Mint(Session{Subject: "alice", Role: RoleAdmin})

	c.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err := c.Verify(tok); err != ErrExpiredToken {
		t.Fatalf("got %v, want ErrExpiredToken", err)
	}
}

func TestTokenVerifyRejectsBadClaims(t *testing.T) {
	c := newTestCodec(t)
	// Forge a payload with an empty subject but a valid signature.
	bad, _ := c.Mint(Session{Subject: "", Role: RoleAdmin})
	if _, err := c.Verify(bad); err != ErrInvalidToken {
		t.Fatalf("empty subject: got %v, want ErrInvalidToken", err)
	}
	badRole, _ := c.Mint(Session{Subject: "x", Role: Role("Nope")})
	if _, err := c.Verify(badRole); err != ErrInvalidToken {
		t.Fatalf("bad role: got %v, want ErrInvalidToken", err)
	}
}

func TestAuthenticator(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Create(User{Username: "alice", BcryptHash: mustHash(t, "pw"), Role: RoleAdmin}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	a := NewBcryptAuthenticator(store)

	sess, err := a.Authenticate("alice", "pw")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if sess.Subject != "alice" || sess.Role != RoleAdmin {
		t.Fatalf("unexpected session %+v", sess)
	}
	if _, err := a.Authenticate("alice", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: got %v", err)
	}
	if _, err := a.Authenticate("ghost", "pw"); err != ErrInvalidCredentials {
		t.Fatalf("unknown user: got %v", err)
	}
}

func TestMemoryStoreDuplicate(t *testing.T) {
	s := NewMemoryStore()
	u := User{Username: "a", BcryptHash: []byte("h"), Role: RoleAdmin}
	if err := s.Create(u); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(u); err != ErrUserExists {
		t.Fatalf("got %v, want ErrUserExists", err)
	}
	if _, err := s.Get("missing"); err != ErrUserNotFound {
		t.Fatalf("got %v, want ErrUserNotFound", err)
	}
}

func protectedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := SessionFromContext(r.Context())
		if !ok {
			t := "no session in context"
			http.Error(w, t, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(sess.Subject))
	})
}

func TestMiddlewareNoSession(t *testing.T) {
	m := NewMiddleware(newTestCodec(t))
	rec := httptest.NewRecorder()
	m.Wrap(protectedHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestMiddlewareInvalidToken(t *testing.T) {
	m := NewMiddleware(newTestCodec(t))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()
	m.Wrap(protectedHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestMiddlewareCookieAndBearer(t *testing.T) {
	c := newTestCodec(t)
	m := NewMiddleware(c)
	tok, _ := c.Mint(Session{Subject: "alice", Role: RoleAdmin})

	// via cookie
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: tok})
	rec := httptest.NewRecorder()
	m.Wrap(protectedHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
		t.Fatalf("cookie path: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// via bearer
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	m.Wrap(protectedHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "alice" {
		t.Fatalf("bearer path: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func testProvider(t *testing.T) *Provider {
	t.Helper()
	p, err := Bootstrap(Config{
		SessionKey:    []byte(testKey),
		SessionTTL:    time.Hour,
		AdminUsername: "boss",
		AdminHash:     mustHash(t, "hunter2"),
		SecureCookie:  true,
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return p
}

func TestLoginLogoutFlow(t *testing.T) {
	p := testProvider(t)

	body, _ := json.Marshal(loginRequest{Username: "boss", Password: "hunter2"})
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	p.Handlers.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login code = %d, body=%s", rec.Code, rec.Body.String())
	}
	var sess Session
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	if sess.Subject != "boss" || sess.Role != RoleAdmin {
		t.Fatalf("unexpected session %+v", sess)
	}
	cookies := rec.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			session = c
		}
	}
	if session == nil || session.Value == "" {
		t.Fatal("no session cookie set")
	}
	if !session.HttpOnly || !session.Secure {
		t.Fatal("cookie should be HttpOnly and Secure")
	}

	// The cookie drives a protected request.
	preq := httptest.NewRequest(http.MethodGet, "/me", nil)
	preq.AddCookie(session)
	prec := httptest.NewRecorder()
	p.Middleware.Wrap(http.HandlerFunc(p.Handlers.WhoAmI)).ServeHTTP(prec, preq)
	if prec.Code != http.StatusOK {
		t.Fatalf("whoami code = %d", prec.Code)
	}

	// Logout clears it.
	lrec := httptest.NewRecorder()
	p.Handlers.Logout(lrec, httptest.NewRequest(http.MethodPost, "/logout", nil))
	if lrec.Code != http.StatusNoContent {
		t.Fatalf("logout code = %d", lrec.Code)
	}
	cleared := lrec.Result().Cookies()[0]
	if cleared.MaxAge >= 0 {
		t.Fatalf("logout should expire cookie, got MaxAge=%d", cleared.MaxAge)
	}
}

func TestLoginRejectsBadCredential(t *testing.T) {
	p := testProvider(t)
	body, _ := json.Marshal(loginRequest{Username: "boss", Password: "nope"})
	rec := httptest.NewRecorder()
	p.Handlers.Login(rec, httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}

func TestLoginRejectsNonPOSTAndBadBody(t *testing.T) {
	p := testProvider(t)
	rec := httptest.NewRecorder()
	p.Handlers.Login(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: got %d, want 405", rec.Code)
	}
	rec = httptest.NewRecorder()
	p.Handlers.Login(rec, httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("{bad")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: got %d, want 400", rec.Code)
	}
}

func TestWhoAmIWithoutSession(t *testing.T) {
	p := testProvider(t)
	rec := httptest.NewRecorder()
	p.Handlers.WhoAmI(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", rec.Code)
	}
}
