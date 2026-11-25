// Package secrets is the write-only server-side path for the sensitive Secrets a
// ReferenceRepo references by name+key: the GitHub PAT (spec.tokenRef) and the
// webhook HMAC (spec.webhookSecretRef). The Admin enters these once in the W1
// create form; Booth writes them into one Kubernetes Secret and hands back only
// the *references* (name+key) so the create flow can populate the CR with no
// kubectl step (ISI-7776 W2). Values are NEVER returned to the client — this
// mirrors the write-only Secret-ref handling designed for the labelRoutes editor
// (ISI-7514): the SPA may set a secret, never read one back.
package secrets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

// Key names inside the per-repo Secret and the Secret-name prefix. The webhook
// registration (W3, ISI-7785) and the controller resolve the values via the
// SecretKeyRef returned by Write, so these are the single source of the layout.
const (
	// KeyToken holds the GitHub PAT.
	KeyToken = "token"
	// KeyWebhookSecret holds the webhook HMAC used to verify X-Hub-Signature-256.
	KeyWebhookSecret = "webhook-secret"
	// NamePrefix namespaces the Secret per ReferenceRepo so one repo's rotation
	// never touches another's.
	NamePrefix = "refrepo-"
	// hmacBytes is the entropy of an auto-generated HMAC (32 bytes → 64 hex chars).
	hmacBytes = 32
)

// ErrTokenRequired means the caller omitted the PAT. A repo with no token cannot
// read the repo, ack, or open PRs, so an empty token is a 400, not a silent write.
var ErrTokenRequired = errors.New("secrets: token is required")

// Ref is the (name, key) pointer the create flow drops into spec.tokenRef /
// spec.webhookSecretRef. It serialises to the same shape as the CRD's
// v1alpha1.SecretKeyRef — deliberately, so W1 can forward it verbatim.
type Ref struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Result is what Write returns to the handler: the two references, never values.
type Result struct {
	TokenRef         Ref `json:"tokenRef"`
	WebhookSecretRef Ref `json:"webhookSecretRef"`
}

// Writer creates/updates the per-repo Secret through the Booth ServiceAccount's
// controller-runtime client (the same one the gating store uses) and audits each
// write (§6.4). Admin-gating is enforced at the HTTP boundary (requireAdmin),
// matching the Users surface — Secrets are platform-level config with no per-row
// owner, so there is no §6.3 ownership clause to evaluate.
type Writer struct {
	client    client.Client
	namespace string
	auditor   audit.Logger
}

// New builds a Writer over a namespaced client.
func New(c client.Client, namespace string, auditor audit.Logger) *Writer {
	return &Writer{client: c, namespace: namespace, auditor: auditor}
}

// SecretName is the deterministic Secret name for a ReferenceRepo CR name.
func SecretName(repoName string) string { return NamePrefix + repoName }

// Write creates or updates the per-repo Secret holding the PAT and HMAC and
// returns their references. The PAT is always (re)written — the form is the
// rotation path too. The HMAC is auto-generated only when the caller supplies
// none AND the Secret has no existing one: an already-registered webhook (W3)
// must keep verifying, so a plain token rotation never silently invalidates it.
func (wr *Writer) Write(ctx context.Context, sess auth.Session, repoName, token, webhookSecret string) (Result, error) {
	if token == "" {
		return Result{}, ErrTokenRequired
	}
	name := SecretName(repoName)

	sec := &corev1.Secret{}
	err := wr.client.Get(ctx, client.ObjectKey{Namespace: wr.namespace, Name: name}, sec)
	exists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return Result{}, err
	}

	// Resolve the HMAC value to persist. Supplied wins; otherwise reuse the one
	// already stored (a plain token rotation must not invalidate W3's registered
	// webhook); otherwise generate a fresh one. The resolved value is written
	// back explicitly every time, so preservation never relies on the apiserver's
	// StringData-over-Data merge.
	if webhookSecret == "" {
		if exists {
			webhookSecret, _ = existingValue(sec, KeyWebhookSecret)
		}
		if webhookSecret == "" {
			if webhookSecret, err = generateHMAC(); err != nil {
				return Result{}, err
			}
		}
	}

	verb := "create"
	if exists {
		verb = "update"
	}
	sec.Name, sec.Namespace, sec.Type = name, wr.namespace, corev1.SecretTypeOpaque
	sec.StringData = map[string]string{KeyToken: token, KeyWebhookSecret: webhookSecret}
	ensureLabels(sec, repoName)

	if exists {
		err = wr.client.Update(ctx, sec)
	} else {
		err = wr.client.Create(ctx, sec)
	}
	if err != nil {
		wr.audit(ctx, sess, verb, name, false)
		return Result{}, err
	}
	wr.audit(ctx, sess, verb, name, true)

	return Result{
		TokenRef:         Ref{Name: name, Key: KeyToken},
		WebhookSecretRef: Ref{Name: name, Key: KeyWebhookSecret},
	}, nil
}

// existingValue reads a stored key from a fetched Secret. A real apiserver moves
// StringData into (base64) Data and clears StringData; a test fake client leaves
// values in StringData. Checking both keeps the preserve-HMAC decision correct
// in either environment.
func existingValue(sec *corev1.Secret, key string) (string, bool) {
	if v, ok := sec.Data[key]; ok {
		return string(v), true
	}
	if v, ok := sec.StringData[key]; ok {
		return v, true
	}
	return "", false
}

// ensureLabels marks the Secret as Booth-managed and back-links the repo so W3
// and operators can find it. Never carries any secret value.
func ensureLabels(sec *corev1.Secret, repoName string) {
	if sec.Labels == nil {
		sec.Labels = map[string]string{}
	}
	sec.Labels["app.kubernetes.io/managed-by"] = "booth"
	sec.Labels["contentremixer.io/referencerepo"] = repoName
}

// generateHMAC returns a cryptographically-random hex HMAC secret.
func generateHMAC() (string, error) {
	b := make([]byte, hmacBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("secrets: generate HMAC: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (wr *Writer) audit(ctx context.Context, sess auth.Session, verb, name string, allowed bool) {
	decision := audit.DecisionDeny
	if allowed {
		decision = audit.DecisionAllow
	}
	wr.auditor.Log(ctx, audit.Entry{
		Subject:      sess.Subject,
		Role:         string(sess.Role),
		Verb:         verb,
		ResourceKind: "Secret",
		ResourceName: name,
		Decision:     decision,
	})
}
