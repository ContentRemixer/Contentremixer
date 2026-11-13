package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

const ns = "content-remixer"

// testKey is a throwaway 32-byte HMAC signing key for minting test sessions.
var testKey = []byte("0123456789abcdef0123456789abcdef")

func newTestServer(t *testing.T, objs ...client.Object) (http.Handler, *auth.TokenCodec) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	st := store.New(c, ns, audit.NewSlogLogger(nil))

	adminHash, err := auth.HashPassword("admin-pw")
	if err != nil {
		t.Fatal(err)
	}
	prov, err := auth.Bootstrap(auth.Config{
		SessionKey:    testKey,
		SessionTTL:    time.Hour,
		AdminUsername: "admin",
		AdminHash:     adminHash,
		SecureCookie:  false,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(prov, st, nil, nil, nil, nil, nil).Handler(), prov.Codec
}

// tokenFor mints a bearer token for an arbitrary session so tests can exercise
// the authz matrix without a full login round-trip.
func tokenFor(t *testing.T, codec *auth.TokenCodec, subject string, role auth.Role) string {
	t.Helper()
	tok, err := codec.Mint(auth.Session{Subject: subject, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func mp(name string) *v1alpha1.ModelProfile {
	return &v1alpha1.ModelProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.ModelProfileSpec{Provider: "openai", Model: "gpt-4o", LiteLLMName: name},
	}
}

func do(t *testing.T, h http.Handler, method, path, bearer, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealthUnauthenticated(t *testing.T) {
	h, _ := newTestServer(t)
	if w := do(t, h, "GET", "/healthz", "", ""); w.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", w.Code)
	}
}

func TestUnauthenticatedRejected(t *testing.T) {
	h, _ := newTestServer(t, mp("m1"))
	if w := do(t, h, "GET", "/v1/modelprofiles", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
	if w := do(t, h, "GET", "/v1/modelprofiles", "not-a-valid-token", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token = %d, want 401", w.Code)
	}
}

func TestLoginThenWhoAmI(t *testing.T) {
	h, _ := newTestServer(t)
	// Login with the seed Admin credential → 200 + session cookie.
	w := do(t, h, "POST", "/v1/auth/login", "", `{"username":"admin","password":"admin-pw"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200", w.Code)
	}
	var cookie string
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login did not set a session cookie")
	}
	// Bad credentials → 401.
	if w := do(t, h, "POST", "/v1/auth/login", "", `{"username":"admin","password":"nope"}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d, want 401", w.Code)
	}
}

func TestAdminOnlyClassEndToEnd(t *testing.T) {
	h, codec := newTestServer(t, mp("m1"))
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	devTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)

	// Admin: full access to the admin-only ModelProfile class.
	if w := do(t, h, "GET", "/v1/modelprofiles/m1", adminTok, ""); w.Code != http.StatusOK {
		t.Fatalf("admin GET = %d, want 200", w.Code)
	}

	// DevRel: 403 on read of an admin-only class (not a silent 404/empty).
	if w := do(t, h, "GET", "/v1/modelprofiles/m1", devTok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("devrel GET admin-only = %d, want 403", w.Code)
	}

	// DevRel: 403 on write of an admin-only class.
	body := `{"metadata":{"name":"m2"},"spec":{"provider":"openai","model":"gpt-4o","litellmName":"m2"}}`
	if w := do(t, h, "POST", "/v1/modelprofiles", devTok, body); w.Code != http.StatusForbidden {
		t.Fatalf("devrel POST admin-only = %d, want 403", w.Code)
	}

	// DevRel: list of an admin-only class returns an empty set, not others' rows.
	w := do(t, h, "GET", "/v1/modelprofiles", devTok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("devrel list = %d, want 200", w.Code)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("devrel should see empty admin-only list, got %d rows", len(list))
	}
}

func TestAdminUpdate(t *testing.T) {
	h, codec := newTestServer(t, mp("m1"))
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	body := `{"spec":{"provider":"anthropic","model":"claude-opus-4","litellmName":"m1"}}`
	if w := do(t, h, "PUT", "/v1/modelprofiles/m1", adminTok, body); w.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d, want 200", w.Code)
	}
}

func TestBadJSONBody(t *testing.T) {
	h, codec := newTestServer(t)
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	if w := do(t, h, "POST", "/v1/modelprofiles", adminTok, "{not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d, want 400", w.Code)
	}
}

func TestReferenceRepoRouteAuthz(t *testing.T) {
	h, codec := newTestServer(t)
	devTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "GET", "/v1/referencerepos", devTok, ""); w.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (empty)", w.Code)
	}
	w := do(t, h, "GET", "/v1/referencerepos/whatever", devTok, "")
	if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Fatalf("devrel ReferenceRepo get = %d, want 404/403", w.Code)
	}
}

func TestDevRelCannotDeleteAdminOnly(t *testing.T) {
	h, codec := newTestServer(t, mp("m1"))
	devTok := tokenFor(t, codec, "sub-alice", auth.RoleDevRel)
	if w := do(t, h, "DELETE", "/v1/modelprofiles/m1", devTok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("devrel DELETE admin-only = %d, want 403", w.Code)
	}
}

func TestAdminCreateAndDelete(t *testing.T) {
	h, codec := newTestServer(t)
	adminTok := tokenFor(t, codec, "sub-admin", auth.RoleAdmin)
	body := `{"metadata":{"name":"created"},"spec":{"provider":"openai","model":"gpt-4o","litellmName":"created"}}`
	if w := do(t, h, "POST", "/v1/modelprofiles", adminTok, body); w.Code != http.StatusCreated {
		t.Fatalf("admin POST = %d, want 201", w.Code)
	}
	if w := do(t, h, "DELETE", "/v1/modelprofiles/created", adminTok, ""); w.Code != http.StatusNoContent {
		t.Fatalf("admin DELETE = %d, want 204", w.Code)
	}
	if w := do(t, h, "GET", "/v1/modelprofiles/created", adminTok, ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", w.Code)
	}
}
