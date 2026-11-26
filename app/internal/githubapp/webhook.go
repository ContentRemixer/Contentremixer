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
	"github.com/ContentRemixer/Contentremixer/app/internal/githubapp/appauth"
)

// InstallationTokenSource mints a short-lived GitHub App installation token for
// one installation. Implemented by *appauth.Minter; nil when the operator runs
// PAT-only (no github-app Secret). Kept as an interface so the handler is
// testable without RSA signing.
type InstallationTokenSource interface {
	InstallationToken(ctx context.Context, installationID int64) (string, error)
}

// maxBodyBytes caps a delivery body (GitHub issue payloads are well under this).
const maxBodyBytes = 1 << 20 // 1 MiB

// webhookPath is the delivery path fronted by the TLS Ingress (design §7).
const webhookPath = "/github/webhook"

// Server is the GitHub delivery receiver: a plain-HTTP server (TLS is
// terminated at the Ingress, design §7) added to the manager as a Runnable. It
// acks recycle:* issues with the routes Resolve derives from the matching
// ReferenceRepo's spec.labelRoutes, and enqueues matched routes for the
// pipeline run worker when Runner is wired (subtask H).
type Server struct {
	// Addr is the listen address (default ":8443").
	Addr string
	// Client reads ReferenceRepos + their Secrets (shares the manager cache).
	Client client.Client
	// Poster posts the ack comment; defaults to a real GitHubClient.
	Poster CommentPoster
	// Runner, when non-nil, executes matched routes end-to-end off the
	// delivery path (analyzer → writer → checker → PR assembly, design §4).
	// When nil the receiver stays ack-only (pre-H behavior).
	Runner RouteRunner
	// AppTokens, when non-nil, mints GitHub App installation tokens for
	// ReferenceRepos that omit spec.tokenRef (ISI-7836). When a repo has a
	// tokenRef the PAT still wins; AppTokens is the no-PAT fallback.
	AppTokens InstallationTokenSource

	// runs is the enqueue built from Runner in Start; tests may pre-set it.
	runs *runQueue
}

