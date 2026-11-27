package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func newContentStyle(name, namespace, format, guide string) *contentremixerv1alpha1.ContentStyle {
	return &contentremixerv1alpha1.ContentStyle{
		TypeMeta: metav1.TypeMeta{APIVersion: "contentremixer.io/v1alpha1", Kind: "ContentStyle"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("uid-" + name),
		},
		Spec: contentremixerv1alpha1.ContentStyleSpec{
			OutputFormat: format,
			Guide:        guide,
		},
	}
}

func contentStyleValidator(objs ...client.Object) *ContentStyleCustomValidator {
	scheme := runtime.NewScheme()
	if err := contentremixerv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return &ContentStyleCustomValidator{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build(),
	}
}

func TestContentStyleValidateCreateOK(t *testing.T) {
	v := contentStyleValidator()
	cs := newContentStyle("blog-style", "contentremixer", "blog", "# Blog guide\n\nVoice: sharp.")
	if _, err := v.ValidateCreate(context.Background(), cs); err != nil {
		t.Fatalf("expected valid create to pass, got %v", err)
	}
}

func TestContentStyleValidateCreateUnknownFormat(t *testing.T) {
	v := contentStyleValidator()
	cs := newContentStyle("podcast-style", "contentremixer", "podcast", "guide body")
	_, err := v.ValidateCreate(context.Background(), cs)
	if err == nil {
		t.Fatal("expected unknown outputFormat to be rejected")
	}
	if !strings.Contains(err.Error(), "outputFormat") {
		t.Fatalf("expected outputFormat error, got %v", err)
	}
}

func TestContentStyleValidateCreateEmptyGuide(t *testing.T) {
	v := contentStyleValidator()
	cs := newContentStyle("blog-style", "contentremixer", "blog", "   \n\t ")
	_, err := v.ValidateCreate(context.Background(), cs)
	if err == nil {
		t.Fatal("expected whitespace-only guide to be rejected")
	}
	if !strings.Contains(err.Error(), "guide") {
		t.Fatalf("expected guide error, got %v", err)
	}
}

func TestContentStyleValidateCreateDuplicateFormat(t *testing.T) {
	existing := newContentStyle("blog-style", "contentremixer", "blog", "guide body")
	v := contentStyleValidator(existing)
	dup := newContentStyle("blog-style-2", "contentremixer", "blog", "another guide")
	_, err := v.ValidateCreate(context.Background(), dup)
	if err == nil {
		t.Fatal("expected duplicate outputFormat to be rejected")
	}
	if !strings.Contains(err.Error(), "blog-style") {
		t.Fatalf("expected duplicate error to name the existing ContentStyle, got %v", err)
	}
}

func TestContentStyleValidateUpdateSelfKeepsFormat(t *testing.T) {
	existing := newContentStyle("blog-style", "contentremixer", "blog", "old guide")
	v := contentStyleValidator(existing)
	updated := newContentStyle("blog-style", "contentremixer", "blog", "new guide")
	updated.UID = existing.UID
	if _, err := v.ValidateUpdate(context.Background(), existing, updated); err != nil {
		t.Fatalf("expected self-update with same format to pass, got %v", err)
	}
}

func TestContentStyleValidateDeleteNoOp(t *testing.T) {
	v := contentStyleValidator()
	if _, err := v.ValidateDelete(context.Background(), nil); err != nil {
		t.Fatalf("expected delete to be a no-op, got %v", err)
	}
}
