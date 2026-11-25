package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
)

// The @mention directory is readable by any authenticated session (unlike the
// Admin-only /v1/users) and returns the fixed agent personas plus the username
// roster.
func TestMentionDirectory(t *testing.T) {
	h, codec := newTestServer(t)
	devTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	// Anonymous → 401.
	if w := do(t, h, "GET", "/v1/mention-directory", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anon directory = %d, want 401", w.Code)
	}

	// A DevRel (not an Admin) may read it.
	w := do(t, h, "GET", "/v1/mention-directory", devTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("devrel directory = %d, want 200", w.Code)
	}
	var out mentionDirectoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode directory: %v", err)
	}
	if len(out.Agents) != 4 {
		t.Fatalf("agents = %d, want 4 board-approved kinds", len(out.Agents))
	}
	var sawGraphic bool
	for _, a := range out.Agents {
		if !a.IsAgent {
			t.Fatalf("agent %q not flagged isAgent", a.Name)
		}
		if a.ID == "graphic-designer" && a.Name == "Graphic Designer" {
			sawGraphic = true
		}
	}
	if !sawGraphic {
		t.Fatalf("agents missing graphic-designer: %+v", out.Agents)
	}
	// The seed Admin "admin" is always present as a person.
	var sawAdmin bool
	for _, p := range out.People {
		if p.IsAgent {
			t.Fatalf("person %q wrongly flagged isAgent", p.Name)
		}
		if p.ID == "admin" {
			sawAdmin = true
		}
	}
	if !sawAdmin {
		t.Fatalf("people missing seed admin: %+v", out.People)
	}
}