// issuesEvent is the minimal slice of the GitHub `issues` webhook payload we
// need. Everything else in the delivery is ignored.
type issuesEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	} `json:"issue"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	// Installation is present on every delivery from a GitHub App (ISI-7836);
	// its id selects which installation token to mint for repos with no PAT.
	Installation struct {
		ID json.RawMessage `json:"id"`
	} `json:"installation"`
}

// installationEvent is the minimal slice of the `installation` /
// `installation_repositories` payloads (ISI-7895). The App delivers one of
// these when it is installed/uninstalled on a repo; we record install state on
// the governing ReferenceRepo so the UI can show "App installed ✓" and the
// no-PAT write path knows the installation id.
type installationEvent struct {
	Action       string `json:"action"`
	Installation struct {
		ID json.RawMessage `json:"id"`
	} `json:"installation"`
	// Repositories carries the full repo set on an `installation` delivery
	// (created/deleted/suspend/unsuspend).
	Repositories []repoRef `json:"repositories"`
	// RepositoriesAdded / RepositoriesRemoved carry the delta on an
	// `installation_repositories` delivery (added/removed).
	RepositoriesAdded   []repoRef `json:"repositories_added"`
	RepositoriesRemoved []repoRef `json:"repositories_removed"`
}

type repoRef struct {
	FullName string `json:"full_name"`
}

// repoState pairs a repo full_name with whether the delivery leaves the App
// installed on it.
type repoState struct {
	fullName  string
	installed bool
}

// repoStates flattens a delivery into per-repo install states. An
// `installation` delivery sets every listed repo to installed=true unless the
// action is deleted/suspend; an `installation_repositories` delivery marks
// added repos installed and removed repos uninstalled (both arrays may be
// present on one delivery).
func (ev *installationEvent) repoStates(event string) []repoState {
	var out []repoState
	switch event {
	case "installation":
		installed := ev.Action != "deleted" && ev.Action != "suspend"
		for _, r := range ev.Repositories {
			out = append(out, repoState{fullName: r.FullName, installed: installed})
		}
	case "installation_repositories":
		for _, r := range ev.RepositoriesAdded {
			out = append(out, repoState{fullName: r.FullName, installed: true})
		}
		for _, r := range ev.RepositoriesRemoved {
			out = append(out, repoState{fullName: r.FullName, installed: false})
		}
	}
	return out
}

// NeedLeaderElection reports false: the delivery receiver serves on every
// replica, independent of controller leader election.
func (s *Server) NeedLeaderElection() bool { return false }

// Start satisfies manager.Runnable: serves until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if s.Poster == nil {
		s.Poster = &GitHubClient{}
	}
	if s.Runner != nil && s.runs == nil {
		s.runs = newRunQueue(s.Runner)
	}
	if s.runs != nil {
		s.runs.start(ctx)
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

// handle is the delivery dispatcher: it reads the raw body once (the HMAC is
// verified over these exact bytes downstream) and routes by X-GitHub-Event.
// `issues` drives the recycle pipeline; `installation` /
// `installation_repositories` record App-install state on the ReferenceRepo
// (ISI-7895); everything else returns 200 so GitHub does not retry.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	switch event {
	case "issues":
		s.handleIssues(ctx, w, r, body)
	case "installation", "installation_repositories":
		s.handleInstallation(ctx, w, r, body, event)
	default:
		// ping and other events: acknowledge, do nothing.
		writeStatus(w, http.StatusOK, "ignored event %q", event)
	}
}

// handleIssues processes one `issues` delivery: it acts only on verified
// labeled/opened actions, acks the matching ReferenceRepo's resolved routes,
// and enqueues pipeline runs.
func (s *Server) handleIssues(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte) {
	log := logf.FromContext(ctx).WithName("github-webhook")

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

	token, err := s.resolveToken(ctx, repoCR, &ev)
	if err != nil {
		log.Error(err, "resolving write credential", "repo", ev.Repository.FullName)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := s.Poster.PostComment(ctx, repoCR.Spec.Repo, ev.Issue.Number, token, ackComment(matched, unmapped)); err != nil {
		log.Error(err, "posting ack comment", "repo", repoCR.Spec.Repo, "issue", ev.Issue.Number)
		http.Error(w, "failed to post ack", http.StatusBadGateway)
		return
	}

	// Matched routes enqueue pipeline runs (design §4): the worker chains
	// analyzer → writer → checker → PR assembly (subtasks E–H). The ack
	// above already told the issue what will run.
	if s.runs != nil {
		sourcePath, notes := issueFormBody(ev.Issue.Body)
		for _, route := range matched {
			req := RouteRequest{
				Repo:        repoCR.Spec.Repo,
				IssueNumber: ev.Issue.Number,
				Token:       token,
				Route:       route,
				SourcePath:  sourcePath,
				Notes:       notes,
				PRTarget:    repoCR.Spec.PRTarget,
			}
			if !s.runs.enqueue(req) {
				// Queue full: answer 503 so GitHub redelivers instead of
				// dropping the run.
				log.Info("run queue full; asking GitHub to redeliver", "repo", req.Repo, "issue", req.IssueNumber, "label", req.Route.Label)
				http.Error(w, "run queue full, retry later", http.StatusServiceUnavailable)
				return
			}
		}
	}

	log.Info("acked recycle issue", "repo", repoCR.Spec.Repo, "issue", ev.Issue.Number, "routes", len(matched), "ignored", len(unmapped))
	writeStatus(w, http.StatusOK, "acked: %d route(s), %d ignored", len(matched), len(unmapped))
}

// handleInstallation records App-install state (ISI-7895) from an
// `installation` / `installation_repositories` delivery onto every governing
// ReferenceRepo. The unverified repo names only select which CR/secret verifies
// the signature; status is written only after the per-repo HMAC passes (the
// App's webhook secret is set to the same value as each repo's
// webhookSecretRef, so the existing per-repo secret verifies App-level
// deliveries too). Always 200s unless a matched repo's signature fails, so a
// forged event cannot flip install state but an org-wide install on repos we
// do not govern is silently ignored.
func (s *Server) handleInstallation(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, event string) {
	log := logf.FromContext(ctx).WithName("github-webhook")

	var ev installationEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	installationID := appauth.ParseInstallationID(ev.Installation.ID)
	states := ev.repoStates(event)
	if len(states) == 0 {
		writeStatus(w, http.StatusOK, "no repositories in %s delivery", event)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	var matched, verified, updated int
	for _, st := range states {
		repoCR, err := s.findReferenceRepo(ctx, st.fullName)
		if err != nil {
			log.Error(err, "listing ReferenceRepos", "repo", st.fullName)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if repoCR == nil {
			continue // no CR governs this repo: nothing to record.
		}
		matched++

		secret, err := s.secretValue(ctx, repoCR.Namespace, repoCR.Spec.WebhookSecretRef)
		if err != nil {
			log.Error(err, "resolving webhook secret", "repo", st.fullName)
			continue
		}
		if !validSignature(sig, body, secret) {
			log.Info("rejected installation delivery: bad signature", "repo", st.fullName)
			continue
		}
		verified++

		wantID := int64(0)
		if st.installed {
			wantID = installationID
		}
		if repoCR.Status.AppInstalled == st.installed && repoCR.Status.InstallationID == wantID {
			continue // already in the desired state: no-op patch avoided.
		}
		patch := client.MergeFrom(repoCR.DeepCopy())
		repoCR.Status.AppInstalled = st.installed
		repoCR.Status.InstallationID = wantID
		if err := s.Client.Status().Patch(ctx, repoCR, patch); err != nil {
			log.Error(err, "patching ReferenceRepo install status", "repo", st.fullName)
			http.Error(w, "failed to record install state", http.StatusInternalServerError)
			return
		}
		updated++
		log.Info("recorded App install state", "repo", repoCR.Spec.Repo, "installed", st.installed, "installationID", wantID)
	}

	// At least one governed repo was in the delivery but none verified: the
	// secret is wrong or the delivery is forged. Answer 401 so GitHub surfaces
	// the failure instead of silently dropping a real install.
	if matched > 0 && verified == 0 {
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		return
	}
	writeStatus(w, http.StatusOK, "installation %s: %d matched, %d updated", event, matched, updated)
}

// resolveToken returns the write credential for a delivery. A ReferenceRepo
// with spec.tokenRef uses that PAT (unchanged legacy path); one without it
// falls back to a GitHub App installation token minted from the delivery's
// installation.id (ISI-7836). The returned string is a Bearer credential the
// REST calls downstream (ack comment, PR assembly) accept identically.
func (s *Server) resolveToken(ctx context.Context, repoCR *contentremixerv1alpha1.ReferenceRepo, ev *issuesEvent) (string, error) {
	if repoCR.Spec.TokenRef != nil {
		tok, err := s.secretValue(ctx, repoCR.Namespace, *repoCR.Spec.TokenRef)
		if err != nil {
			return "", err
		}
		return string(tok), nil
	}
	if s.AppTokens == nil {
		return "", fmt.Errorf("ReferenceRepo %q has no spec.tokenRef and no GitHub App is configured", repoCR.Spec.Repo)
	}
	installationID := appauth.ParseInstallationID(ev.Installation.ID)
	if installationID == 0 {
		return "", fmt.Errorf("delivery for %q carries no installation.id; GitHub App not installed on this repo?", repoCR.Spec.Repo)
	}
	return s.AppTokens.InstallationToken(ctx, installationID)
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
	_, _ = fmt.Fprintf(w, format, args...)
}
