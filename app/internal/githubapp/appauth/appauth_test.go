package appauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return p, key
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New("123", []byte("not a pem")); err == nil {
		t.Fatal("expected error for non-PEM key")
	}
	if _, err := New("", mustPEM(t)); err == nil {
		t.Fatal("expected error for empty app id")
	}
}

func TestNewAcceptsPKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := New("42", p); err != nil {
		t.Fatalf("PKCS#8 key should parse: %v", err)
	}
}

func TestAppJWTIsVerifiableRS256(t *testing.T) {
	pemKey, key := testKeyPEM(t)
	m, err := New("app-77", pemKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tok, err := m.appJWT(now)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt should have 3 parts, got %d", len(parts))
	}

	// Header.
	var hdr map[string]string
	decodeSeg(t, parts[0], &hdr)
	if hdr["alg"] != "RS256" || hdr["typ"] != "JWT" {
		t.Fatalf("bad header: %v", hdr)
	}

	// Claims.
	var claims map[string]any
	decodeSeg(t, parts[1], &claims)
	if claims["iss"] != "app-77" {
		t.Fatalf("iss = %v, want app-77", claims["iss"])
	}
	iat := int64(claims["iat"].(float64))
	exp := int64(claims["exp"].(float64))
	if iat != now.Add(-jwtBackdate).Unix() {
		t.Fatalf("iat = %d, want %d", iat, now.Add(-jwtBackdate).Unix())
	}
	if exp != now.Add(jwtTTL).Unix() {
		t.Fatalf("exp = %d, want %d", exp, now.Add(jwtTTL).Unix())
	}

	// Signature verifies against the public key.
	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
}

func TestInstallationTokenMintsAndCaches(t *testing.T) {
	pemKey, _ := testKeyPEM(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/app/installations/555/access_tokens" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Errorf("missing bearer auth: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_installationtoken",
			"expires_at": time.Unix(1_700_000_000, 0).Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	m, err := New("app-1", pemKey)
	if err != nil {
		t.Fatal(err)
	}
	m.BaseURL = srv.URL
	m.now = func() time.Time { return time.Unix(1_700_000_000, 0) }

	tok, err := m.InstallationToken(context.Background(), 555)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_installationtoken" {
		t.Fatalf("token = %q", tok)
	}

	// Second call within the lifetime is served from cache (no new HTTP call).
	if _, err := m.InstallationToken(context.Background(), 555); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 mint call (second cached), got %d", got)
	}
}

func TestInstallationTokenReMintsNearExpiry(t *testing.T) {
	pemKey, _ := testKeyPEM(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      fmt.Sprintf("ghs_token_%d", n),
			"expires_at": time.Unix(1_700_000_000, 0).Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	m, err := New("app-1", pemKey)
	if err != nil {
		t.Fatal(err)
	}
	m.BaseURL = srv.URL

	cur := time.Unix(1_700_000_000, 0)
	m.now = func() time.Time { return cur }

	if _, err := m.InstallationToken(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	// Advance to within renewMargin of expiry: should re-mint.
	cur = cur.Add(time.Hour - renewMargin + time.Second)
	tok, err := m.InstallationToken(context.Background(), 9)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_token_2" {
		t.Fatalf("expected re-mint to ghs_token_2, got %q", tok)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 mint calls, got %d", got)
	}
}

func TestInstallationTokenZeroID(t *testing.T) {
	pemKey, _ := testKeyPEM(t)
	m, _ := New("app-1", pemKey)
	if _, err := m.InstallationToken(context.Background(), 0); err == nil {
		t.Fatal("expected error for zero installation id")
	}
}

func TestInstallationTokenServerError(t *testing.T) {
	pemKey, _ := testKeyPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	m, _ := New("app-1", pemKey)
	m.BaseURL = srv.URL
	if _, err := m.InstallationToken(context.Background(), 7); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestParseInstallationID(t *testing.T) {
	cases := map[string]int64{
		`123`:    123,
		`"456"`:  456,
		`"nope"`: 0,
		``:       0,
		`{}`:     0,
	}
	for in, want := range cases {
		if got := ParseInstallationID(json.RawMessage(in)); got != want {
			t.Errorf("ParseInstallationID(%q) = %d, want %d", in, got, want)
		}
	}
}

func decodeSeg(t *testing.T, seg string, v any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal segment: %v", err)
	}
}

func mustPEM(t *testing.T) []byte {
	t.Helper()
	p, _ := testKeyPEM(t)
	return p
}
