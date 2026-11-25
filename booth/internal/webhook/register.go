// Package webhook is the server-side path that registers a GitHub `issues`
// webhook on a ReferenceRepo's GitHub repo (ISI-7776 W3, ISI-7785). Today this
// is a manual owner step (deploy/README.md fallback): the default ProxOps token
// lacks the Webhooks permission, so registration needs a dedicated
// DevOps-provisioned token with that permission.
//
// When that token AND the public delivery URL (the W4 edge) are configured,
// Booth exposes POST /v1/referencerepos/{name}/webhook. The handler resolves the
// repo's spec.repo ("owner/name") and the HMAC the controller verifies
// (spec.webhookSecretRef → the Secret W2 wrote), then creates — or idempotently
// updates — the repo webhook with exactly the contract
// app/internal/githubapp/webhook.go verifies: the public /github/webhook URL,
// content_type json, the HMAC secret, and events = Issues only.
//
// booth is a separate module and cannot import app/internal/pipeline/pr or
// app/internal/githubapp, so the tiny GitHub REST client here is self-contained —
// but mirrors the discipline of booth/internal/approve (API-version header,
// "already present = success").
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

const (
	defaultGitHubAPI = "https://api.github.com"
	githubAPIVersion = "2022-11-28"
)

// ErrRepoNotFound means no ReferenceRepo CR of the requested name exists → 404.
var ErrRepoNotFound = errors.New("webhook: reference repo not found")

// ErrSecretMissing means the repo's webhookSecretRef points at a Secret/key that
// does not exist yet (W2 secret-write must run first) → 409.
var ErrSecretMissing = errors.New("webhook: webhook HMAC secret not found — write secrets first")

// Result is returned to the handler (and surfaced in the UI): which repo was
// registered, the GitHub hook id, the delivery URL set, and whether the hook was
// freshly created (true) or an existing one updated (false, e.g. secret rotation).
type Result struct {
	Repo        string `json:"repo"`
	HookID      int64  `json:"hookId"`
	DeliveryURL string `json:"deliveryUrl"`
	Created     bool   `json:"created"`
}

// Registrar reads the ReferenceRepo CR + its HMAC Secret through the Booth
// ServiceAccount's client (the one the gating stores share) and drives the
// GitHub repo-hooks REST API with a dedicated Webhooks-permission token. Each
// registration is audited (§6.4); Admin-gating is enforced at the HTTP boundary
// (requireAdmin), exactly like the W2 secret-write path.
type Registrar struct {
	client    client.Client
	namespace string
	// token is the DevOps-provisioned GitHub token WITH the Webhooks permission
	// (distinct from the per-repo PAT, which intentionally lacks it).
	token string
	// publicURL is the full public delivery URL (the W4 edge), e.g.
	// https://<edge>/github/webhook. Injected via config — never committed, as
	// the public repo must not carry the edge hostname.
	publicURL string
	// apiURL overrides the GitHub REST base for tests / GitHub Enterprise.
	apiURL  string
	http    *http.Client
	auditor audit.Logger
}

// New builds a Registrar. token and publicURL must be non-empty for the caller
// to mount the route — an unconfigured Registrar should not be wired (the manual
// deploy/README fallback covers repos we do not own / before DevOps provisions
// the token).
func New(c client.Client, namespace, token, publicURL, apiURL string, auditor audit.Logger) *Registrar {
	return &Registrar{
		client:    c,
		namespace: namespace,
		token:     token,
		publicURL: publicURL,
		apiURL:    apiURL,
		http:      &http.Client{Timeout: 30 * time.Second},
		auditor:   auditor,
	}
}

// Register creates or idempotently updates the GitHub `issues` webhook for the
// named ReferenceRepo. It never echoes the HMAC back — only the resulting hook
// id / delivery URL.
func (r *Registrar) Register(ctx context.Context, sess auth.Session, repoName string) (Result, error) {
	var cr appv1alpha1.ReferenceRepo
	if err := r.client.Get(ctx, client.ObjectKey{Namespace: r.namespace, Name: repoName}, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return Result{}, ErrRepoNotFound
		}
		return Result{}, err
	}
	fullName := cr.Spec.Repo

	hmacSecret, err := r.resolveHMAC(ctx, cr.Spec.WebhookSecretRef)
	if err != nil {
		r.audit(ctx, sess, fullName, false)
		return Result{}, err
	}

	res, err := r.registerHook(ctx, fullName, hmacSecret)
	if err != nil {
		r.audit(ctx, sess, fullName, false)
		return Result{}, err
	}
	r.audit(ctx, sess, fullName, true)
	return res, nil
}

