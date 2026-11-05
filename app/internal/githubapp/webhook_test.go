package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

const (
	testSecret = "s3cr3t-hmac"
	testPAT    = "ghp_testtoken"
	testNS     = "contentremixer"
)

// recordingPoster captures the ack instead of hitting GitHub.
type recordingPoster struct {
	calls    int
	lastRepo string
	lastNum  int
	lastTok  string
	lastBody string
}

func (p *recordingPoster) PostComment(_ context.Context, repo string, num int, token, body string) error {
	p.calls++
	p.lastRepo, p.lastNum, p.lastTok, p.lastBody = repo, num, token, body
	return nil
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newTestServer(t *testing.T) (*Server, *recordingPoster) {
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
	repo.Spec.TokenRef = contentremixerv1alpha1.SecretKeyRef{Name: "github-pat", Key: "token"}

	secrets := []*corev1.Secret{
		{ObjectMeta: metav1.ObjectMeta{Name: "github-webhook", Namespace: testNS}, Data: map[string][]byte{"hmac": []byte(testSecret)}},
		{ObjectMeta: metav1.ObjectMeta{Name: "github-pat", Namespace: testNS}, Data: map[string][]byte{"token": []byte(testPAT)}},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(repo, secrets[0], secrets[1]).Build()

	poster := &recordingPoster{}
	return &Server{Client: cl, Poster: poster}, poster
}

func post(t *testing.T, s *Server, event, sig string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(string(body)))
	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	rec := httptest.NewRecorder()
	s.handle(rec, req)
	return rec
}

func TestHandleMappedLabelPostsAck(t *testing.T) {
	s, poster := newTestServer(t)
	body := []byte(`{"action":"labeled","issue":{"number":42,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)

	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if poster.calls != 1 {
		t.Fatalf("poster called %d times, want 1", poster.calls)
	}
	if poster.lastNum != 42 || poster.lastRepo != "ContentRemixer/Contentremixer" || poster.lastTok != testPAT {
		t.Errorf("ack routed wrong: repo=%q num=%d tok=%q", poster.lastRepo, poster.lastNum, poster.lastTok)
	}
	if !strings.Contains(poster.lastBody, "output `blog`") {
		t.Errorf("ack body missing resolved format:\n%s", poster.lastBody)
	}
}

func TestHandleUnmappedLabelAcksIgnored(t *testing.T) {
	s, poster := newTestServer(t)
	body := []byte(`{"action":"labeled","issue":{"number":7,"labels":[{"name":"recycle:podcast"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)

	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if poster.calls != 1 {
		t.Fatalf("poster called %d times, want 1 (ignored ack still acks)", poster.calls)
	}
	if !strings.Contains(poster.lastBody, "Ignored") || !strings.Contains(poster.lastBody, "recycle:podcast") {
		t.Errorf("ignored ack malformed:\n%s", poster.lastBody)
	}
}

func TestHandleBadSignatureRejected(t *testing.T) {
	s, poster := newTestServer(t)
	body := []byte(`{"action":"labeled","issue":{"number":1,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)

	rec := post(t, s, "issues", sign(body, "wrong-secret"), body)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if poster.calls != 0 {
		t.Errorf("poster called on bad signature (%d)", poster.calls)
	}
}

func TestHandleNoRecycleLabelsStaysQuiet(t *testing.T) {
	s, poster := newTestServer(t)
	body := []byte(`{"action":"opened","issue":{"number":9,"labels":[{"name":"bug"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)

	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if poster.calls != 0 {
		t.Errorf("acked an issue with no recycle labels (%d)", poster.calls)
	}
}

func TestHandleUnknownRepoNoAck(t *testing.T) {
	s, poster := newTestServer(t)
	body := []byte(`{"action":"labeled","issue":{"number":1,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"someone/else"}}`)

	// No matching ReferenceRepo => no secret to verify against => quiet 200.
	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if poster.calls != 0 {
		t.Errorf("acked for a repo with no ReferenceRepo (%d)", poster.calls)
	}
}

func TestHandleNonIssuesEventIgnored(t *testing.T) {
	s, poster := newTestServer(t)
	rec := post(t, s, "ping", "", []byte(`{"zen":"hi"}`))
	if rec.Code != http.StatusOK || poster.calls != 0 {
		t.Errorf("ping not ignored: code=%d calls=%d", rec.Code, poster.calls)
	}
}

func TestValidSignature(t *testing.T) {
	body := []byte("payload")
	good := sign(body, testSecret)
	if !validSignature(good, body, []byte(testSecret)) {
		t.Error("valid signature rejected")
	}
	if validSignature(good, []byte("tampered"), []byte(testSecret)) {
		t.Error("signature accepted over tampered body")
	}
	if validSignature("md5=abc", body, []byte(testSecret)) {
		t.Error("non-sha256 prefix accepted")
	}
	if validSignature(good, body, nil) {
		t.Error("empty secret accepted")
	}
}
