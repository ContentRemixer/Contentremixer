package seedissues

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

const testNS = "contentremixer"

// fakeGitHub is a tiny stateful stand-in for the GitHub REST endpoints the
// seeder drives. Created issues persist so a second Seed call sees their
// markers and skips — exercising idempotency end to end.
type fakeGitHub struct {
	mu        sync.Mutex
	issues    []ghIssue
	labelPOST int
	issuePOST int
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/{owner}/{repo}/labels", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.labelPOST++
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		// page>1 returns empty — all fixtures fit on page 1.
		if r.URL.Query().Get("page") != "1" {
			_ = json.NewEncoder(w).Encode([]ghIssue{})
			return
		}
		_ = json.NewEncoder(w).Encode(f.issues)
	})
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title, Body string
			Labels      []string
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.issuePOST++
		is := ghIssue{Number: len(f.issues) + 1, Body: body.Body, HTMLURL: "https://github.test/i/" + body.Title}
		f.issues = append(f.issues, is)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(is)
	})
	return mux
}

func newSeeder(t *testing.T, srvURL string, objs ...client.Object) *Seeder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	sd := New(c, testNS, audit.NewSlogLogger(nil))
	sd.apiURL = srvURL
	return sd
}

func repoCR(withToken bool) *v1alpha1.ReferenceRepo {
	r := &v1alpha1.ReferenceRepo{
		ObjectMeta: metav1.ObjectMeta{Name: "docs", Namespace: testNS},
		Spec:       v1alpha1.ReferenceRepoSpec{Repo: "owner/docs"},
	}
	if withToken {
		r.Spec.TokenRef = &v1alpha1.SecretKeyRef{Name: "gh-token", Key: "pat"}
	}
	return r
}

func patSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gh-token", Namespace: testNS},
		Data:       map[string][]byte{"pat": []byte("ghp_test")},
	}
}

func admin() auth.Session { return auth.Session{Subject: "sub-admin", Role: auth.RoleAdmin} }

func TestSeed_CreatesThenSkips(t *testing.T) {
	gh := &fakeGitHub{}
	ts := httptest.NewServer(gh.handler())
	defer ts.Close()

	sd := newSeeder(t, ts.URL, repoCR(true), patSecret())

	res, err := sd.Seed(context.Background(), admin(), "docs")
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if len(res.Created) != len(Pack) {
		t.Fatalf("first run: created %d, want %d", len(res.Created), len(Pack))
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("first run: skipped %v, want none", res.Skipped)
	}
	// Every created issue body carries its marker so a re-run recognises it.
	if gh.issuePOST != len(Pack) {
		t.Fatalf("first run issued %d POSTs, want %d", gh.issuePOST, len(Pack))
	}

	// Second run is idempotent: nothing new created, everything skipped.
	res2, err := sd.Seed(context.Background(), admin(), "docs")
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if len(res2.Created) != 0 {
		t.Fatalf("second run: created %v, want none", res2.Created)
	}
	if len(res2.Skipped) != len(Pack) {
		t.Fatalf("second run: skipped %d, want %d", len(res2.Skipped), len(Pack))
	}
	if gh.issuePOST != len(Pack) {
		t.Fatalf("second run must create no issues: total POSTs %d, want %d", gh.issuePOST, len(Pack))
	}
}

func TestSeed_NoToken(t *testing.T) {
	sd := newSeeder(t, "http://unused", repoCR(false))
	_, err := sd.Seed(context.Background(), admin(), "docs")
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("got %v, want ErrNoToken", err)
	}
}

func TestSeed_RepoNotFound(t *testing.T) {
	sd := newSeeder(t, "http://unused")
	_, err := sd.Seed(context.Background(), admin(), "ghost")
	if !IsNotFound(err) {
		t.Fatalf("got %v, want not-found", err)
	}
}

func TestPack_Loaded(t *testing.T) {
	if len(Pack) == 0 {
		t.Fatal("embedded pack is empty")
	}
	for _, tpl := range Pack {
		if tpl.Title == "" || len(tpl.Labels) == 0 {
			t.Fatalf("template %q missing title/labels", tpl.ID)
		}
		if !strings.Contains(tpl.renderBody(), marker(tpl.ID)) {
			t.Fatalf("template %q body missing its marker", tpl.ID)
		}
	}
}
