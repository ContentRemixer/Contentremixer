package server

import (
	"net/http"

	appcontent "github.com/ContentRemixer/Contentremixer/app/content"
)

// mountDirectory wires the @mention directory the comment composer sources
// (plan §6 P2, ISI-7819). Unlike /v1/users (Admin-only, W6) this is a thin,
// read-only directory any authenticated reviewer can read: the agent half is
// the board-approved fixed persona set, the people half is the username roster
// (no hashes, no roles beyond what /v1/users already exposes). It is NOT a CRUD
// surface — only a lookup the popover filters client-side.
func (s *Server) mountDirectory(mux *http.ServeMux) {
	mux.Handle("GET /v1/mention-directory", s.protect(s.mentionDirectory))
}

// mentionEntry is one directory row. IsAgent drives the Agents/People split and
// the agent:// vs person:// URI the composer emits.
type mentionEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    string `json:"role,omitempty"`
	IsAgent bool   `json:"isAgent"`
}

// mentionDirectoryResponse is the GET /v1/mention-directory body: the two
// namespaces the popover shows as separate groups.
type mentionDirectoryResponse struct {
	Agents []mentionEntry `json:"agents"`
	People []mentionEntry `json:"people"`
}

func (s *Server) mentionDirectory(w http.ResponseWriter, r *http.Request) {
	if _, ok := session(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	agents := appcontent.DefaultAgentDirectory()
	out := mentionDirectoryResponse{
		Agents: make([]mentionEntry, 0, len(agents)),
		People: []mentionEntry{},
	}
	for _, a := range agents {
		out.Agents = append(out.Agents, mentionEntry{ID: a.Ref, Name: a.Label, Role: "Agent", IsAgent: true})
	}

	users, err := s.auth.Store.List()
	if err != nil {
		s.writeUserErr(w, err)
		return
	}
	for _, u := range users {
		out.People = append(out.People, mentionEntry{ID: u.Username, Name: u.Username, Role: string(u.Role)})
	}

	writeJSON(w, http.StatusOK, out)
}
