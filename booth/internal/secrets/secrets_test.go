package secrets

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/booth/auth"
	"github.com/ContentRemixer/Contentremixer/booth/internal/audit"
)

const ns = "contentremixer"

func newWriter(t *testing.T, objs ...client.Object) (*Writer, client.Client) {
	t.Helper()
	scheme := clientgoscheme.Scheme
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return New(c, ns, audit.NewSlogLogger(nil)), c
}

func admin() auth.Session { return auth.Session{Subject: "sub-admin", Role: auth.RoleAdmin} }

// storedValue reads a key from the persisted Secret. The fake client leaves
// values in StringData (a real apiserver would move them into base64 Data), so
// mirror the writer's existingValue and check both.
func storedValue(t *testing.T, c client.Client, name, key string) string {
	t.Helper()
	sec := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, sec); err != nil {
		t.Fatalf("get secret %s: %v", name, err)
	}
	if v, ok := sec.Data[key]; ok {
		return string(v)
	}
	return sec.StringData[key]
}

// secretLabels returns the persisted Secret's labels.
func secretLabels(t *testing.T, c client.Client, name string) map[string]string {
	t.Helper()
	sec := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, sec); err != nil {
		t.Fatalf("get secret %s: %v", name, err)
	}
	return sec.Labels
}

func TestWriteCreatesSecretAndAutoGeneratesHMAC(t *testing.T) {
	wr, c := newWriter(t)

	res, err := wr.Write(context.Background(), admin(), "mallard", "pat-123", "")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	wantName := "refrepo-mallard"
	if res.TokenRef.Name != wantName || res.TokenRef.Key != KeyToken {
		t.Fatalf("tokenRef = %+v, want {%s %s}", res.TokenRef, wantName, KeyToken)
	}
	if res.WebhookSecretRef.Name != wantName || res.WebhookSecretRef.Key != KeyWebhookSecret {
		t.Fatalf("webhookSecretRef = %+v", res.WebhookSecretRef)
	}

	if got := storedValue(t, c, wantName, KeyToken); got != "pat-123" {
		t.Fatalf("stored token = %q, want pat-123", got)
	}
	hmac := storedValue(t, c, wantName, KeyWebhookSecret)
	if len(hmac) != hmacBytes*2 { // hex of 32 bytes
		t.Fatalf("auto HMAC len = %d, want %d hex chars", len(hmac), hmacBytes*2)
	}
	if secretLabels(t, c, wantName)["app.kubernetes.io/managed-by"] != "booth" {
		t.Fatalf("missing managed-by label")
	}
}

func TestWriteUsesSuppliedHMAC(t *testing.T) {
	wr, c := newWriter(t)
	if _, err := wr.Write(context.Background(), admin(), "teal", "pat", "my-hmac"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := storedValue(t, c, "refrepo-teal", KeyWebhookSecret); got != "my-hmac" {
		t.Fatalf("stored HMAC = %q, want my-hmac", got)
	}
}

// Token rotation (second write, no HMAC supplied) must rotate the token but
// PRESERVE the existing HMAC — regenerating it would break W3's registered
// GitHub webhook.
func TestWritePreservesHMACOnTokenRotation(t *testing.T) {
	wr, c := newWriter(t)
	if _, err := wr.Write(context.Background(), admin(), "wigeon", "pat-v1", ""); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	first := storedValue(t, c, "refrepo-wigeon", KeyWebhookSecret)

	if _, err := wr.Write(context.Background(), admin(), "wigeon", "pat-v2", ""); err != nil {
		t.Fatalf("rotate Write: %v", err)
	}
	if got := storedValue(t, c, "refrepo-wigeon", KeyToken); got != "pat-v2" {
		t.Fatalf("token after rotation = %q, want pat-v2", got)
	}
	if got := storedValue(t, c, "refrepo-wigeon", KeyWebhookSecret); got != first {
		t.Fatalf("HMAC changed on rotation: was %q now %q", first, got)
	}
}

func TestWriteRejectsEmptyToken(t *testing.T) {
	wr, _ := newWriter(t)
	_, err := wr.Write(context.Background(), admin(), "gadwall", "", "")
	if !errors.Is(err, ErrTokenRequired) {
		t.Fatalf("err = %v, want ErrTokenRequired", err)
	}
}

// A pre-existing Secret of the derived name that Booth did not create must not be
// overwritten — the write refuses with ErrNameConflict.
func TestWriteRefusesUnmanagedSecret(t *testing.T) {
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: SecretName("pintail"), Namespace: ns},
		Data:       map[string][]byte{"someone-elses": []byte("value")},
	}
	wr, c := newWriter(t, foreign)
	_, err := wr.Write(context.Background(), admin(), "pintail", "pat", "")
	if !errors.Is(err, ErrNameConflict) {
		t.Fatalf("err = %v, want ErrNameConflict", err)
	}
	// The foreign Secret is untouched.
	if got := storedValue(t, c, SecretName("pintail"), KeyToken); got != "" {
		t.Fatalf("foreign secret was mutated: token=%q", got)
	}
}
