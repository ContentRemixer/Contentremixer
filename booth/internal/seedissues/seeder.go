package seedissues

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

// ErrNoToken is returned when the ReferenceRepo has no spec.tokenRef PAT to
// seed with. App-installation-token seeding is not yet wired in Booth (the App
// credential lives in the app module's githubapp server, which Booth cannot
// import); add a PAT via the create/edit form to seed.
var ErrNoToken = errors.New("seedissues: reference repo has no spec.tokenRef (PAT) to seed with")

// Seeder seeds the embedded template pack into an onboarded repo. It reuses the
// Booth ServiceAccount's namespaced client to read the ReferenceRepo CR and its
// PAT Secret, then drives GitHub with that token.
type Seeder struct {
	client    client.Client
	namespace string
	auditor   audit.Logger
	templates []IssueTemplate

	// apiURL / http override the GitHub endpoint and transport for tests. Empty
	// apiURL → public GitHub.
	apiURL string
	http   *http.Client
}

// New builds a Seeder over the Booth namespaced client and the embedded pack.
func New(c client.Client, namespace string, auditor audit.Logger) *Seeder {
	return &Seeder{client: c, namespace: namespace, auditor: auditor, templates: Pack}
}

// SeededIssue reports one issue the seeder created (or would have).
type SeededIssue struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Result reports the outcome of a seed run. Skipped lists template ids that
// already existed in the repo (idempotent re-run).
type Result struct {
	Repo    string        `json:"repo"`
	Created []SeededIssue `json:"created"`
	Skipped []string      `json:"skipped"`
}

// Seed seeds the pack into the ReferenceRepo named name. It is idempotent:
// templates already present (matched by their hidden marker) are skipped.
func (s *Seeder) Seed(ctx context.Context, sess auth.Session, name string) (Result, error) {
	var repoCR contentremixerv1alpha1.ReferenceRepo
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &repoCR); err != nil {
		return Result{}, err // apierrors.IsNotFound surfaced as 404 by the handler
	}
	if repoCR.Spec.TokenRef == nil {
		return Result{}, ErrNoToken
	}
	token, err := s.secretValue(ctx, repoCR.Spec.TokenRef.Name, repoCR.Spec.TokenRef.Key)
	if err != nil {
		return Result{}, err
	}

	gh := &ghClient{token: token, apiURL: s.apiURL, http: s.http}
	repo := repoCR.Spec.Repo

	// Ensure every label the pack references exists before creating issues —
	// GitHub rejects an issue that names an unknown label.
	for _, l := range allLabels(s.templates) {
		if err := gh.ensureLabel(ctx, repo, l); err != nil {
			return Result{}, err
		}
	}

	ids := make([]string, len(s.templates))
	for i, t := range s.templates {
		ids[i] = t.ID
	}
	seeded, err := gh.seededIDs(ctx, repo, ids)
	if err != nil {
		return Result{}, err
	}

	res := Result{Repo: repo}
	for _, t := range s.templates {
		if seeded[t.ID] {
			res.Skipped = append(res.Skipped, t.ID)
			continue
		}
		labels := append([]string{TrackingLabel}, t.Labels...)
		is, err := gh.createIssue(ctx, repo, t.Title, t.renderBody(), labels)
		if err != nil {
			// Partial progress is real work — return what we created so far
			// alongside the error so a retry only fills the gap (ids already
			// created carry their marker and will be skipped).
			s.audit(ctx, sess, name, audit.DecisionDeny)
			return res, fmt.Errorf("seeding %q into %s: %w", t.ID, repo, err)
		}
		res.Created = append(res.Created, SeededIssue{ID: t.ID, Number: is.Number, URL: is.HTMLURL})
	}
	s.audit(ctx, sess, name, audit.DecisionAllow)
	return res, nil
}

func (s *Seeder) secretValue(ctx context.Context, name, key string) (string, error) {
	var sec corev1.Secret
	if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, &sec); err != nil {
		return "", err
	}
	v, ok := sec.Data[key]
	if !ok {
		return "", fmt.Errorf("secret %q has no key %q", name, key)
	}
	return string(v), nil
}

func (s *Seeder) audit(ctx context.Context, sess auth.Session, name, decision string) {
	if s.auditor == nil {
		return
	}
	s.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         "seed-issues",
		ResourceKind: "ReferenceRepo",
		ResourceName: name,
		Decision:     decision,
	})
}

// IsNotFound reports whether err is a k8s not-found (the CR name the caller
// gave does not exist), so the handler can map it to 404.
func IsNotFound(err error) bool { return apierrors.IsNotFound(err) }
