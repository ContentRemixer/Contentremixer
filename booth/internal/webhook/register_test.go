package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

const ns = "contentremixer"

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := appv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("app scheme: %v", err)
	}
	return s
}

func admin() auth.Session { return auth.Session{Subject: "sub-admin", Role: auth.RoleAdmin} }

func refRepo() *appv1alpha1.ReferenceRepo {
	return &appv1alpha1.ReferenceRepo{
		ObjectMeta: metav1.ObjectMeta{Name: "mallard", Namespace: ns},
		Spec: appv1alpha1.ReferenceRepoSpec{
			Repo:             "ContentRemixer/Contentremixer",
			WebhookSecretRef: appv1alpha1.SecretKeyRef{Name: "refrepo-mallard", Key: "webhook-secret"},
		},
	}
}

func hmacSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "refrepo-mallard", Namespace: ns},
		Data:       map[string][]byte{"webhook-secret": []byte("deadbeef")},
	}
}

// capture records the requests the registrar makes so assertions can inspect the
// exact contract sent to GitHub.
type capture struct {
	methods []string
	paths   []string
	auth    string
	body    map[string]any
}

// newGitHub returns a fake GitHub honoring GET /hooks (returns existing) and
// POST/PATCH (captures). deliveryURL in existing controls the idempotency path.
func newGitHub(t *testing.T, cap *capture, existing []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.methods = append(cap.methods, r.Method)
		cap.paths = append(cap.paths, r.URL.Path)
		cap.auth = r.Header.Get("Authorization")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(existing)
			return
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &cap.body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 555})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newRegistrar(t *testing.T, apiURL, publicURL string, objs ...client.Object) *Registrar {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(scheme(t)).WithObjects(objs...).Build()
	return New(c, ns, "tok-webhooks", publicURL, apiURL, audit.NewSlogLogger(nil))
}

func TestRegisterCreatesHookWithIssuesContract(t *testing.T) {
	cap := &capture{}
	gh := newGitHub(t, cap, nil) // no existing hooks
	const url = "https://edge.example/github/webhook"
	reg := newRegistrar(t, gh.URL, url, refRepo(), hmacSecret())

	res, err := reg.Register(context.Background(), admin(), "mallard")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !res.Created {
		t.Fatalf("expected Created=true, got %+v", res)
	}
	if res.HookID != 555 || res.DeliveryURL != url {
		t.Fatalf("unexpected result %+v", res)
	}
	if cap.auth != "Bearer tok-webhooks" {
		t.Fatalf("auth header = %q", cap.auth)
	}
	// List then create.
	if len(cap.methods) != 2 || cap.methods[1] != http.MethodPost {
		t.Fatalf("methods = %v", cap.methods)
	}
	// Exact contract the receiver verifies: issues-only, json, our URL, secret set.
	cfg, _ := cap.body["config"].(map[string]any)
	if cfg["url"] != url || cfg["content_type"] != "json" || cfg["secret"] != "deadbeef" {
		t.Fatalf("config = %v", cfg)
	}
	events, _ := cap.body["events"].([]any)
	if len(events) != 1 || events[0] != "issues" {
		t.Fatalf("events = %v", events)
	}
}

func TestRegisterUpdatesExistingHookIdempotently(t *testing.T) {
	cap := &capture{}
	const url = "https://edge.example/github/webhook"
	existing := []map[string]any{{"id": float64(99), "config": map[string]any{"url": url}}}
	gh := newGitHub(t, cap, existing)
	reg := newRegistrar(t, gh.URL, url, refRepo(), hmacSecret())

	res, err := reg.Register(context.Background(), admin(), "mallard")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if res.Created {
		t.Fatalf("expected Created=false (update), got %+v", res)
	}
	if res.HookID != 99 {
		t.Fatalf("expected existing hook id 99, got %d", res.HookID)
	}
	if cap.methods[len(cap.methods)-1] != http.MethodPatch {
		t.Fatalf("expected PATCH, methods = %v", cap.methods)
	}
}

func TestRegisterMissingRepoIs404(t *testing.T) {
	reg := newRegistrar(t, "http://unused", "https://edge/github/webhook", hmacSecret())
	_, err := reg.Register(context.Background(), admin(), "ghost")
	if !errors.Is(err, ErrRepoNotFound) {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestRegisterMissingSecretIsConflict(t *testing.T) {
	reg := newRegistrar(t, "http://unused", "https://edge/github/webhook", refRepo())
	_, err := reg.Register(context.Background(), admin(), "mallard")
	if !errors.Is(err, ErrSecretMissing) {
		t.Fatalf("expected ErrSecretMissing, got %v", err)
	}
}
