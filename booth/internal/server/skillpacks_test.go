package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"testing"
	"testing/fstest"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/skillpacks"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

// newSkillServer is newTestServer with a skill-pack FS wired, so the test can
// exercise discovery through the handler. It returns the handler and a codec.
func newSkillServer(t *testing.T, fsys fs.FS) (http.Handler, *auth.TokenCodec) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	st := store.New(c, ns, audit.NewSlogLogger(nil))
	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey: testKey, SessionTTL: time.Hour,
		AdminUsername: "admin", AdminHash: adminHash, SecureCookie: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(prov, st, nil, nil, nil, nil, nil, nil).WithSkillPacks(fsys)
	return srv.Handler(), prov.Codec
}

func TestSkillPacksAdminGated(t *testing.T) {
	h, codec := newSkillServer(t, fstest.MapFS{})
	// Unauthenticated → 401.
	if w := do(t, h, "GET", "/v1/skillpacks", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
	// DevRel → 403 (platform content, Admin-only like Users).
	dev := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "GET", "/v1/skillpacks", dev, ""); w.Code != http.StatusForbidden {
		t.Fatalf("devrel = %d, want 403", w.Code)
	}
}

func TestSkillPacksReturnsCatalogAndDefaults(t *testing.T) {
	fsys := fstest.MapFS{
		"blog-writing/SKILL.md":       {Data: []byte("---\ndescription: Write blog posts.\n---\n# Blog writing skill\n")},
		"human-voice-review/SKILL.md": {Data: []byte("---\ndescription: Strip AI tells.\n---\n# Human voice review\n")},
		"seo/SKILL.md":                {Data: []byte("---\ndescription: Optimize for search.\n---\n# SEO skill\n")},
	}
	h, codec := newSkillServer(t, fsys)
	admin := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)

	w := do(t, h, "GET", "/v1/skillpacks", admin, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin GET = %d, want 200", w.Code)
	}
	var got struct {
		Packs           []skillpacks.Pack   `json:"packs"`
		Defaults        map[string][]string `json:"defaults"`
		DynamicFallback bool                `json:"dynamicFallback"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Packs) != 3 {
		t.Fatalf("got %d packs, want 3: %+v", len(got.Packs), got.Packs)
	}
	// The default composition is surfaced verbatim from the shared catalog.
	if want := []string{"blog-writing", "human-voice-review", "seo"}; !equalStrs(got.Defaults["content-writer"], want) {
		t.Fatalf("content-writer default = %v, want %v", got.Defaults["content-writer"], want)
	}
	if !got.DynamicFallback {
		t.Fatal("dynamicFallback should be true (unlisted skills resolve to a same-named pack)")
	}
}

func TestSkillPacksEmptyCatalogStillServesDefaults(t *testing.T) {
	// No skills FS wired at all: packs is an empty array (never null), and the
	// static default composition is still returned.
	h, codec := newSkillServer(t, nil)
	admin := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	w := do(t, h, "GET", "/v1/skillpacks", admin, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin GET = %d, want 200", w.Code)
	}
	var got struct {
		Packs    []skillpacks.Pack   `json:"packs"`
		Defaults map[string][]string `json:"defaults"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Packs == nil {
		t.Fatal("packs must serialize as [] not null")
	}
	if len(got.Packs) != 0 {
		t.Fatalf("want empty catalog, got %+v", got.Packs)
	}
	if len(got.Defaults) == 0 {
		t.Fatal("defaults must be present even with an empty catalog")
	}
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
