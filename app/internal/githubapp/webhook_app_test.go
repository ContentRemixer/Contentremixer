package githubapp

import (
	"context"
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// fakeMinter records the installation id it was asked for and returns a fixed
// token, standing in for *appauth.Minter so the handler path is tested without
// RSA signing or HTTP (ISI-7836).
type fakeMinter struct {
	token  string
	err    error
	lastID int64
	calls  int
}

func (m *fakeMinter) InstallationToken(_ context.Context, id int64) (string, error) {
	m.calls++
	m.lastID = id
	return m.token, m.err
}

// newAppServer builds a receiver whose ReferenceRepo has NO tokenRef, so the
// App installation-token path is exercised.
func newAppServer(t *testing.T, minter InstallationTokenSource) (*Server, *recordingPoster) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := contentremixerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	repo := starterRepo()
	repo.ObjectMeta = metav1.ObjectMeta{Name: "crate", Namespace: testNS}
	repo.Spec.WebhookSecretRef = contentremixerv1alpha1.SecretKeyRef{Name: "github-webhook", Key: "hmac"}
	repo.Spec.TokenRef = nil // no PAT → App path

	hookSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-webhook", Namespace: testNS},
		Data:       map[string][]byte{"hmac": []byte(testSecret)},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo, hookSecret).Build()

	poster := &recordingPoster{}
	return &Server{Client: cl, Poster: poster, AppTokens: minter}, poster
}

func TestHandleAppInstallationTokenPath(t *testing.T) {
	minter := &fakeMinter{token: "ghs_installationtoken"}
	s, poster := newAppServer(t, minter)

	body := []byte(`{"action":"labeled","issue":{"number":99,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"},"installation":{"id":12345}}`)
	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if minter.calls != 1 || minter.lastID != 12345 {
		t.Fatalf("minter calls=%d lastID=%d, want 1 and 12345", minter.calls, minter.lastID)
	}
	if poster.lastTok != "ghs_installationtoken" {
		t.Errorf("ack used token %q, want the installation token", poster.lastTok)
	}
}

func TestHandleAppPathNoMinterConfigured(t *testing.T) {
	s, poster := newAppServer(t, nil) // no PAT and no App → cannot resolve a credential
	body := []byte(`{"action":"labeled","issue":{"number":1,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"},"installation":{"id":42}}`)
	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if poster.calls != 0 {
		t.Errorf("poster should not be called when no credential resolves")
	}
}

func TestHandleAppPathMissingInstallationID(t *testing.T) {
	minter := &fakeMinter{token: "ghs_x"}
	s, _ := newAppServer(t, minter)
	// Delivery with no installation object (e.g. App not installed on repo).
	body := []byte(`{"action":"labeled","issue":{"number":1,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)
	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if minter.calls != 0 {
		t.Errorf("minter should not be invoked without an installation id")
	}
}

func TestHandlePATWinsOverApp(t *testing.T) {
	// A repo WITH a tokenRef must keep using the PAT even when an App is wired.
	minter := &fakeMinter{token: "ghs_shouldnotbeused"}
	s, poster := newTestServer(t)
	s.AppTokens = minter

	body := []byte(`{"action":"labeled","issue":{"number":5,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"},"installation":{"id":999}}`)
	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if minter.calls != 0 {
		t.Errorf("minter must not be called when tokenRef is set")
	}
	if poster.lastTok != testPAT {
		t.Errorf("ack used token %q, want the PAT %q", poster.lastTok, testPAT)
	}
}
