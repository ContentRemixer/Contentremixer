// Command contentremixer is the Content CI command-line entrypoint (ISI-7118,
// subtask C7). Its one Phase-1 subcommand, `content validate`, runs the
// extensible Content CI rule registry (internal/contentvalidate) over the
// changed content folders of a reference-repo checkout and emits either a
// human-readable report (local use) or GitHub PR annotations + a markdown
// summary table (CI use, via the composite action in
// .github/actions/content-validate).
//
// Usage:
//
//	contentremixer content validate [flags]
//	  --repo-root DIR        reference-repo checkout root (default ".")
//	  --changed LIST         comma/space/newline-separated changed paths
//	  --changed-file FILE    read changed paths from FILE (one per line; "-" = stdin)
//	  --all                  validate every content folder under content/
//	  --pr-body-file FILE    PR body for the pr-template check
//	  --format text|github   output mode (default "text")
//	  --summary-out FILE     write the markdown summary here (github mode;
//	                         defaults to $GITHUB_STEP_SUMMARY when set)
//	  --fail-on error|warning  severity that fails the run (default "error")
//	  --list-rules           print the registered rules and exit
//
// Exit codes: 0 pass · 1 validation failed (per --fail-on) · 2 usage/IO error.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/contentvalidate"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "content" && args[1] == "validate" {
		os.Exit(runValidate(args[2:]))
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println("contentremixer", version)
		os.Exit(0)
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		usage(os.Stdout)
		os.Exit(0)
	}
	usage(os.Stderr)
	os.Exit(2)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "contentremixer — Content CI CLI (ISI-7118)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  contentremixer content validate [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'contentremixer content validate --help' for the flag list.")
}

func runValidate(argv []string) int {
	fs := newFlagSet()
	opts, err := fs.parse(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "content validate: %v\n", err)
		return 2
	}

	engine := contentvalidate.DefaultEngine()

	if opts.listRules {
		for _, r := range engine.Rules() {
			fmt.Printf("%-16s %s\n", r.ID(), r.Description())
		}
		return 0
	}

	ctx := context.Background()

	var targets []contentvalidate.Target
	var skipped []string
	if opts.all {
		targets, skipped, err = contentvalidate.DiscoverAll(ctx, opts.repoRoot)
	} else {
		changed, cerr := opts.changedPaths()
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "content validate: %v\n", cerr)
			return 2
		}
		if len(changed) == 0 {
			fmt.Fprintln(os.Stderr, "content validate: no changed paths supplied (use --changed, --changed-file, or --all)")
			return 2
		}
		targets, skipped, err = contentvalidate.Discover(ctx, opts.repoRoot, changed)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "content validate: discovery failed: %v\n", err)
		return 2
	}

	prBody := ""
	if opts.prBodyFile != "" {
		b, rerr := os.ReadFile(opts.prBodyFile)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "content validate: reading --pr-body-file: %v\n", rerr)
			return 2
		}
		prBody = string(b)
	}

	vc := &contentvalidate.ValidateContext{
		RepoRoot:      opts.repoRoot,
		Targets:       targets,
		PRBody:        prBody,
		PRBodyFile:    opts.prBodyFile,
		PRContentType: opts.prContentType,
	}

	if len(skipped) > 0 && opts.format == "text" {
		fmt.Fprintf(os.Stderr, "content validate: %d path(s) not in a content folder, skipped\n", len(skipped))
	}
	if len(targets) == 0 && prBody == "" {
		fmt.Fprintln(os.Stderr, "content validate: no content folders to validate")
		// Not an error: a PR touching only non-content files legitimately
		// has nothing to check.
		return 0
	}

	report := engine.Run(ctx, vc)

	switch opts.format {
	case "github":
		report.WriteAnnotations(os.Stdout)
		if sErr := writeSummary(report, opts); sErr != nil {
			fmt.Fprintf(os.Stderr, "content validate: writing summary: %v\n", sErr)
		}
	default:
		report.WriteText(os.Stdout, opts.failOnWarning)
	}

	if report.OK(opts.failOnWarning) {
		return 0
	}
	return 1
}

