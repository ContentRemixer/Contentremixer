// ContentStyle CR style resolution (ISI-7965 W2): the primary style-guide
// source for the writer. The repo styles/<format>.md read stays as the
// transitional fallback and is removed in W5 after the CRD source is
// verified live.
package writer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// ErrNoContentStyle is the sentinel for "the CR source yielded no guide"
// (missing CR, empty guide, unwired resolver, or a read error). The writer
// treats it as the transitional repo-fallback signal, never as a failed run.
var ErrNoContentStyle = errors.New("writer: no ContentStyle guide resolved")

// ContentStyleResolver resolves the output-format style guide from
// ContentStyle CRs (ISI-7965 W2). ContentStyle is GLOBAL in scope — one set
// of guides shared across reference repos — living in one namespace (the
// operator's). Reader is the manager's cached client the operator already
// builds in app/cmd/main.go (mgr.GetClient()), mirroring the
// CRPersonaProvider wiring: reads are cache-served with no extra watches.
type ContentStyleResolver struct {
	// Reader lists ContentStyle CRs. Required.
	Reader client.Reader
	// Namespace is where the ContentStyle CRs live. Required.
	Namespace string
}

var _ StyleResolver = (*ContentStyleResolver)(nil)

// ResolveStyleGuide lists the ContentStyles in the resolver namespace and
// returns the guide whose spec.outputFormat matches the run's format. The
// validating webhook (ISI-7975 W1) enforces one CR per outputFormat per
// namespace, so at most one match exists; a multi-match (webhook bypassed
// or racing writes) resolves deterministically by name so the writer never
// errors on ambiguity. No match, an empty guide, or a read error returns an
// error wrapping ErrNoContentStyle — the transitional repo fallback signal.
func (r *ContentStyleResolver) ResolveStyleGuide(ctx context.Context, rc pipeline.RunContext, format string) (StyleGuide, error) {
	if r == nil || r.Reader == nil {
		return StyleGuide{}, fmt.Errorf("contentstyle resolver has no reader: %w", ErrNoContentStyle)
	}
	ns := strings.TrimSpace(r.Namespace)
	if ns == "" {
		return StyleGuide{}, fmt.Errorf("contentstyle resolver has no namespace: %w", ErrNoContentStyle)
	}
	var list contentremixerv1alpha1.ContentStyleList
	if err := r.Reader.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return StyleGuide{}, fmt.Errorf("%w: listing ContentStyles in %s: %w", ErrNoContentStyle, ns, err)
	}
	matches := make([]contentremixerv1alpha1.ContentStyle, 0, 1)
	for i := range list.Items {
		if strings.TrimSpace(list.Items[i].Spec.OutputFormat) == format {
			matches = append(matches, list.Items[i])
		}
	}
	if len(matches) == 0 {
		return StyleGuide{}, fmt.Errorf("%w: no ContentStyle for outputFormat %q in %s", ErrNoContentStyle, format, ns)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	cr := matches[0]
	guide := strings.TrimSpace(cr.Spec.Guide)
	if guide == "" {
		// The webhook rejects whitespace-only guides (W1); belt-and-braces:
		// an empty guide degrades to the repo fallback, never fails the run.
		return StyleGuide{}, fmt.Errorf("%w: ContentStyle %s/%s has an empty guide", ErrNoContentStyle, ns, cr.Name)
	}
	return StyleGuide{
		Guide:  guide,
		Source: "contentstyle:" + ns + "/" + cr.Name,
	}, nil
}