// resolveHMAC reads the HMAC value the controller verifies against. Checks Data
// (real apiserver) and StringData (fake test client), matching the W2 writer.
func (r *Registrar) resolveHMAC(ctx context.Context, ref appv1alpha1.SecretKeyRef) (string, error) {
	var sec corev1.Secret
	if err := r.client.Get(ctx, client.ObjectKey{Namespace: r.namespace, Name: ref.Name}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return "", ErrSecretMissing
		}
		return "", err
	}
	if v, ok := sec.Data[ref.Key]; ok && len(v) > 0 {
		return string(v), nil
	}
	if v, ok := sec.StringData[ref.Key]; ok && v != "" {
		return v, nil
	}
	return "", ErrSecretMissing
}

// ghHook is the subset of the GitHub repo-hook object we read.
type ghHook struct {
	ID     int64 `json:"id"`
	Config struct {
		URL string `json:"url"`
	} `json:"config"`
}

// hookConfig is the webhook contract app/internal/githubapp verifies. secret is
// the per-repo HMAC; insecure_ssl stays "0" (TLS terminated at the W4 edge).
func (r *Registrar) hookBody(secret string) map[string]any {
	return map[string]any{
		"name":   "web",
		"active": true,
		"events": []string{"issues"},
		"config": map[string]string{
			"url":          r.publicURL,
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": "0",
		},
	}
}

// registerHook lists the repo's hooks, and if one already targets our delivery
// URL, PATCHes it (so a secret rotation re-applies and events stay Issues-only);
// otherwise POSTs a fresh hook. This makes repeated "Install webhook" clicks
// idempotent instead of hitting GitHub's 422 "hook already exists".
func (r *Registrar) registerHook(ctx context.Context, fullName, secret string) (Result, error) {
	existing, err := r.findHook(ctx, fullName)
	if err != nil {
		return Result{}, err
	}
	body := r.hookBody(secret)
	if existing != nil {
		if err := r.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/hooks/%d", fullName, existing.ID), body, nil); err != nil {
			return Result{}, err
		}
		return Result{Repo: fullName, HookID: existing.ID, DeliveryURL: r.publicURL, Created: false}, nil
	}
	var created ghHook
	if err := r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/hooks", fullName), body, &created); err != nil {
		return Result{}, err
	}
	return Result{Repo: fullName, HookID: created.ID, DeliveryURL: r.publicURL, Created: true}, nil
}

// findHook returns the existing hook whose config.url matches our delivery URL,
// or nil when none does.
func (r *Registrar) findHook(ctx context.Context, fullName string) (*ghHook, error) {
	var hooks []ghHook
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/hooks?per_page=100", fullName), nil, &hooks); err != nil {
		return nil, err
	}
	for i := range hooks {
		if strings.EqualFold(hooks[i].Config.URL, r.publicURL) {
			return &hooks[i], nil
		}
	}
	return nil, nil
}

// do performs one GitHub REST call. out, when non-nil, is JSON-decoded from a 2xx
// body. Any non-2xx is an error carrying a truncated body for diagnosis.
func (r *Registrar) do(ctx context.Context, method, path string, in any, out any) error {
	var rdr io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.base()+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: %s %s: %s: %s", method, path, resp.Status, truncate(data))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("webhook: decoding %s %s: %w", method, path, err)
		}
	}
	return nil
}

func (r *Registrar) base() string {
	if strings.TrimSpace(r.apiURL) != "" {
		return strings.TrimRight(r.apiURL, "/")
	}
	return defaultGitHubAPI
}

func (r *Registrar) audit(ctx context.Context, sess auth.Session, fullName string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	r.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         "register-webhook",
		ResourceKind: "ReferenceRepo",
		ResourceName: fullName,
		Decision:     decision,
	})
}

// truncate bounds an error-embedded GitHub response body so logs stay readable.
func truncate(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
