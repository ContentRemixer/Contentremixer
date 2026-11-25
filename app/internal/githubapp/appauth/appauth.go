// Package appauth mints short-lived GitHub App installation tokens (ISI-7836).
//
// A GitHub App removes the per-repo PAT + manual-webhook onboarding dance: the
// webhook URL/secret/events are configured once on the App, onboarding a repo
// is a one-click Install, and git writes use installation tokens minted from
// the App key instead of a long-lived PAT in the cluster.
//
// The REST calls the recycle pipeline makes (issue comments, git data, pulls)
// accept an installation token as a Bearer credential exactly like a PAT, so
// the token this package returns is a drop-in for the resolved-from-Secret PAT
// everywhere downstream (CommentPoster, pr.Assembler). Only the acquisition
// differs, and it lives here.
//
// Flow (design: GitHub App auth):
//  1. Build an App JWT: RS256 over {iss: app-id, iat, exp} signed with the
//     App private key. Short-lived (<=10 min, GitHub's cap).
//  2. POST /app/installations/{id}/access_tokens with that JWT → a token that
//     expires in ~1h, scoped to the installation (the repos it's installed on).
//  3. Cache per installation until shortly before expiry so steady-state
//     deliveries do not re-mint on every event.
//
// Stdlib only (crypto/rsa + encoding/json), consistent with the rest of
// internal/githubapp — the handful of calls here does not justify a dependency.
package appauth

import (
	"bytes"
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
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// jwtTTL is how long an App JWT is valid. GitHub caps App JWTs at 10 minutes;
// we use 9 to leave headroom for clock skew on GitHub's side.
const jwtTTL = 9 * time.Minute

// jwtBackdate shifts iat into the past to tolerate this pod's clock running
// slightly ahead of GitHub's (GitHub rejects JWTs whose iat is in the future).
const jwtBackdate = 30 * time.Second

// renewMargin re-mints an installation token this long before its stated
// expiry, so an in-flight pipeline run never races the expiry.
const renewMargin = 2 * time.Minute

// Minter builds App JWTs and exchanges them for cached installation tokens.
// The zero value is not usable — construct with New.
type Minter struct {
	appID      string
	privateKey *rsa.PrivateKey

	// BaseURL defaults to https://api.github.com; overridable for GH
	// Enterprise or tests.
	BaseURL string
	// HTTP is the client used; defaults to a 15s-timeout client.
	HTTP *http.Client
	// now is overridable in tests; defaults to time.Now.
	now func() time.Time

	mu    sync.Mutex
	cache map[int64]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

// New constructs a Minter from the App ID and its PEM-encoded private key
// (PKCS#1 or PKCS#8). Returns an error if the key cannot be parsed, so a
// mis-provisioned github-app Secret fails loudly at wiring time rather than on
// the first delivery.
func New(appID string, pemKey []byte) (*Minter, error) {
	if appID == "" {
		return nil, fmt.Errorf("appauth: empty app id")
	}
	key, err := parseRSAPrivateKey(pemKey)
	if err != nil {
		return nil, err
	}
	return &Minter{
		appID:      appID,
		privateKey: key,
		cache:      map[int64]cachedToken{},
	}, nil
}

func parseRSAPrivateKey(pemKey []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemKey)
	if block == nil {
		return nil, fmt.Errorf("appauth: no PEM block in private key")
	}
	// GitHub hands out PKCS#1 ("RSA PRIVATE KEY"); accept PKCS#8 too.
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("appauth: parse private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("appauth: private key is %T, want RSA", parsed)
	}
	return key, nil
}

// InstallationToken returns a valid installation token for installationID,
// minting (and caching) a fresh one when none is cached or the cached one is
// within renewMargin of expiry.
func (m *Minter) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if installationID == 0 {
		return "", fmt.Errorf("appauth: installation id is zero (no installation in delivery)")
	}
	now := m.clock()

	m.mu.Lock()
	if c, ok := m.cache[installationID]; ok && now.Before(c.expires.Add(-renewMargin)) {
		tok := c.token
		m.mu.Unlock()
		return tok, nil
	}
	m.mu.Unlock()

	tok, expires, err := m.mint(ctx, installationID, now)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.cache[installationID] = cachedToken{token: tok, expires: expires}
	m.mu.Unlock()
	return tok, nil
}

// mint exchanges a fresh App JWT for an installation token.
func (m *Minter) mint(ctx context.Context, installationID int64, now time.Time) (string, time.Time, error) {
	jwt, err := m.appJWT(now)
	if err != nil {
		return "", time.Time{}, err
	}

	base := m.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	hc := m.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", base, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := hc.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", time.Time{}, fmt.Errorf("appauth: mint installation %d token returned %s: %s", installationID, resp.Status, bytes.TrimSpace(snippet))
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", time.Time{}, fmt.Errorf("appauth: decode token response: %w", err)
	}
	if out.Token == "" {
		return "", time.Time{}, fmt.Errorf("appauth: empty token in installation %d response", installationID)
	}
	// If GitHub omits expires_at, fall back to its documented 1h lifetime.
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = now.Add(time.Hour)
	}
	return out.Token, out.ExpiresAt, nil
}

// appJWT builds and signs the App-authentication JWT (RS256) GitHub requires
// to call the /app/* endpoints.
func (m *Minter) appJWT(now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtTTL).Unix(),
		"iss": m.appID,
	}

	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64(hb) + "." + b64(cb)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, m.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("appauth: sign jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func (m *Minter) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// ParseInstallationID tolerantly reads an installation id that may arrive as a
// JSON number or string (GitHub sends a number; be liberal in what we accept).
func ParseInstallationID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
