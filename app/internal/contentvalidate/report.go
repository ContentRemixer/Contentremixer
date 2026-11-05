package contentvalidate

import (
	"fmt"
	"io"
	"strings"
)

// WriteText renders findings as a plain, human-readable list for local CLI use
// and a one-line verdict. It is the default (non-CI) output.
func (r Report) WriteText(w io.Writer, failOnWarning bool) {
	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "content validate: no findings ✓")
		return
	}
	for _, f := range r.Findings {
		loc := f.File
		if loc == "" {
			loc = f.Target
		}
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.Line)
		}
		fmt.Fprintf(w, "%-8s %-16s %s — %s\n", strings.ToUpper(string(f.Severity)), f.Rule, loc, f.Message)
	}
	fmt.Fprintf(w, "\ncontent validate: %d error(s), %d warning(s) → %s\n",
		r.Errors(), r.Warnings(), verdict(r.OK(failOnWarning)))
}

// WriteAnnotations emits GitHub Actions workflow commands (::error / ::warning
// with file=,line=) to w. GitHub surfaces these as inline PR annotations when
// the file paths are workspace-relative — which they are, since discovery keys
// everything off repo-relative paths.
func (r Report) WriteAnnotations(w io.Writer) {
	for _, f := range r.Findings {
		var props []string
		if f.File != "" {
			props = append(props, "file="+escapeProp(f.File))
		}
		if f.Line > 0 {
			props = append(props, fmt.Sprintf("line=%d", f.Line))
		}
		title := "content-validate: " + f.Rule
		props = append(props, "title="+escapeProp(title))
		fmt.Fprintf(w, "::%s %s::%s\n", f.Severity, strings.Join(props, ","), escapeData(f.Message))
	}
}

// WriteSummary renders a GitHub-flavored markdown summary (heading + verdict +
// a findings table grouped error-first) suitable for $GITHUB_STEP_SUMMARY. With
// no findings it writes a green one-liner.
func (r Report) WriteSummary(w io.Writer, failOnWarning bool) {
	fmt.Fprintln(w, "## Content CI — `content validate`")
	fmt.Fprintln(w)
	ok := r.OK(failOnWarning)
	fmt.Fprintf(w, "**%s** — %d error(s), %d warning(s).\n\n", verdict(ok), r.Errors(), r.Warnings())
	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "No findings — all changed content folders passed. ✅")
		return
	}
	fmt.Fprintln(w, "| Severity | Rule | Target | Location | Finding |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- |")
	// Errors first, then warnings; within each, the stable report order.
	for _, want := range []Severity{SeverityError, SeverityWarning} {
		for _, f := range r.Findings {
			if f.Severity != want {
				continue
			}
			loc := f.File
			if f.Line > 0 {
				loc = fmt.Sprintf("%s:%d", loc, f.Line)
			}
			fmt.Fprintf(w, "| %s | `%s` | %s | %s | %s |\n",
				severityBadge(f.Severity), f.Rule, mdCell(f.Target), mdCell(loc), mdCell(f.Message))
		}
	}
}

func verdict(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

func severityBadge(s Severity) string {
	switch s {
	case SeverityError:
		return "🔴 error"
	case SeverityWarning:
		return "🟡 warning"
	default:
		return string(s)
	}
}

// mdCell makes a string safe inside a markdown table cell: escape pipes and
// collapse newlines; render empty as an em dash.
func mdCell(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// escapeData escapes a workflow-command message body per GitHub's rules.
func escapeData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// escapeProp escapes a workflow-command property value (adds ":" and "," on top
// of the data escapes).
func escapeProp(s string) string {
	s = escapeData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}
