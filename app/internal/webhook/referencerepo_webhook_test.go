package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

func refRepo(routes ...contentremixerv1alpha1.LabelRoute) *contentremixerv1alpha1.ReferenceRepo {
	return &contentremixerv1alpha1.ReferenceRepo{
		ObjectMeta: metav1.ObjectMeta{Name: "crate"},
		Spec: contentremixerv1alpha1.ReferenceRepoSpec{
			Repo:        "ContentRemixer/Contentremixer",
			LabelRoutes: routes,
		},
	}
}

func route(label, out string) contentremixerv1alpha1.LabelRoute {
	return contentremixerv1alpha1.LabelRoute{Label: label, InputFormat: "auto", OutputFormat: out}
}

func mediaRoute(label, out, tmpl string) contentremixerv1alpha1.LabelRoute {
	r := route(label, out)
	r.ActorTemplate = tmpl
	return r
}

func TestValidateReferenceRepo(t *testing.T) {
	cases := []struct {
		name     string
		in       *contentremixerv1alpha1.ReferenceRepo
		wantErr  bool
		contains string
	}{
		{name: "valid single route", in: refRepo(route("recycle:blog", "blog")), wantErr: false},
		{
			name:    "valid distinct labels",
			in:      refRepo(route("recycle:blog", "blog"), route("recycle:short", "short")),
			wantErr: false,
		},
		{name: "no routes", in: refRepo(), wantErr: false},
		{
			name: "duplicate label rejected by the webhook",
			// Both routes are schema-valid (outputFormats are in the enum); only
			// the webhook's cross-item check catches the repeated label. This is
			// the message the kind negative-admission step greps for (ISI-7371).
			in:       refRepo(route("recycle:blog", "blog"), route("recycle:blog", "short")),
			wantErr:  true,
			contains: "appears more than once in spec.labelRoutes",
		},
		{
			name:     "media outputFormat pinned to the content template is rejected",
			in:       refRepo(mediaRoute("recycle:demo", "demo", contentremixerv1alpha1.ActorTemplateContent)),
			wantErr:  true,
			contains: "cannot run on the \"content\" actor template",
		},
		{
			name:    "media outputFormat on the media template is valid",
			in:      refRepo(mediaRoute("recycle:video", "video", contentremixerv1alpha1.ActorTemplateMedia)),
			wantErr: false,
		},
		{
			name:    "media outputFormat with derived template (omitted) is valid",
			in:      refRepo(route("recycle:demo", "demo")),
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateReferenceRepo(tc.in)
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v got err=%v", tc.wantErr, err)
			}
			if tc.contains != "" && (err == nil || !strings.Contains(err.Error(), tc.contains)) {
				t.Fatalf("error %v does not contain %q", err, tc.contains)
			}
		})
	}
}

func TestReferenceRepoValidatorCreateUpdateDelete(t *testing.T) {
	val := &ReferenceRepoCustomValidator{}
	ctx := context.Background()

	good := refRepo(route("recycle:blog", "blog"))
	if _, err := val.ValidateCreate(ctx, good); err != nil {
		t.Fatalf("ValidateCreate(good): %v", err)
	}
	if _, err := val.ValidateUpdate(ctx, good, good); err != nil {
		t.Fatalf("ValidateUpdate(good): %v", err)
	}
	if _, err := val.ValidateDelete(ctx, good); err != nil {
		t.Fatalf("ValidateDelete: %v", err)
	}

	bad := refRepo(route("recycle:blog", "blog"), route("recycle:blog", "short"))
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
