package contentvalidate

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/analyzer"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
)

// typeToFormat maps a content type to the ISI-7080 analyzer input format
// vocabulary, so DetectedInput speaks the same language as the recycle
// pipeline's label routes (design §2.2).
var typeToFormat = map[string]string{
	"blog":        analyzer.FormatBlog,
	"videos":      analyzer.FormatTeleprompter,
	"livestreams": analyzer.FormatLivestream,
	"podcasts":    analyzer.FormatPodcast,
}

// bodyFileKeys is the preference order for resolving a target's primary body
// file from meta.files (mirrors the analyzer's per-format body selection, plus
// the C9 output types: social files.post, newsletter files.issue).
var bodyFileKeys = []string{"post", "teleprompter", "transcript", "issue"}

// templateMetaKeys are top-level meta.template.yaml keys that are NOT content
// fields a content item fills in: "files" declares assets (checked by
// required-assets) and "pr_template" is the C9 PR-template contract block
// (consumed by the pr-template rule) — neither is a field whose emptiness
// asset-structure should flag.
var templateMetaKeys = map[string]bool{"files": true, "pr_template": true}

// knownTypes is the content-type set (one meta.template.yaml each under
// content/<type>/). Mirrors the content/ tree: the four source types plus the
// two output types C9 (ISI-7119) added (social, newsletter).
var knownTypes = map[string]bool{
	"blog":        true,
	"videos":      true,
	"podcasts":    true,
	"livestreams": true,
	"social":      true,
	"newsletter":  true,
}

// Template is a parsed content/<type>/meta.template.yaml: the authoritative
// declaration of a type's required assets (Files) and expected meta fields
// (Fields). It is built from the template document, so adding a type or a
// field is a content-repo edit, not a code change.
type Template struct {
	// Type is the content type the template governs.
	Type string
	// Path is the repo-relative template path.
	Path string
	// Files maps each declared asset key to its default filename
	// (files.<key>: <name> in the template).
	Files map[string]string
	// FileOrder preserves the template's files.* key order for stable output.
	FileOrder []string
	// Fields are the template's top-level non-"files" keys (title, author,
	// tags, ...), in template order — the fields a content item is expected
	// to fill in.
	Fields []string
}

// Discover maps a set of changed repo-relative paths to the unique item
// folders (Targets) they belong to, loads each folder's meta.yaml and its
// type template, and resolves the body via the reused ISI-7080 analyzer.
//
// An item folder is the nearest ancestor directory containing a meta.yaml that
// sits under either content/<type>/<folder> (a Crate item) or recycled/<slug>/
// (a recycled draft — the folder a remix PR adds, ISI-7076 §4). Paths that
// resolve to no item folder (e.g. a changed styles/*.md, or the
// meta.template.yaml itself) are skipped — Skipped lists them for the caller to
// log. Discovery reads through analyzer.LocalFS (reuse) so path traversal is
// bounded to the repo root.
func Discover(ctx context.Context, repoRoot string, changed []string) (targets []Target, skipped []string, err error) {
	fs := analyzer.LocalFS{Root: repoRoot}

	// Collect unique content-folder dirs (nearest ancestor with a meta.yaml).
	dirs := map[string]bool{}
	var order []string
	for _, raw := range changed {
		rel := normalizeRel(raw)
		if rel == "" {
			skipped = append(skipped, raw)
			continue
		}
		dir := nearestItemDir(repoRoot, rel)
		if dir == "" {
			skipped = append(skipped, raw)
			continue
		}
		if !dirs[dir] {
			dirs[dir] = true
			order = append(order, dir)
		}
	}
	sort.Strings(order)

	templates := map[string]*Template{}
	for _, dir := range order {
		t := Target{
			Dir:      dir,
			MetaPath: path.Join(dir, "meta.yaml"),
		}

		metaBytes, readErr := fs.ReadFile(t.MetaPath)
		if readErr != nil {
			t.MetaErr = fmt.Errorf("reading %s: %w", t.MetaPath, readErr)
		} else if meta, perr := parseYAMLMap(metaBytes); perr != nil {
			t.MetaErr = fmt.Errorf("parsing %s: %w", t.MetaPath, perr)
		} else {
			t.Meta = meta
		}

		// Type is resolved AFTER meta is read: a Crate item carries its type in
		// the path (content/<type>/...), but a recycled/<slug>/ draft has no
		// type path segment and resolves its type from meta.output_format.
		t.Type = resolveType(dir, t.Meta)

		if t.Type != "" {
			tmpl, ok := templates[t.Type]
			if !ok {
				tmpl = loadTemplate(fs, t.Type)
				templates[t.Type] = tmpl // may be nil; cache the miss
			}
			t.Template = tmpl
			t.DetectedInput = typeToFormat[t.Type]
		}

		// Resolve the primary body file through the reused analyzer.LocalFS
		// (ISI-7080). Unlike analyzer.Analyze we do NOT require a git commit
		// (Sample-clearance is a recycle concern, not a validation one) and we
		// do NOT fail on a missing/URL-only body — required-assets reports a
		// genuinely missing declared asset, and URL-backed types (podcasts/
		// livestreams) legitimately validate with an empty local body.
		t.BodyFile, t.Body = resolveBody(fs, dir, t.Meta, t.Template)

		targets = append(targets, t)
	}
	return targets, skipped, nil
}

