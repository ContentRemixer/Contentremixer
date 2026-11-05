package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// maxBodyBytes caps a delivery body (GitHub issue payloads are well under this).
const maxBodyBytes = 1 << 20 // 1 MiB

// webhookPath is the delivery path fronted by the TLS Ingress (design §7).
const webhookPath = "/github/webhook"

// Server is the GitHub delivery receiver: a plain-HTTP server (TLS is
// terminated at the Ingress, design §7) added to the manager as a Runnable. It
// acks recycle:* issues with the routes Resolve derives from the matching
// ReferenceRepo's spec.labelRoutes.
type Server struct {
	// Addr is the listen address (default ":8443").
	Addr string
	// Client reads ReferenceRepos + their Secrets (shares the manager cache).
	Client client.Client
	// Poster posts the ack comment; defaults to a real GitHubClient.
	Poster CommentPoster
}

// issuesEvent is the minimal slice of the GitHub `issues` webhook payload we
// need. Everything else in the delivery is ignored.
type issuesEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number int `json:"number"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// NeedLeaderElection reports false: the delivery receiver serves on every
// replica, independent of controller leader election.
func (s *Server) NeedLeaderElection() bool { return false }

// Start satisfies manager.Runnable: serves until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if s.Poster == nil {
		s.Poster = &GitHubClient{}
	}
	addr := s.Addr
	if addr == "" {
		addr = ":8443"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(webhookPath, s.handle)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logf.FromContext(ctx).Info("github webhook receiver listening", "addr", addr, "path", webhookPath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// handle processes one GitHub delivery. It only acts on verified `issues`
// deliveries whose action is labeled/opened; everything else returns 200 so
// GitHub does not retry.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logf.FromContext(ctx).WithName("github-webhook")

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ev := r.Header.Get("X-GitHub-Event"); ev != "issues" {
		// ping and other events: acknowledge, do nothing.
		writeStatus(w, http.StatusOK, "ignored event %q", ev)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	var ev issuesEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	if ev.Action != "labeled" && ev.Action != "opened" {
		writeStatus(w, http.StatusOK, "ignored action %q", ev.Action)
		return
	}
	if ev.Repository.FullName == "" {
		http.Error(w, "missing repository.full_name", http.StatusBadRequest)
		return
	}

	// Find the ReferenceRepo for this repo. The unverified full_name is used
	// ONLY to select which CR/secret verifies the signature — never acted on
	// before HMAC passes.
	repoCR, err := s.findReferenceRepo(ctx, ev.Repository.FullName)
	if err != nil {
		log.Error(err, "listing ReferenceRepos", "repo", ev.Repository.FullName)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if repoCR == nil {
		// No CR governs this repo: nothing to verify against, nothing to do.
		log.Info("no ReferenceRepo for delivery", "repo", ev.Repository.FullName)
		writeStatus(w, http.StatusOK, "no ReferenceRepo for %s", ev.Repository.FullName)
		return
	}

	secret, err := s.secretValue(ctx, repoCR.Namespace, repoCR.Spec.WebhookSecretRef)
	if err != nil {
		log.Error(err, "resolving webhook secret", "repo", ev.Repository.FullName)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !validSignature(r.Header.Get("X-Hub-Signature-256"), body, secret) {
		log.Info("rejected delivery: bad signature", "repo", ev.Repository.FullName)
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		return
	}

	// Verified. Resolve routes and ack.
	labels := make([]string, 0, len(ev.Issue.Labels))
	for _, l := range ev.Issue.Labels {
		labels = append(labels, l.Name)
	}
	matched, unmapped := Resolve(repoCR, labels)
	if len(matched) == 0 && len(unmapped) == 0 {
		// No recycle:* label at all (e.g. opened without labels): stay quiet.
		writeStatus(w, http.StatusOK, "no recycle labels")
		return
	}

	token, err := s.secretValue(ctx, repoCR.Namespace, repoCR.Spec.TokenRef)
	if err != nil {
		log.Error(err, "resolving PAT", "repo", ev.Repository.FullName)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.Poster.PostComment(ctx, repoCR.Spec.Repo, ev.Issue.Number, string(token), ackComment(matched, unmapped)); err != nil {
		log.Error(err, "posting ack comment", "repo", repoCR.Spec.Repo, "issue", ev.Issue.Number)
		http.Error(w, "failed to post ack", http.StatusBadGateway)
		return
	}

	// Matched routes enqueue pipeline runs — the run worker lands with the
	// analyzer→writer→checker orchestration (subtask H). This subtask (D)
	// stops at the ack per its acceptance criteria.
	log.Info("acked recycle issue", "repo", repoCR.Spec.Repo, "issue", ev.Issue.Number, "routes", len(matched), "ignored", len(unmapped))
	writeStatus(w, http.StatusOK, "acked: %d route(s), %d ignored", len(matched), len(unmapped))
}

// findReferenceRepo returns the ReferenceRepo whose spec.repo matches fullName
// (case-insensitive, GitHub repo names are), or nil if none governs it.
func (s *Server) findReferenceRepo(ctx context.Context, fullName string) (*contentremixerv1alpha1.ReferenceRepo, error) {
	var list contentremixerv1alpha1.ReferenceRepoList
	if err := s.Client.List(ctx, &list); err != nil {
		return nil, err
	}
	for i := range list.Items {
		if strings.EqualFold(list.Items[i].Spec.Repo, fullName) {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

// secretValue reads one Secret key in the CR's namespace.
func (s *Server) secretValue(ctx context.Context, ns string, ref contentremixerv1alpha1.SecretKeyRef) ([]byte, error) {
	var sec corev1.Secret
	if err := s.Client.Get(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name}, &sec); err != nil {
		return nil, err
	}
	v, ok := sec.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %q has no key %q", ref.Name, ref.Key)
	}
	return v, nil
}

// validSignature verifies GitHub's X-Hub-Signature-256 ("sha256=<hex>") over
// the raw body with the per-repo HMAC secret, in constant time.
func validSignature(header string, body, secret []byte) bool {
	const prefix = "sha256="
	if len(secret) == 0 || !strings.HasPrefix(header, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}

func writeStatus(w http.ResponseWriter, code int, format string, args ...any) {
	w.WriteHeader(code)
	fmt.Fprintf(w, format, args...)
}
