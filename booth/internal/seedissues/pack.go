// Package seedissues seeds a versioned pack of content-generation "template
// issues" into an onboarded GitHub repo (ISI-7896, W3 of ISI-7887). Option A:
// the pack is checked into this repo (content-templates/*.md with front-matter)
// and shipped inside the Booth binary, rather than live-copied from a source
// repo at runtime.
//
// Booth is a separate Go module and cannot import app/internal, so — like
// booth/internal/approve — this package carries its own tiny GitHub REST client
// instead of reusing app/internal/githubapp.
package seedissues

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// PackVersion is the pack's schema/content version. It is recorded on every
// seeded issue for traceability; idempotency is keyed on the per-template id
// (not the version) so bumping bodies never re-seeds a duplicate.
const PackVersion = "1"

//go:embed content-templates/*.md
var packFS embed.FS

// IssueTemplate is one content-generation issue in the pack.
type IssueTemplate struct {
	// ID is the stable slug (the markdown filename without extension). It keys
	// the idempotency marker, so renaming a file reseeds — treat ids as stable.
	ID     string
	Title  string
	Labels []string
	Body   string
}

// frontMatter is the YAML header parsed from each template file.
type frontMatter struct {
	Title  string   `json:"title"`
	Labels []string `json:"labels"`
}

// Pack is the embedded, versioned set of template issues, ordered by id.
var Pack = mustLoad(packFS)

func mustLoad(fsys fs.FS) []IssueTemplate {
	tpls, err := load(fsys)
	if err != nil {
		// A parse failure here is a build-time authoring bug in the embedded
		// pack, not a runtime input — fail loudly at startup rather than ship a
		// silently-empty catalog.
		panic(fmt.Sprintf("seedissues: loading embedded pack: %v", err))
	}
	return tpls
}

func load(fsys fs.FS) ([]IssueTemplate, error) {
	entries, err := fs.ReadDir(fsys, "content-templates")
	if err != nil {
		return nil, err
	}
	var out []IssueTemplate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "content-templates/"+e.Name())
		if err != nil {
			return nil, err
		}
		tpl, err := parse(strings.TrimSuffix(e.Name(), ".md"), raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		out = append(out, tpl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// parse splits a Jekyll-style front-matter document into its YAML header and
// markdown body. The file must open with a "---" fence.
func parse(id string, raw []byte) (IssueTemplate, error) {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return IssueTemplate{}, fmt.Errorf("missing front-matter fence")
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return IssueTemplate{}, fmt.Errorf("unterminated front-matter")
	}
	header, body := rest[:end], rest[end+len("\n---\n"):]

	var fm frontMatter
	if err := yaml.Unmarshal([]byte(header), &fm); err != nil {
		return IssueTemplate{}, fmt.Errorf("front-matter: %w", err)
	}
	if strings.TrimSpace(fm.Title) == "" {
		return IssueTemplate{}, fmt.Errorf("front-matter: title is required")
	}
	return IssueTemplate{
		ID:     id,
		Title:  strings.TrimSpace(fm.Title),
		Labels: fm.Labels,
		Body:   strings.TrimSpace(body),
	}, nil
}

// marker is the hidden HTML comment stamped into every seeded issue body. It is
// how a re-run recognises an already-seeded template (keyed on id, not version)
// so seeding is idempotent.
func marker(id string) string {
	return fmt.Sprintf("<!-- content-template: %s -->", id)
}

// renderBody returns the issue body with the idempotency marker and a version
// footer appended.
func (t IssueTemplate) renderBody() string {
	return fmt.Sprintf("%s\n\n---\n%s\n<sub>Seeded from the content-template pack (v%s).</sub>",
		t.Body, marker(t.ID), PackVersion)
}

// TrackingLabel is applied to every seeded issue so a re-run can list prior
// seeds with a single labelled query instead of scanning all issues.
const TrackingLabel = "content-template"

// allLabels returns the de-duplicated set of labels the pack requires to exist
// in the target repo: every template's labels plus the tracking label.
func allLabels(tpls []IssueTemplate) []string {
	seen := map[string]bool{TrackingLabel: true}
	out := []string{TrackingLabel}
	for _, t := range tpls {
		for _, l := range t.Labels {
			if l = strings.TrimSpace(l); l != "" && !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return out
}