// DiscoverAll finds every item folder under content/ AND recycled/ (the --all
// mode), then delegates to Discover so loading/body resolution is identical.
func DiscoverAll(ctx context.Context, repoRoot string) ([]Target, []string, error) {
	var metas []string
	for _, base := range []string{"content", "recycled"} {
		baseDir := filepath.Join(repoRoot, base)
		walkErr := filepath.WalkDir(baseDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			if !d.IsDir() && d.Name() == "meta.yaml" {
				rel, rerr := filepath.Rel(repoRoot, p)
				if rerr != nil {
					return rerr
				}
				metas = append(metas, filepath.ToSlash(rel))
			}
			return nil
		})
		if walkErr != nil && !os.IsNotExist(walkErr) {
			return nil, nil, fmt.Errorf("scanning %s/: %w", base, walkErr)
		}
	}
	return Discover(ctx, repoRoot, metas)
}

// nearestItemDir walks rel's ancestors (including rel itself if it is a
// directory) for the nearest one that contains a meta.yaml and sits under
// content/<type>/<folder> (a Crate item) or recycled/<slug>/ (a recycled
// draft). Returns "" when none qualifies.
func nearestItemDir(repoRoot, rel string) string {
	// Candidate dirs: if rel itself is a dir, start there; else its parent.
	start := rel
	if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
		start = path.Dir(rel)
	}
	for dir := start; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if !underContent(dir) && !underRecycled(dir) {
			continue
		}
		if fileExists(repoRoot, path.Join(dir, "meta.yaml")) {
			return dir
		}
	}
	return ""
}

// underRecycled reports whether dir is a recycled draft folder: a "recycled"
// segment with at least one folder segment after it (recycled/<slug>[/...]).
// This is where a remix PR lands its generated Mix (ISI-7076 §4); excludes the
// recycled/ root itself.
func underRecycled(dir string) bool {
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	for i, s := range segs {
		if s == "recycled" && i+1 <= len(segs)-1 {
			return true
		}
	}
	return false
}

// resolveType determines an item's content type. Crate items carry it in the
// path (the segment after content/). A recycled/<slug>/ draft has no type path
// segment, so its type comes from meta.output_format via the C9 pr.ContentType
// mapping (blog|social|newsletter; "short"/"tutorial"/"teleprompter" and the
// like map to "default", returned here as "" so the type-specific rules
// degrade gracefully while content-quality/seo/pr-template still run).
func resolveType(dir string, meta map[string]any) string {
	if t := contentType(dir); t != "" {
		return t
	}
	if underRecycled(dir) && meta != nil {
		if of, ok := meta["output_format"].(string); ok {
			if ct := pr.ContentType(of); knownTypes[ct] {
				return ct
			}
		}
	}
	return ""
}

// underContent reports whether dir is a content folder candidate: it contains
// a "content" segment with at least a type segment AND a folder segment after
// it (content/<type>/<folder>[/...]). This excludes content/ itself and the
// content/<type>/ template dir, whose meta.template.yaml is not a content item.
func underContent(dir string) bool {
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	for i, s := range segs {
		if s == "content" && i+2 <= len(segs)-1 {
			return true
		}
	}
	return false
}

// contentType returns the type segment after content/ for dir, or "" when dir
// is not under a known content type.
func contentType(dir string) string {
	segs := strings.Split(strings.Trim(dir, "/"), "/")
	for i, s := range segs {
		if s == "content" && i+1 <= len(segs)-1 {
			t := segs[i+1]
			if knownTypes[t] {
				return t
			}
			return ""
		}
	}
	return ""
}

// loadTemplate reads and parses content/<type>/meta.template.yaml. Returns nil
// when the template is absent or unparsable (required-assets reports that).
func loadTemplate(fs analyzer.LocalFS, typ string) *Template {
	p := path.Join("content", typ, "meta.template.yaml")
	b, err := fs.ReadFile(p)
	if err != nil {
		return nil
	}
	// Parse into a yaml.Node to preserve key order for stable field output.
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	doc := root.Content[0]
	t := &Template{Type: typ, Path: p, Files: map[string]string{}}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		val := doc.Content[i+1]
		if key == "files" {
			if val.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(val.Content); j += 2 {
					fk := val.Content[j].Value
					fv := val.Content[j+1].Value
					t.Files[fk] = fv
					t.FileOrder = append(t.FileOrder, fk)
				}
			}
			continue
		}
		if templateMetaKeys[key] {
			continue // non-content structural key (pr_template contract)
		}
		t.Fields = append(t.Fields, key)
	}
	return t
}

// resolveBody picks and reads the target's primary body file: the first of
// files.{post,teleprompter,transcript} the meta declares (falling back to the
// template's default filename for that key), read through the repo-bounded
// analyzer.LocalFS. Returns ("","") when no body file is declared or readable.
func resolveBody(fs analyzer.LocalFS, dir string, meta map[string]any, tmpl *Template) (bodyFile, body string) {
	metaFiles, _ := meta["files"].(map[string]any)
	for _, key := range bodyFileKeys {
		name := ""
		if metaFiles != nil {
			if s, ok := metaFiles[key].(string); ok {
				name = strings.TrimSpace(s)
			}
		}
		if name == "" && tmpl != nil {
			name = strings.TrimSpace(tmpl.Files[key])
		}
		if name == "" {
			continue
		}
		rel := path.Join(dir, name)
		b, err := fs.ReadFile(rel)
		if err != nil {
			continue // required-assets reports a declared-but-missing asset
		}
		return rel, string(b)
	}
	return "", ""
}

// normalizeRel canonicalizes a changed path to a clean repo-relative slash
// path, rejecting "" and parent-escaping paths.
func normalizeRel(p string) string {
	p = strings.TrimSpace(p)
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return ""
		}
	}
	return path.Clean(p)
}

func fileExists(repoRoot, rel string) bool {
	info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	return err == nil && !info.IsDir()
}

// parseYAMLMap parses a YAML document into a generic string-keyed map.
func parseYAMLMap(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}
