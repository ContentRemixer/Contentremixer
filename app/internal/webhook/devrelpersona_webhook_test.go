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

func personaWithTargets(subject string, targets ...contentremixerv1alpha1.ContentFormatTarget) *contentremixerv1alpha1.DevRelPersona {
	p := persona(subject)
	p.Spec.ContentFormatTargets = targets
	return p
}

func personaWithKinds(subject string, primary contentremixerv1alpha1.PrimaryContentKind, kinds ...contentremixerv1alpha1.PrimaryContentKind) *contentremixerv1alpha1.DevRelPersona {
	p := persona(subject)
	p.Spec.PrimaryContentKind = primary
	p.Spec.ContentKinds = kinds
	return p
}

func TestValidateDevRelPersona(t *testing.T) {
	ok := contentremixerv1alpha1.PersonaReference{URL: "https://youtu.be/abc", Kind: "youtube"}
	art := contentremixerv1alpha1.PersonaReference{URL: "https://example.com/post", Kind: "article"}

	blogTarget := contentremixerv1alpha1.ContentFormatTarget{ContentType: contentremixerv1alpha1.ContentFormatBlog, LengthSpec: "~1500 words"}
	shortTarget := contentremixerv1alpha1.ContentFormatTarget{ContentType: contentremixerv1alpha1.ContentFormatYouTubeShort, LengthSpec: "≤3min"}

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
		{name: "valid with format targets", in: personaWithTargets("alice", blogTarget, shortTarget), wantErr: false},
		{
			name:     "unknown content format type",
			in:       personaWithTargets("alice", contentremixerv1alpha1.ContentFormatTarget{ContentType: "tiktok", LengthSpec: "≤1min"}),
			wantErr:  true,
			contains: "contentFormatTargets",
		},
		{
			name:    "valid contentKinds set with primary in set",
			in:      personaWithKinds("alice", contentremixerv1alpha1.PrimaryContentBlog, contentremixerv1alpha1.PrimaryContentBlog, contentremixerv1alpha1.PrimaryContentVideo),
			wantErr: false,
		},
		{
			name:    "empty contentKinds with primary is backward compatible",
			in:      personaWithKinds("alice", contentremixerv1alpha1.PrimaryContentBlog),
			wantErr: false,
		},
		{
			name:    "contentKinds set, primary unset is allowed",
			in:      personaWithKinds("alice", "", contentremixerv1alpha1.PrimaryContentBlog),
			wantErr: false,
		},
		{
			name:     "unknown contentKinds item",
			in:       personaWithKinds("alice", contentremixerv1alpha1.PrimaryContentBlog, contentremixerv1alpha1.PrimaryContentBlog, "podcast"),
			wantErr:  true,
			contains: "contentKinds",
		},
		{
			name:     "primary not in contentKinds set",
			in:       personaWithKinds("alice", contentremixerv1alpha1.PrimaryContentTalk, contentremixerv1alpha1.PrimaryContentBlog, contentremixerv1alpha1.PrimaryContentVideo),
			wantErr:  true,
			contains: "primaryContentKind",
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
