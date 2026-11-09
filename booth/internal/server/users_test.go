package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
)

// The seed Admin ("admin") is always present (Bootstrap), so it is the last
// Admin in every fresh test server.

func TestUsersAdminOnly(t *testing.T) {
	h, codec := newTestServer(t)
	devTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	cases := []struct{ method, path, body string }{
		{"GET", "/v1/users", ""},
		{"POST", "/v1/users", `{"username":"bob","password":"password1","role":"DevRel"}`},
		{"PATCH", "/v1/users/admin", `{"role":"DevRel"}`},
		{"DELETE", "/v1/users/admin", ""},
	}
	for _, c := range cases {
		if w := do(t, h, c.method, c.path, devTok, c.body); w.Code != http.StatusForbidden {
			t.Fatalf("DevRel %s %s = %d, want 403", c.method, c.path, w.Code)
		}
		// Unauthenticated → 401.
		if w := do(t, h, c.method, c.path, "", c.body); w.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s = %d, want 401", c.method, c.path, w.Code)
		}
	}
}

func TestUsersCRUD(t *testing.T) {
	h, codec := newTestServer(t)
	adminTok := tokenFor(t, codec, "admin", auth.RoleAdmin)

	// List: just the seed admin.
	w := do(t, h, "GET", "/v1/users", adminTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", w.Code)
	}
	var users []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(users) != 1 || users[0]["username"] != "admin" {
		t.Fatalf("initial users = %v, want [admin]", users)
	}
	// Hash must never be serialised (§8).
	if strings.Contains(strings.ToLower(w.Body.String()), "hash") || strings.Contains(w.Body.String(), "$2") {
		t.Fatalf("user list leaked a hash: %s", w.Body.String())
	}

	// Create a DevRel.
	if w := do(t, h, "POST", "/v1/users", adminTok, `{"username":"alice","password":"password1","role":"DevRel"}`); w.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201", w.Code)
	}
	// The new user can log in with the set password.
	if w := do(t, h, "POST", "/v1/auth/login", "", `{"username":"alice","password":"password1"}`); w.Code != http.StatusOK {
		t.Fatalf("new user login = %d, want 200", w.Code)
	}

	// Promote alice to Admin, then demote the seed admin (allowed now: 2 admins).
	if w := do(t, h, "PATCH", "/v1/users/alice", adminTok, `{"role":"Admin"}`); w.Code != http.StatusOK {
		t.Fatalf("promote = %d, want 200", w.Code)
	}
	if w := do(t, h, "PATCH", "/v1/users/admin", adminTok, `{"role":"DevRel"}`); w.Code != http.StatusOK {
		t.Fatalf("demote seed (2 admins) = %d, want 200", w.Code)
	}

	// Delete the (now-DevRel) seed admin.
	if w := do(t, h, "DELETE", "/v1/users/admin", adminTok, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", w.Code)
	}
	if w := do(t, h, "DELETE", "/v1/users/admin", adminTok, ""); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", w.Code)
	}
}

func TestLastAdminProtected(t *testing.T) {
	h, codec := newTestServer(t)
	adminTok := tokenFor(t, codec, "admin", auth.RoleAdmin)

	// Only one Admin exists — deleting or demoting it must be refused (409).
	if w := do(t, h, "DELETE", "/v1/users/admin", adminTok, ""); w.Code != http.StatusConflict {
		t.Fatalf("delete last admin = %d, want 409", w.Code)
	}
	if w := do(t, h, "PATCH", "/v1/users/admin", adminTok, `{"role":"DevRel"}`); w.Code != http.StatusConflict {
		t.Fatalf("demote last admin = %d, want 409", w.Code)
	}
}

func TestCreateUserValidation(t *testing.T) {
	h, codec := newTestServer(t)
	adminTok := tokenFor(t, codec, "admin", auth.RoleAdmin)

	cases := []struct {
		name, body string
		want       int
	}{
		{"short password", `{"username":"x","password":"short","role":"DevRel"}`, http.StatusBadRequest},
		{"empty username", `{"username":"","password":"password1","role":"DevRel"}`, http.StatusBadRequest},
		{"bad role", `{"username":"x","password":"password1","role":"Wizard"}`, http.StatusBadRequest},
		{"malformed", `{not json`, http.StatusBadRequest},
		{"ok", `{"username":"x","password":"password1","role":"DevRel"}`, http.StatusCreated},
		{"duplicate", `{"username":"x","password":"password1","role":"DevRel"}`, http.StatusConflict},
	}
	for _, c := range cases {
		if w := do(t, h, "POST", "/v1/users", adminTok, c.body); w.Code != c.want {
			t.Fatalf("%s: create = %d, want %d", c.name, w.Code, c.want)
		}
	}
}

// TestDevRelPersonaRoute verifies the newly-wired owner-scoped persona route:
// a DevRel owns their own personas (clause 2) but cannot create one for someone
// else, and an Admin sees all.
func TestDevRelPersonaRoute(t *testing.T) {
	h, codec := newTestServer(t)
	aliceTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	bobTok := tokenFor(t, codec, "sub-bob", auth.RoleDevRel)
	adminTok := tokenFor(t, codec, "admin", auth.RoleAdmin)

	// Alice creates her own persona → 201.
	own := `{"metadata":{"name":"alice-voice"},"spec":{"owner":{"subject":"sub-alice"}}}`
	if w := do(t, h, "POST", "/v1/devrelpersonas", aliceTok, own); w.Code != http.StatusCreated {
		t.Fatalf("alice create own persona = %d, want 201", w.Code)
	}
	// Alice cannot create a persona owned by Bob → 403.
	notOwn := `{"metadata":{"name":"bob-voice"},"spec":{"owner":{"subject":"sub-bob"}}}`
	if w := do(t, h, "POST", "/v1/devrelpersonas", aliceTok, notOwn); w.Code != http.StatusForbidden {
		t.Fatalf("alice create bob persona = %d, want 403", w.Code)
	}
	// Alice sees her own persona in the list; Bob sees none.
	if n := listLen(t, h, "/v1/devrelpersonas", aliceTok); n != 1 {
		t.Fatalf("alice persona list = %d, want 1", n)
	}
	if n := listLen(t, h, "/v1/devrelpersonas", bobTok); n != 0 {
		t.Fatalf("bob persona list = %d, want 0", n)
	}
	// Admin sees all personas (clause 1).
	if n := listLen(t, h, "/v1/devrelpersonas", adminTok); n != 1 {
		t.Fatalf("admin persona list = %d, want 1", n)
	}
	// Alice can read her own; Bob gets 403 on it.
	if w := do(t, h, "GET", "/v1/devrelpersonas/alice-voice", aliceTok, ""); w.Code != http.StatusOK {
		t.Fatalf("alice get own = %d, want 200", w.Code)
	}
	if w := do(t, h, "GET", "/v1/devrelpersonas/alice-voice", bobTok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("bob get alice persona = %d, want 403", w.Code)
	}
}

func listLen(t *testing.T, h http.Handler, path, bearer string) int {
	t.Helper()
	w := do(t, h, "GET", path, bearer, "")
	if w.Code != http.StatusOK {
		t.Fatalf("list %s = %d, want 200", path, w.Code)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return len(list)
}
