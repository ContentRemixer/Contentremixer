// Content PR templates (ISI-7119 / plan §4 "Content PR template"). This file
// is the SINGLE SOURCE OF TRUTH for the type-aware content PR template:
//
//   - the PR-assembly step (pr.go, PRBody) renders the right template for the
//     generated content type and pre-fills the common fields it knows, and
//   - the static .github/PULL_REQUEST_TEMPLATE/<type>.md files (the GitHub
//     web-UI chooser + the installable Content-CI contract) are GENERATED from
//     exactly this code and verified byte-for-byte by TestStaticTemplatesMatch
//     — so the two can never drift.
//
// The operator opens content PRs in an EXTERNAL ReferenceRepo whose template
// files it cannot read at run time, so the template text has to live here and
// be pre-filled programmatically; the static files are the human/web copy.
//
// Type-specific fields are declared both here (typeFields) and in each content
// type's content/<type>/meta.template.yaml (pr_template.fields). The two stay
// in lock-step with the C7/C8 field-validation contract via
// TestTypeFieldsMatchMetaTemplate.
package pr

import (
	"fmt"
	"strings"
)

// Content types that have a dedicated PR template (plan §4). Any other output
// format falls back to DefaultContentType.
const (
	ContentTypeBlog       = "blog"
	ContentTypeSocial     = "social"
	ContentTypeNewsletter = "newsletter"
	DefaultContentType    = "default"
)

// field is one checklist field in a PR template. Key is the machine-readable
// key Content CI validates (and the key under pr_template.fields in the
// content type's meta.template.yaml); Label is the human checklist label; Hint
// is optional parenthetical guidance.
type field struct {
	Key   string
	Label string
	Hint  string
}

// commonFields are carried by every content type (plan §4 "Common fields").
// They are pre-filled by the PR-assembly step from the run context.
var commonFields = []field{
	{Key: "content_type", Label: "Content type"},
	{Key: "source", Label: "Source URL / original asset"},
	{Key: "target_format", Label: "Target format"},
	{Key: "slug", Label: "Slug"},
	{Key: "summary", Label: "Summary"},
	{Key: "linked_issue", Label: "Linked issue"},
}

// typeFields are the type-specific fields (plan §4). Each list is kept in
// lock-step with content/<type>/meta.template.yaml:pr_template.fields.
var typeFields = map[string][]field{
	ContentTypeBlog: {
		{Key: "title", Label: "Title"},
		{Key: "meta_description", Label: "Meta description", Hint: "140–160 characters"},
		{Key: "canonical_url", Label: "Canonical URL"},
		{Key: "tags", Label: "Tags"},
		{Key: "primary_keyword", Label: "Primary keyword"},
		{Key: "h2_outline", Label: "H2 outline confirmed"},
		{Key: "sources", Label: "Sources section present"},
	},
	ContentTypeSocial: {
		{Key: "platform", Label: "Platform"},
		{Key: "char_count", Label: "Character-count confirmed"},
		{Key: "hashtags", Label: "Hashtags"},
		{Key: "alt_text", Label: "Media alt text"},
	},
	ContentTypeNewsletter: {
		{Key: "subject", Label: "Subject line"},
		{Key: "preview", Label: "Preview text"},
		{Key: "cta", Label: "Call to action (CTA)"},
	},
}

// KnownContentTypes lists every type that has a dedicated template file,
// in the order the static files are generated.
var KnownContentTypes = []string{ContentTypeBlog, ContentTypeSocial, ContentTypeNewsletter, DefaultContentType}

// ContentType maps a generated output format to the content type whose PR
// template should be used. Output formats that do not have a dedicated
// template (e.g. "short", "tutorial", "teleprompter") fall back to the default
// template so assembly never fails for an unmapped format.
func ContentType(outputFormat string) string {
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case ContentTypeBlog:
		return ContentTypeBlog
	case ContentTypeSocial:
		return ContentTypeSocial
	case ContentTypeNewsletter:
		return ContentTypeNewsletter
	default:
		return DefaultContentType
	}
}

// TemplateQueryParam is the file name a human PR author passes as the GitHub
// `template=` query param to select this type's template from the chooser.
func TemplateQueryParam(contentType string) string {
	return ContentType(contentType) + ".md"
}

// Prefill carries the common-field values the PR-assembly step knows at open
// time. Empty fields render as a "_fill in_" placeholder (the static-file
// case) so the same renderer produces both the pre-filled bot body and the
// blank web template.
type Prefill struct {
	ContentType  string
	Source       string
	TargetFormat string
	Slug         string
	Summary      string
	LinkedIssue  string // e.g. "#42"
}

