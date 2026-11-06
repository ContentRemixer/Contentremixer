package githubapp

import (
	"strings"
	"testing"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// starterRepo mirrors the Phase-1 example ReferenceRepo (design §3).
func starterRepo() *contentremixerv1alpha1.ReferenceRepo {
	return &contentremixerv1alpha1.ReferenceRepo{
		Spec: contentremixerv1alpha1.ReferenceRepoSpec{
			Repo: "ContentRemixer/Contentremixer",
			LabelRoutes: []contentremixerv1alpha1.LabelRoute{
				{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"},
				{Label: "recycle:short", InputFormat: "auto", OutputFormat: "short"},
				{Label: "recycle:tutorial", InputFormat: "auto", OutputFormat: "tutorial"},
				{Label: "recycle:teleprompter", InputFormat: "", OutputFormat: "teleprompter"}, // empty => auto default
			},
		},
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name         string
		labels       []string
		wantMatched  []Route
		wantUnmapped []string
	}{
		{
			name:        "single mapped label",
			labels:      []string{"recycle:blog"},
			wantMatched: []Route{{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"}},
		},
		{
			name:         "non-recycle labels are dropped, not reported",
			labels:       []string{"bug", "recycle:short", "enhancement"},
			wantMatched:  []Route{{Label: "recycle:short", InputFormat: "auto", OutputFormat: "short"}},
			wantUnmapped: nil,
		},
		{
			name:         "unmapped recycle label is ignored (reported)",
			labels:       []string{"recycle:podcast"},
			wantUnmapped: []string{"recycle:podcast"},
		},
		{
			name:   "multiple routes plus one unmapped",
			labels: []string{"recycle:blog", "recycle:teleprompter", "recycle:carrier-pigeon"},
			wantMatched: []Route{
				{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"},
				{Label: "recycle:teleprompter", InputFormat: "auto", OutputFormat: "teleprompter"},
			},
			wantUnmapped: []string{"recycle:carrier-pigeon"},
		},
		{
			name:        "empty inputFormat defaults to auto",
			labels:      []string{"recycle:teleprompter"},
			wantMatched: []Route{{Label: "recycle:teleprompter", InputFormat: "auto", OutputFormat: "teleprompter"}},
		},
		{
			name:        "duplicate label routes once",
			labels:      []string{"recycle:blog", "recycle:blog"},
			wantMatched: []Route{{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"}},
		},
		{
			name:   "no recycle labels at all",
			labels: []string{"bug", "question"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, unmapped := Resolve(starterRepo(), tc.labels)
			if !routesEqual(matched, tc.wantMatched) {
				t.Errorf("matched = %+v, want %+v", matched, tc.wantMatched)
			}
			if strings.Join(unmapped, ",") != strings.Join(tc.wantUnmapped, ",") {
				t.Errorf("unmapped = %v, want %v", unmapped, tc.wantUnmapped)
			}
		})
	}
}

func TestAckComment(t *testing.T) {
	matched := []Route{
		{Label: "recycle:blog", InputFormat: "auto", OutputFormat: "blog"},
	}
	unmapped := []string{"recycle:podcast"}

	body := ackComment(matched, unmapped)

	// Resolved pair is echoed (acceptance: ack shows resolved input/output).
	for _, want := range []string{"recycle:blog", "input `auto`", "output `blog`"} {
		if !strings.Contains(body, want) {
			t.Errorf("ack missing %q\n---\n%s", want, body)
		}
	}
	// Unmapped label is named under an "Ignored" section (acceptance).
	if !strings.Contains(body, "Ignored") || !strings.Contains(body, "recycle:podcast") {
		t.Errorf("ack missing ignored section for unmapped label\n---\n%s", body)
	}

	// All-unmapped ack states nothing will run.
	only := ackComment(nil, []string{"recycle:podcast"})
	if !strings.Contains(only, "nothing will run") {
		t.Errorf("all-unmapped ack should say nothing will run\n---\n%s", only)
	}
}

func routesEqual(a, b []Route) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
