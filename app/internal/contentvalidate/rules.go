package contentvalidate

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/checker"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
)

// ---------------------------------------------------------------------------
// required-assets (plan §3: "required assets present per meta.template.yaml")

// RequiredAssetsRule fails closed when an asset declared by the type's
// meta.template.yaml (files.<key>) is absent on disk. The resolved filename is
// the content folder's own meta.files.<key> when set, else the template
// default — so a folder may rename its post but must still ship one.
type RequiredAssetsRule struct{}

func (RequiredAssetsRule) ID() string { return "required-assets" }
func (RequiredAssetsRule) Description() string {
	return "every asset declared by meta.template.yaml (files.*) is present"
}

func (r RequiredAssetsRule) Check(_ context.Context, vc *ValidateContext) []Finding {
	var out []Finding
	for _, t := range vc.Targets {
		if t.Type == "" {
			continue // unknown type: asset-structure flags it; no template to check
		}
		if t.Template == nil {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityError, Target: t.Dir, File: t.MetaPath,
				Message: fmt.Sprintf("content type %q has no readable meta.template.yaml; cannot verify required assets", t.Type),
			})
			continue
		}
		if len(t.Template.FileOrder) == 0 {
			continue // type declares no file assets (e.g. podcasts/livestreams)
		}
		metaFiles, _ := t.Meta["files"].(map[string]any)
		for _, key := range t.Template.FileOrder {
			name := t.Template.Files[key]
			if metaFiles != nil {
				if override, ok := metaFiles[key].(string); ok && strings.TrimSpace(override) != "" {
					name = strings.TrimSpace(override)
				}
			}
			assetPath := path.Join(t.Dir, name)
			if !fileExists(vc.RepoRoot, assetPath) {
				out = append(out, Finding{
					Rule: r.ID(), Severity: SeverityError, Target: t.Dir, File: assetPath,
					Message: fmt.Sprintf("required asset %q (files.%s) not found", name, key),
				})
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// asset-structure (plan §3: "asset-structure validation")

// AssetStructureRule validates the meta.yaml itself: it must read and parse;
// the folder must sit under a known content type; title must be non-empty
// (error); every other template-declared field should be non-empty (warn).
type AssetStructureRule struct{}

func (AssetStructureRule) ID() string { return "asset-structure" }
func (AssetStructureRule) Description() string {
	return "meta.yaml parses and template-declared fields are populated"
}

func (r AssetStructureRule) Check(_ context.Context, vc *ValidateContext) []Finding {
	var out []Finding
	for _, t := range vc.Targets {
		if t.MetaErr != nil {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityError, Target: t.Dir, File: t.MetaPath,
				Message: fmt.Sprintf("meta.yaml is missing or invalid: %v", t.MetaErr),
			})
			continue
		}
		if t.Type == "" {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityWarning, Target: t.Dir, File: t.MetaPath,
				Message: "folder has no known content type (blog|videos|podcasts|livestreams|social|newsletter); skipping type-specific checks",
			})
			continue
		}

		// title is the one universally-required field (all templates declare
		// it, and an untitled item cannot be published).
		if isEmpty(t.Meta["title"]) {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityError, Target: t.Dir, File: t.MetaPath,
				Message: "meta.yaml: title is empty",
			})
		}

		if t.Template == nil {
			continue
		}
		for _, field := range t.Template.Fields {
			if field == "title" {
				continue // handled above as an error
			}
			if isEmpty(t.Meta[field]) {
				out = append(out, Finding{
					Rule: r.ID(), Severity: SeverityWarning, Target: t.Dir, File: t.MetaPath,
					Message: fmt.Sprintf("meta.yaml: field %q is empty (declared in meta.template.yaml)", field),
				})
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// content-quality (plan §3: reuse the ISI-7082 fail-closed checker/Mastering)

// ContentQualityRule runs the exact ISI-7082 checker over each target's body +
// meta — zero duplication of the scrub logic. Block violations (secrets, IPs,
// internal hostnames, tenant ids) map to errors (fail-closed); brand-voice
// warn violations map to warnings. The checker already redacts evidence, so
// Finding.Message is safe on a public PR.
type ContentQualityRule struct{}

func (ContentQualityRule) ID() string { return "content-quality" }
func (ContentQualityRule) Description() string {
	return "ISI-7082 fail-closed scrub (secrets/IP/hostname) + brand-voice"
}

func (r ContentQualityRule) Check(ctx context.Context, vc *ValidateContext) []Finding {
	var out []Finding
	chk := checker.New()
	for _, t := range vc.Targets {
		if t.MetaErr != nil {
			continue // asset-structure already reported the unreadable meta
		}
		mix := pipeline.Mix{
			Markdown: t.Body,
			Meta:     t.Meta,
			Title:    metaStr(t.Meta, "title"),
		}
		res, err := chk.Check(ctx, mix)
		if err != nil {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityError, Target: t.Dir, File: t.MetaPath,
				Message: fmt.Sprintf("checker error: %v", err),
			})
			continue
		}
		for _, v := range res.Violations {
			sev := SeverityWarning
			if v.Severity == pipeline.SeverityBlock {
				sev = SeverityError
			}
			file := t.MetaPath
			if strings.HasPrefix(v.Where, "markdown") && t.BodyFile != "" {
				file = t.BodyFile
			}
			out = append(out, Finding{
				Rule: r.ID(), Severity: sev, Target: t.Dir, File: file,
				Message: fmt.Sprintf("%s: %q (in %s)", v.Rule, v.Match, v.Where),
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// seo (plan §3: "SEO validation")

// SEORule applies deterministic on-page SEO checks (all advisory): a sane
// title length, a single H1 in the body, and non-empty topical tags.
type SEORule struct{}

func (SEORule) ID() string          { return "seo" }
func (SEORule) Description() string { return "title length, single H1, non-empty tags" }

const (
	seoTitleMin = 10
	seoTitleMax = 70
)

var h1Re = regexp.MustCompile(`(?m)^#\s+\S`)

func (r SEORule) Check(_ context.Context, vc *ValidateContext) []Finding {
	var out []Finding
	for _, t := range vc.Targets {
		if t.MetaErr != nil || t.Type == "" {
			continue
		}
		title := metaStr(t.Meta, "title")
		if title != "" {
			if n := len([]rune(title)); n < seoTitleMin || n > seoTitleMax {
				out = append(out, Finding{
					Rule: r.ID(), Severity: SeverityWarning, Target: t.Dir, File: t.MetaPath,
					Message: fmt.Sprintf("title is %d chars; aim for %d–%d for SEO", n, seoTitleMin, seoTitleMax),
				})
			}
		}

		if t.Template != nil && hasField(t.Template.Fields, "tags") && isEmpty(t.Meta["tags"]) {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityWarning, Target: t.Dir, File: t.MetaPath,
				Message: "no tags set (SEO: add topical tags)",
			})
		}

		if strings.TrimSpace(t.Body) != "" {
			n := len(h1Re.FindAllString(t.Body, -1))
			file := t.BodyFile
			if file == "" {
				file = t.MetaPath
			}
			switch {
			case n == 0:
				out = append(out, Finding{
					Rule: r.ID(), Severity: SeverityWarning, Target: t.Dir, File: file,
					Message: "body has no H1 (`# `) heading",
				})
			case n > 1:
				out = append(out, Finding{
					Rule: r.ID(), Severity: SeverityWarning, Target: t.Dir, File: file,
					Message: fmt.Sprintf("body has %d H1 headings; use exactly one", n),
				})
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// pr-template (plan §3: "PR-template field validation (pairs with C9)")

// PRTemplateRule validates that a supplied PR body carries the fields and the
// Sample-clearance section the C9 (ISI-7119) content PR template declares for
// its content type. It no-ops when no PR body is supplied, so running `content
// validate` on a plain content folder never triggers it.
//
// Zero duplication: the expected fields come from pr.RenderStaticTemplate — the
// single source of truth C9 generates both the pre-filled bot PR body and the
// static .github/PULL_REQUEST_TEMPLATE/<type>.md files from. If C9 adds or
// renames a field, this rule requires the new field automatically.
type PRTemplateRule struct{}

func (PRTemplateRule) ID() string { return "pr-template" }
func (PRTemplateRule) Description() string {
	return "PR body carries the C9 content-template fields + Sample clearance"
}

// prMarkerRe extracts the content type from the template marker C9 writes at
// the top of every content PR body.
var prMarkerRe = regexp.MustCompile(`(?i)<!--\s*ContentRemixer content PR template:\s*([a-z]+)\s*-->`)

// prFieldLabelRe captures the bold field labels (**Label:**) a rendered C9
// template declares.
var prFieldLabelRe = regexp.MustCompile(`\*\*([^*]+?):\*\*`)

func (r PRTemplateRule) Check(_ context.Context, vc *ValidateContext) []Finding {
	if strings.TrimSpace(vc.PRBody) == "" {
		return nil // only runs when a PR body is supplied
	}
	file := vc.PRBodyFile
	if file == "" {
		file = "(pr-body)"
	}

	contentType := prContentType(vc)
	expected := pr.RenderStaticTemplate(contentType)

	var out []Finding
	for _, label := range fieldLabels(expected) {
		// Match the bold label prefix "**Label:" so a reworded value cannot
		// be mistaken for a missing field.
		if !strings.Contains(vc.PRBody, "**"+label+":") {
			out = append(out, Finding{
				Rule: r.ID(), Severity: SeverityError, File: file,
				Message: fmt.Sprintf("PR body missing %q field (per %s content PR template, pairs with C9)", label, contentType),
			})
		}
	}
	if !strings.Contains(strings.ToLower(vc.PRBody), "sample clearance") {
		out = append(out, Finding{
			Rule: r.ID(), Severity: SeverityError, File: file,
			Message: "PR body missing the Sample clearance section (required by the content PR template)",
		})
	}
	return out
}

// prContentType resolves which C9 template to validate against: an explicit
// override, else the PR body's template marker, else the default template.
func prContentType(vc *ValidateContext) string {
	if vc.PRContentType != "" {
		return pr.ContentType(vc.PRContentType)
	}
	if m := prMarkerRe.FindStringSubmatch(vc.PRBody); m != nil {
		return pr.ContentType(m[1])
	}
	return pr.DefaultContentType
}

// fieldLabels extracts the unique bold field labels from a rendered template,
// preserving first-seen order for stable findings.
func fieldLabels(rendered string) []string {
	seen := map[string]bool{}
	var labels []string
	for _, m := range prFieldLabelRe.FindAllStringSubmatch(rendered, -1) {
		l := strings.TrimSpace(m[1])
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		labels = append(labels, l)
	}
	return labels
}

// ---------------------------------------------------------------------------
// helpers

// isEmpty reports whether a parsed meta value is unset/placeholder: nil, blank
// string, zero number, or an empty list/map. Numeric 0 counts as empty because
// the templates use 0 as an unfilled placeholder (episode: 0, duration_min: 0).
func isEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case bool:
		return !t
	case int:
		return t == 0
	case int64:
		return t == 0
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

func metaStr(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

func hasField(fields []string, name string) bool {
	for _, f := range fields {
		if f == name {
			return true
		}
	}
	return false
}
