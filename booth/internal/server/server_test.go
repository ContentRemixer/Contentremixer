package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
	"github.com/ContentRemixer/Contentremixer/booth/internal/authz"
	"github.com/ContentRemixer/Contentremixer/booth/internal/session"
	"github.com/ContentRemixer/Contentremixer/booth/internal/store"
)

const ns = "content-remixer"

func newTestServer(t *testing.T, objs ...client.Object) http.Handler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	st := store.New(c, ns, audit.NewSlogLogger(nil))

	adminHash, _ := session.HashPassword("admin-pw")
	devHash, _ := session.HashPassword("dev-pw")
	auth := session.NewBcryptAuthenticator(map[string]session.User{
		"admin": {Subject: "sub-admin", Role: authz.RoleAdmin, PasswordHash: adminHash},
		"alice": {Subject: "sub-alice", Role: authz.RoleDevRel, PasswordHash: devHash},
	})
	return New(auth, st, nil).Handler()
}

func mp(name string) *v1alpha1.ModelProfile {
	return &v1alpha1.ModelProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       v1alpha1.ModelProfileSpec{Provider: "openai", Model: "gpt-4o", LiteLLMName: name},
	}
}

func do(t *testing.T, h http.Handler, method, path, user, pass, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if user != "" {
		r.SetBasicAuth(user, pass)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHealthUnauthenticated(t *testing.T) {
	h := newTestServer(t)
	if w := do(t, h, "GET", "/healthz", "", "", ""); w.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", w.Code)
	}
}

func TestUnauthenticatedRejected(t *testing.T) {
	h := newTestServer(t, mp("m1"))
	w := do(t, h, "GET", "/v1/modelprofiles", "", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no creds = %d, want 401", w.Code)
	}
	w = do(t, h, "GET", "/v1/modelprofiles", "alice", "wrong-pw", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad creds = %d, want 401", w.Code)
	}
}

func TestAdminOnlyClassEndToEnd(t *testing.T) {
	h := newTestServer(t, mp("m1"))

	// Admin: full access to the admin-only ModelProfile class.
	if w := do(t, h, "GET", "/v1/modelprofiles/m1", "admin", "admin-pw", ""); w.Code != http.StatusOK {
		t.Fatalf("admin GET = %d, want 200", w.Code)
	}

	// DevRel: 403 on read of an admin-only class (not a silent 404/empty).
	if w := do(t, h, "GET", "/v1/modelprofiles/m1", "alice", "dev-pw", ""); w.Code != http.StatusForbidden {
		t.Fatalf("devrel GET admin-only = %d, want 403", w.Code)
	}

	// DevRel: 403 on write of an admin-only class.
	body := `{"metadata":{"name":"m2"},"spec":{"provider":"openai","model":"gpt-4o","litellmName":"m2"}}`
	if w := do(t, h, "POST", "/v1/modelprofiles", "alice", "dev-pw", body); w.Code != http.StatusForbidden {
		t.Fatalf("devrel POST admin-only = %d, want 403", w.Code)
	}

	// DevRel: list of an admin-only class returns an empty set, not others' rows.
	w := do(t, h, "GET", "/v1/modelprofiles", "alice", "dev-pw", "")
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
	h := newTestServer(t, mp("m1"))
	body := `{"spec":{"provider":"anthropic","model":"claude-opus-4","litellmName":"m1"}}`
	w := do(t, h, "PUT", "/v1/modelprofiles/m1", "admin", "admin-pw", body)
	if w.Code != http.StatusOK {
		t.Fatalf("admin PUT = %d, want 200", w.Code)
	}
}

func TestBadJSONBody(t *testing.T) {
	h := newTestServer(t)
	w := do(t, h, "POST", "/v1/modelprofiles", "admin", "admin-pw", "{not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body = %d, want 400", w.Code)
	}
}

func TestReferenceRepoRouteAuthz(t *testing.T) {
	h := newTestServer(t)
	// DevRel is 403 on the admin-only ReferenceRepo class too (route wired, same gate).
	w := do(t, h, "GET", "/v1/referencerepos", "alice", "dev-pw", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200 (empty)", w.Code)
	}
	w = do(t, h, "GET", "/v1/referencerepos/whatever", "alice", "dev-pw", "")
	if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Fatalf("devrel ReferenceRepo get = %d, want 404/403", w.Code)
	}
}

func TestDevRelCannotDeleteAdminOnly(t *testing.T) {
	h := newTestServer(t, mp("m1"))
	w := do(t, h, "DELETE", "/v1/modelprofiles/m1", "alice", "dev-pw", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("devrel DELETE admin-only = %d, want 403", w.Code)
	}
}

func TestAdminCreateAndDelete(t *testing.T) {
	h := newTestServer(t)
	body := `{"metadata":{"name":"created"},"spec":{"provider":"openai","model":"gpt-4o","litellmName":"created"}}`
	if w := do(t, h, "POST", "/v1/modelprofiles", "admin", "admin-pw", body); w.Code != http.StatusCreated {
		t.Fatalf("admin POST = %d, want 201", w.Code)
	}
	if w := do(t, h, "DELETE", "/v1/modelprofiles/created", "admin", "admin-pw", ""); w.Code != http.StatusNoContent {
		t.Fatalf("admin DELETE = %d, want 204", w.Code)
	}
	if w := do(t, h, "GET", "/v1/modelprofiles/created", "admin", "admin-pw", ""); w.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", w.Code)
	}
}
