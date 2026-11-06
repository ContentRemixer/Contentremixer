package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func persona(subject string, refs ...contentremixerv1alpha1.PersonaReference) *contentremixerv1alpha1.DevRelPersona {
	return &contentremixerv1alpha1.DevRelPersona{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: contentremixerv1alpha1.DevRelPersonaSpec{
			Owner:      contentremixerv1alpha1.OwnerRef{Subject: subject},
			References: refs,
		},
	}
}

func TestValidateDevRelPersona(t *testing.T) {
	ok := contentremixerv1alpha1.PersonaReference{URL: "https://youtu.be/abc", Kind: "youtube"}
	art := contentremixerv1alpha1.PersonaReference{URL: "https://example.com/post", Kind: "article"}

	cases := []struct {
		name     string
		in       *contentremixerv1alpha1.DevRelPersona
		wantErr  bool
		contains string
	}{
		{name: "valid with refs", in: persona("alice", ok, art), wantErr: false},
		{name: "valid no refs", in: persona("alice"), wantErr: false},
		{name: "missing owner", in: persona(""), wantErr: true, contains: "spec.owner.subject"},
		{
			name:     "unknown kind",
			in:       persona("alice", contentremixerv1alpha1.PersonaReference{URL: "x", Kind: "tweet"}),
			wantErr:  true,
			contains: "kind",
		},
		{
			name:     "owner and kind both bad reports owner",
			in:       persona("", contentremixerv1alpha1.PersonaReference{URL: "x", Kind: "tweet"}),
			wantErr:  true,
			contains: "owner",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDevRelPersona(tc.in)
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", tc.wantErr, err)
			}
			if tc.contains != "" && (err == nil || !strings.Contains(err.Error(), tc.contains)) {
				t.Fatalf("error %v does not contain %q", err, tc.contains)
			}
		})
	}
}

func TestDevRelPersonaValidatorCreateUpdateDelete(t *testing.T) {
	val := &DevRelPersonaCustomValidator{}
	ctx := context.Background()

	good := persona("alice", contentremixerv1alpha1.PersonaReference{URL: "u", Kind: "article"})
	if _, err := val.ValidateCreate(ctx, good); err != nil {
		t.Fatalf("ValidateCreate(good): %v", err)
	}
	if _, err := val.ValidateUpdate(ctx, good, good); err != nil {
		t.Fatalf("ValidateUpdate(good): %v", err)
	}
	if _, err := val.ValidateDelete(ctx, good); err != nil {
		t.Fatalf("ValidateDelete: %v", err)
	}

	bad := persona("")
	if _, err := val.ValidateCreate(ctx, bad); err == nil {
		t.Fatal("ValidateCreate(bad): expected error")
	}
	if _, err := val.ValidateUpdate(ctx, bad, bad); err == nil {
		t.Fatal("ValidateUpdate(bad): expected error")
	}

	// Wrong type is rejected, not panicked.
	if _, err := val.ValidateCreate(ctx, &contentremixerv1alpha1.ModelProfile{}); err == nil {
		t.Fatal("ValidateCreate(wrong type): expected error")
	}
	if _, err := val.ValidateUpdate(ctx, nil, &contentremixerv1alpha1.ModelProfile{}); err == nil {
		t.Fatal("ValidateUpdate(wrong type): expected error")
	}
}