// writeSummary writes the markdown summary to --summary-out, falling back to
// $GITHUB_STEP_SUMMARY. A missing target is a no-op (annotations already went
// to stdout).
func writeSummary(report contentvalidate.Report, opts *options) error {
	target := opts.summaryOut
	if target == "" {
		target = os.Getenv("GITHUB_STEP_SUMMARY")
	}
	if target == "" {
		return nil
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	report.WriteSummary(f, opts.failOnWarning)
	return nil
}

// ---------------------------------------------------------------------------
// flags

type options struct {
	repoRoot      string
	changed       string
	changedFile   string
	all           bool
	prBodyFile    string
	prContentType string
	format        string
	summaryOut    string
	failOnWarning bool
	listRules     bool
}

// changedPaths resolves the changed-path list from --changed and/or
// --changed-file (one path per line; "-" reads stdin). Blank lines are ignored.
func (o *options) changedPaths() ([]string, error) {
	var paths []string
	for _, p := range strings.FieldsFunc(o.changed, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' ' || r == '\t' || r == '\r'
	}) {
		if s := strings.TrimSpace(p); s != "" {
			paths = append(paths, s)
		}
	}
	if o.changedFile != "" {
		var rd io.Reader
		if o.changedFile == "-" {
			rd = os.Stdin
		} else {
			f, err := os.Open(o.changedFile)
			if err != nil {
				return nil, fmt.Errorf("opening --changed-file: %w", err)
			}
			defer f.Close()
			rd = f
		}
		sc := bufio.NewScanner(rd)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if s := strings.TrimSpace(sc.Text()); s != "" {
				paths = append(paths, s)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("reading --changed-file: %w", err)
		}
	}
	return paths, nil
}

// flagSet is a tiny hand-rolled parser (no cobra dependency, matching the
// operator's stdlib-only posture). It recognizes --flag=value and --flag value.
type flagSet struct{ opts *options }

func newFlagSet() *flagSet {
	return &flagSet{opts: &options{repoRoot: ".", format: "text"}}
}

func (fs *flagSet) parse(argv []string) (*options, error) {
	o := fs.opts
	failOn := "error"
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, inlineVal, hasInline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unexpected argument %q", arg)
		}
		next := func() (string, error) {
			if hasInline {
				return inlineVal, nil
			}
			if i+1 >= len(argv) {
				return "", fmt.Errorf("flag --%s needs a value", name)
			}
			i++
			return argv[i], nil
		}
		switch name {
		case "repo-root":
			v, err := next()
			if err != nil {
				return nil, err
			}
			o.repoRoot = v
		case "changed":
			v, err := next()
			if err != nil {
				return nil, err
			}
			o.changed = v
		case "changed-file":
			v, err := next()
			if err != nil {
				return nil, err
			}
			o.changedFile = v
		case "pr-body-file":
			v, err := next()
			if err != nil {
				return nil, err
			}
			o.prBodyFile = v
		case "pr-content-type":
			v, err := next()
			if err != nil {
				return nil, err
			}
			o.prContentType = v
		case "format":
			v, err := next()
			if err != nil {
				return nil, err
			}
			if v != "text" && v != "github" {
				return nil, fmt.Errorf("--format must be text or github, got %q", v)
			}
			o.format = v
		case "summary-out":
			v, err := next()
			if err != nil {
				return nil, err
			}
			o.summaryOut = v
		case "fail-on":
			v, err := next()
			if err != nil {
				return nil, err
			}
			if v != "error" && v != "warning" {
				return nil, fmt.Errorf("--fail-on must be error or warning, got %q", v)
			}
			failOn = v
		case "all":
			o.all = true
		case "list-rules":
			o.listRules = true
		case "help", "h":
			usageValidate(os.Stdout)
			os.Exit(0)
		default:
			return nil, fmt.Errorf("unknown flag --%s", name)
		}
	}
	o.failOnWarning = failOn == "warning"
	return o, nil
}

func usageValidate(w io.Writer) {
	fmt.Fprintln(w, "Usage: contentremixer content validate [flags]")
	fmt.Fprintln(w, "  --repo-root DIR          reference-repo checkout root (default \".\")")
	fmt.Fprintln(w, "  --changed LIST           comma/space/newline-separated changed paths")
	fmt.Fprintln(w, "  --changed-file FILE      read changed paths from FILE (\"-\" = stdin)")
	fmt.Fprintln(w, "  --all                    validate every content folder under content/")
	fmt.Fprintln(w, "  --pr-body-file FILE      PR body for the pr-template check")
	fmt.Fprintln(w, "  --pr-content-type TYPE   force the content PR template type (blog|social|newsletter|default)")
	fmt.Fprintln(w, "  --format text|github     output mode (default \"text\")")
	fmt.Fprintln(w, "  --summary-out FILE       markdown summary path (github mode)")
	fmt.Fprintln(w, "  --fail-on error|warning  severity that fails the run (default \"error\")")
	fmt.Fprintln(w, "  --list-rules             print the registered rules and exit")
}
