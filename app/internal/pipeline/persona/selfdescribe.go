package persona

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
	"github.com/ContentRemixer/Contentremixer/app/internal/rag/retrieve"
)

// DefaultSampleK is how many corpus chunks the self-describer samples to infer
// voice when a persona does not specify otherwise.
const DefaultSampleK = 12

// MaxSelfDescriptionLen bounds the authored self-description so a runaway model
// response cannot bloat the CR spec (the field is a concise voice anchor, §5.3).
const MaxSelfDescriptionLen = 1200

// SelfDescriber authors or refines DevRelPersona.spec.selfDescription from the
// ingested corpus (design §5 / CRD contract: "P2-D may PATCH selfDescription,
// the refine loop"). It samples the persona's owner-scoped voice chunks, asks
// the writer's model to distil a concise voice/positioning statement, and
// (Refine) PATCHes it back onto the CR.
//
// Draft is pure (retrieve + generate, no write) so it is unit-testable and lets
// a caller review before applying; Refine composes Draft with the PATCH.
type SelfDescriber struct {
	// Retriever samples the persona corpus (same owner-scoped query as the
	// conditioner). Required.
	Retriever *retrieve.Retriever
	// Generate runs the model call that distils the voice. Reuses the writer's
	// Generator seam so self-description and conditioning share one gateway.
	// Required.
	Generate writer.Generator
	// Client PATCHes spec.selfDescription in Refine. Required for Refine only.
	Client client.Client
	// Namespace is where the DevRelPersona CR lives (Refine). Required for
	// Refine only.
	Namespace string
	// SampleK is how many corpus chunks to sample; <= 0 uses DefaultSampleK.
	SampleK int
	// SeedLimit caps the retrieval seed; <= 0 uses DefaultSeedLimit.
	SeedLimit int
}

// Draft samples the persona's corpus and returns a model-authored voice
// self-description. It does NOT write the CR. currentSelfDescription (may be
// empty) seeds retrieval and primes the model to refine rather than replace an
// existing voice. An empty corpus (no matches) yields an error — there is
// nothing to describe — so callers do not overwrite a human-authored
// description with an empty-corpus guess.
func (d *SelfDescriber) Draft(ctx context.Context, ownerSubject, personaName, currentSelfDescription string) (string, error) {
	if d == nil || d.Retriever == nil || d.Generate == nil {
		return "", fmt.Errorf("persona: self-describer not fully wired (retriever, generate)")
	}
	owner := strings.TrimSpace(ownerSubject)
	name := strings.TrimSpace(personaName)
	if owner == "" || name == "" {
		return "", fmt.Errorf("persona: self-describe needs owner subject and persona name")
	}

	seed := strings.TrimSpace(currentSelfDescription)
	if seed == "" {
		// No prior description to refine around: seed with the persona name so
		// retrieval returns a representative slice of the corpus.
		seed = name
	}
	matches, err := d.Retriever.Retrieve(ctx, retrieve.Params{
		OwnerSubject: owner,
		PersonaName:  name,
		Seed:         truncateRunes(seed, d.seedLimit()),
		TopK:         d.sampleK(),
		MinScore:     0,
	})
	if err != nil {
		return "", fmt.Errorf("persona: sampling corpus for %q: %w", name, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("persona: no ingested corpus for %q/%q to describe (is it Ready?)", owner, name)
	}

	resp, err := d.Generate.Generate(ctx, writer.GenerateRequest{
		System: selfDescribeSystemPrompt(),
		Prompt: selfDescribeUserPrompt(currentSelfDescription, toExemplars(matches)),
	})
	if err != nil {
		return "", fmt.Errorf("persona: authoring self-description for %q: %w", name, err)
	}
	desc := strings.TrimSpace(resp.Content)
	if desc == "" {
		return "", fmt.Errorf("persona: model returned an empty self-description for %q", name)
	}
	return truncateRunes(desc, MaxSelfDescriptionLen), nil
}

// Refine drafts a self-description from the corpus and PATCHes it onto the CR's
// spec.selfDescription, returning the applied text. It enforces the owner match
// before writing (never edit another owner's persona). A no-op change (identical
// text) still returns the text without an error.
func (d *SelfDescriber) Refine(ctx context.Context, ownerSubject, personaName string) (string, error) {
	if d.Client == nil {
		return "", fmt.Errorf("persona: self-describer has no client for Refine")
	}
	if strings.TrimSpace(d.Namespace) == "" {
		return "", fmt.Errorf("persona: self-describer has no namespace for Refine")
	}

	var cr contentremixerv1alpha1.DevRelPersona
	if err := d.Client.Get(ctx, client.ObjectKey{Namespace: d.Namespace, Name: personaName}, &cr); err != nil {
		return "", fmt.Errorf("persona: get devrelpersona %s/%s: %w", d.Namespace, personaName, err)
	}
	if got := strings.TrimSpace(cr.Spec.Owner.Subject); got != strings.TrimSpace(ownerSubject) {
		return "", fmt.Errorf("persona %q owner %q does not match %q (refusing cross-owner refine)", personaName, got, ownerSubject)
	}

	desc, err := d.Draft(ctx, ownerSubject, personaName, cr.Spec.SelfDescription)
	if err != nil {
		return "", err
	}
	if desc == strings.TrimSpace(cr.Spec.SelfDescription) {
		return desc, nil // already current — no PATCH needed
	}

	patch := client.MergeFrom(cr.DeepCopy())
	cr.Spec.SelfDescription = desc
	if err := d.Client.Patch(ctx, &cr, patch); err != nil {
		return "", fmt.Errorf("persona: patching self-description on %s/%s: %w", d.Namespace, personaName, err)
	}
	return desc, nil
}

func (d *SelfDescriber) sampleK() int {
	if d.SampleK > 0 {
		return d.SampleK
	}
	return DefaultSampleK
}

func (d *SelfDescriber) seedLimit() int {
	if d.SeedLimit > 0 {
		return d.SeedLimit
	}
	return DefaultSeedLimit
}

// selfDescribeSystemPrompt instructs the model to distil voice, not content.
func selfDescribeSystemPrompt() string {
	var b strings.Builder
	b.WriteString("You are a writing-voice analyst for the ContentRemixer platform.\n")
	b.WriteString("Given representative writings by one DevRel, write a concise self-description of THEIR voice and positioning — the kind of note that would help another writer match their tone.\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Output ONLY the self-description prose. No preamble, no heading, no bullet list, no quotes.\n")
	b.WriteString("- 2 to 4 sentences. Describe tone, register, rhythm, and recurring themes/positioning.\n")
	b.WriteString("- Describe HOW they write, not WHAT any one piece is about. Do not summarise the sample content or restate facts from it.\n")
	b.WriteString("- Write in the third person (\"Writes with…\", \"Favours…\").\n")
	return b.String()
}

// selfDescribeUserPrompt lays out the current description (if any, to refine)
// and the sampled exemplars.
func selfDescribeUserPrompt(current string, exemplars []writer.Exemplar) string {
	var b strings.Builder
	if cur := strings.TrimSpace(current); cur != "" {
		fmt.Fprintf(&b, "Current self-description (refine it; keep what still holds, correct what the samples contradict):\n%s\n\n", cur)
	}
	b.WriteString("Representative writings:\n")
	for i, e := range exemplars {
		if t := strings.TrimSpace(e.Text); t != "" {
			fmt.Fprintf(&b, "\n--- sample %d ---\n%s\n", i+1, t)
		}
	}
	b.WriteString("\nWrite the voice self-description now.\n")
	return b.String()
}