const fillIn = "_fill in_"

// typeHeading is the human title for a content type's field section.
func typeHeading(contentType string) string {
	switch contentType {
	case ContentTypeBlog:
		return "Blog"
	case ContentTypeSocial:
		return "Social"
	case ContentTypeNewsletter:
		return "Newsletter"
	default:
		return "Content"
	}
}

// commonValue resolves a common field's rendered value from the prefill,
// substituting the placeholder when empty.
func commonValue(key string, pf Prefill) string {
	var v string
	switch key {
	case "content_type":
		v = pf.ContentType
	case "source":
		v = pf.Source
	case "target_format":
		v = pf.TargetFormat
	case "slug":
		v = pf.Slug
	case "summary":
		v = pf.Summary
	case "linked_issue":
		v = pf.LinkedIssue
	}
	if strings.TrimSpace(v) == "" {
		if key == "linked_issue" {
			return "_#<issue>_"
		}
		return fillIn
	}
	return v
}

// renderTemplateBody renders everything above the Sample-clearance section:
// the provenance header, the Closes line, the pre-filled common fields, and
// the type-specific checklist. The clearance section is appended separately
// (the real block for the bot, a checklist for the static web file) so the two
// callers share this body verbatim.
func renderTemplateBody(contentType string, pf Prefill) string {
	contentType = ContentType(contentType)
	heading := typeHeading(contentType)

	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "<!-- ContentRemixer content PR template: %s -->\n", contentType)
	if contentType == DefaultContentType {
		b.WriteString("## 🎛️ Recycled content — generated by ContentRemixer\n\n")
	} else {
		_, _ = fmt.Fprintf(&b, "## 🎛️ Recycled %s — generated by ContentRemixer\n\n", contentType)
	}
	b.WriteString("Generated by **ContentRemixer** — automated content recycling. This PR adds one recycled content folder under `recycled/`.\n\n")

	linked := strings.TrimSpace(pf.LinkedIssue)
	if linked == "" {
		linked = "#<issue>"
	}
	_, _ = fmt.Fprintf(&b, "Closes %s\n\n", linked)

	b.WriteString("### Content\n")
	for _, f := range commonFields {
		val := commonValue(f.Key, pf)
		if f.Key == "content_type" && strings.TrimSpace(pf.ContentType) == "" {
			// content_type is always known from the template itself.
			val = contentType
		}
		_, _ = fmt.Fprintf(&b, "- **%s:** %s\n", f.Label, val)
	}
	b.WriteString("\n")

	if contentType == DefaultContentType {
		b.WriteString("### Type-specific fields\n")
		b.WriteString("<!-- No dedicated template for this output format. If this is a blog, social, or newsletter PR, use the matching template (append `?template=blog.md`, `social.md`, or `newsletter.md` to the PR URL). -->\n")
		b.WriteString("- [ ] Type-specific fields confirmed\n")
		return b.String()
	}

	_, _ = fmt.Fprintf(&b, "### %s fields\n", heading)
	_, _ = fmt.Fprintf(&b, "<!-- Driven by content/%s/meta.template.yaml (pr_template); kept in lock-step with Content CI field validation (C7/C8). -->\n", contentType)
	for _, f := range typeFields[contentType] {
		if f.Hint != "" {
			_, _ = fmt.Fprintf(&b, "- [ ] **%s:** (%s)\n", f.Label, f.Hint)
		} else {
			_, _ = fmt.Fprintf(&b, "- [ ] **%s:**\n", f.Label)
		}
	}
	return b.String()
}

// staticClearanceChecklist is the human-facing Sample-clearance section in the
// static .github templates. ContentRemixer replaces it with the real,
// pre-filled ClearanceBlock when it opens the PR (PRBody).
const staticClearanceChecklist = `## Sample clearance
> Provenance is filled in automatically when ContentRemixer opens the PR. Confirm each item before merge.
- [ ] Source path recorded
- [ ] Source commit SHA recorded
- [ ] Model recorded
- [ ] Input → Output recorded
- [ ] Linked recycle issue
`

// RenderStaticTemplate renders the full static .github/PULL_REQUEST_TEMPLATE
// file for a content type (blank common fields + checklist clearance). This is
// the authoritative generator for those files; the test suite regenerates and
// diffs them so they can never drift from this code.
func RenderStaticTemplate(contentType string) string {
	return renderTemplateBody(contentType, Prefill{}) + "\n" + staticClearanceChecklist
}
