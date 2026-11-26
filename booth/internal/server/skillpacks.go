package server

import (
	"io/fs"
	"net/http"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/internal/skillpacks"
)

// WithSkillPacks attaches the reference-repo filesystem that GET /v1/skillpacks
// discovers skill packs from (ISI-7856) and returns the Server for chaining. A
// setter (like WithTutorialRuns) keeps New's signature stable. A nil fsys leaves
// discovery empty — the endpoint still serves the static default composition, so
// the route is always mounted.
func (s *Server) WithSkillPacks(fsys fs.FS) *Server {
	s.skillPacksFS = fsys
	return s
}

// mountSkillPacks wires GET /v1/skillpacks. Admin-gated in the handler
// (requireAdmin): skill packs are platform content with no per-row owner,
// exactly like the Users and ReferenceRepo-Secrets surfaces. Pure read.
func (s *Server) mountSkillPacks(mux *http.ServeMux) {
	mux.Handle("GET /v1/skillpacks", s.protect(s.listSkillPacks))
}

// skillPacksResponse is the read model the Agent "Skills…" editor consumes: the
// discovered pack catalog, plus the default per-skill composition so the UI can
// render "Default for a content-writer: blog-writing, human-voice-review, seo".
type skillPacksResponse struct {
	// Packs is the catalog discovered from the reference repo (sorted by name).
	// Empty when no reference-repo filesystem is wired or it has no skills dir.
	Packs []skillpacks.Pack `json:"packs"`
	// Defaults is the explicit skill → ordered packs composition map.
	Defaults map[string][]string `json:"defaults"`
	// DynamicFallback states the rule for a skill NOT in Defaults: it resolves to
	// a single pack whose name equals the skill. Lets the UI explain any skill's
	// default without enumerating every possible skill.
	DynamicFallback bool `json:"dynamicFallback"`
}

// listSkillPacks returns the discovered pack catalog and the default composition.
func (s *Server) listSkillPacks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	packs, err := skillpacks.Catalog(s.skillPacksFS)
	if err != nil {
		// Catalog is degrade-never, so this is unexpected; serve the composition
		// with an empty catalog rather than failing the whole read.
		s.log.Error("skill-pack discovery failed", "err", err)
		packs = nil
	}
	if packs == nil {
		packs = []skillpacks.Pack{}
	}
	writeJSON(w, http.StatusOK, skillPacksResponse{
		Packs:           packs,
		Defaults:        contentremixerv1alpha1.SkillComposition,
		DynamicFallback: true,
	})
}
