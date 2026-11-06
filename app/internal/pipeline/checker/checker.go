// Package checker implements pipeline subtask G (ISI-7082, design §4
// "Checker / Mastering"): the fail-closed quality gate that runs over the
// final Mix before a PR is opened.
//
// It enforces four HARD scrub rules (severity "block") with deterministic
// matchers and one SOFT brand-voice rule (severity "warn"):
//
//	tenant-id         — tenant/company UUIDs and labelled tenant ids       (block)
//	token             — API keys, bearer tokens, PATs, JWTs, sk-/AKIA/gh*  (block)
//	ip                — RFC1918 + public IPv4 and IPv6 literals            (block)
//	internal-hostname — *.local/.internal/.lan/.svc[.cluster.local]/…      (block)
//	brand-voice       — marketing-voice phrases (styles/*.md say "no       (warn)
//	                    marketing voice")
//
// FAIL-CLOSED semantics (design §4): a single block violation sets
// CheckResult.Passed = false and the run must open NO PR. warn violations are
// advisory and never flip Passed. The returned error is reserved for
// operational failures (there are none in Phase 1 — the matchers are pure),
// NOT for policy violations, which travel as Violations.
//
// Both Mix.Markdown and the flattened Mix.Meta are scanned (design §4: "run
// on the final Mix.Markdown *and* Mix.Meta") — a secret that leaks into the
// output meta.yaml is as blocking as one in the body.
//
// Evidence (Violation.Match) is always redacted/truncated so the blocked-run
// issue comment (posted on a public GitHub issue) never re-leaks the secret
// it just caught.
package checker

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// Rule ids (design §4).
const (
	RuleTenantID         = "tenant-id"
	RuleToken            = "token"
	RuleIP               = "ip"
	RuleInternalHostname = "internal-hostname"
	RuleBrandVoice       = "brand-voice"
)

// Checker is the subtask-G implementation of pipeline.Checker. It carries no
// configuration — the Phase-1 rule catalogue is built in — so the zero value
// is ready to use.
type Checker struct{}

var _ pipeline.Checker = (*Checker)(nil)

// New returns a ready Checker.
func New() *Checker { return &Checker{} }

// rule pairs a compiled matcher with its id and severity.
type rule struct {
	id       string
	severity string
	re       *regexp.Regexp
}

// blockRules are the hard, fail-closed scrub matchers. They are deliberately
// anchored on unambiguous shapes (prefixes, validated octets, DNS suffixes)
// rather than loose heuristics, so a clean draft passes (acceptance: "clean
// drafts pass") while planted secrets are caught (acceptance: "drafts with
// planted secrets/tenant IDs/IPs/internal hostnames are blocked").
var blockRules = []rule{
	// tenant-id: UUID (any version) and values labelled as a tenant/company id.
	{RuleTenantID, pipeline.SeverityBlock, regexp.MustCompile(
		`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)},
	{RuleTenantID, pipeline.SeverityBlock, regexp.MustCompile(
		`(?i)\b(?:tenant|company|org(?:anization)?|account|customer)[_-]?id\b\s*[:=]\s*"?[A-Za-z0-9][A-Za-z0-9._-]{5,}`)},

	// token: well-known secret shapes. Prefix-anchored so a bare 40-hex git
	// SHA in sample_clearance meta is NOT mistaken for a secret.
	{RuleToken, pipeline.SeverityBlock, regexp.MustCompile( // JWT
		`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{RuleToken, pipeline.SeverityBlock, regexp.MustCompile( // OpenAI-style
		`\bsk-[A-Za-z0-9_-]{16,}\b`)},
	{RuleToken, pipeline.SeverityBlock, regexp.MustCompile( // GitHub tokens
		`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`)},
	{RuleToken, pipeline.SeverityBlock, regexp.MustCompile( // AWS access key id
		`\bAKIA[0-9A-Z]{16}\b`)},
	{RuleToken, pipeline.SeverityBlock, regexp.MustCompile( // Bearer header
		`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}=*`)},
	{RuleToken, pipeline.SeverityBlock, regexp.MustCompile( // labelled secret with opaque value
		`(?i)\b(?:api[_-]?key|secret|access[_-]?token|auth[_-]?token|password|passwd)\b\s*[:=]\s*"?[A-Za-z0-9+/_-]{12,}`)},

	// ip: IPv4 with validated 0-255 octets (so "1.31.0" k8s versions and
	// 4-part versions with an octet >255 do not match) + IPv6 literals that
	// require "::" or a full 8-group form (so HH:MM:SS clocks never match).
	{RuleIP, pipeline.SeverityBlock, ipv4Re},
	{RuleIP, pipeline.SeverityBlock, regexp.MustCompile( // full 8-group IPv6
		`(?i)\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b`)},
	// compressed IPv6 ("::"): require a real hextet adjacent to the "::" so
	// code like std::vec / boost:: does not match. Two forms: leading
	// hextet(s) then "::" (fe80::, 2001:db8::1) or "::" then trailing
	// hextet(s) (::1, ::ffff:1).
	{RuleIP, pipeline.SeverityBlock, regexp.MustCompile(
		`(?i)\b(?:[0-9a-f]{1,4}:)+:(?:[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*)?|::[0-9a-f]{1,4}(?::[0-9a-f]{1,4})*\b`)},

	// internal-hostname: DNS names under internal suffixes, incl. k8s service
	// DNS. ponytail: scoped to suffix-anchored names — the design's "bare
	// service names" (a single unqualified token) cannot be matched
	// deterministically without blocking ordinary prose, so Phase 1 covers
	// the false-positive-safe suffixed subset; upgrade path is a configurable
	// internal-domain allow/deny list.
	{RuleInternalHostname, pipeline.SeverityBlock, regexp.MustCompile(
		`(?i)\b[a-z0-9]([a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*\.(?:local|internal|lan|home|corp|intranet|localdomain)\b`)},
	{RuleInternalHostname, pipeline.SeverityBlock, regexp.MustCompile( // k8s service DNS
		`(?i)\b[a-z0-9-]+\.[a-z0-9-]+\.svc(?:\.cluster\.local)?\b`)},
}

// warnRules are soft brand-voice matchers. ponytail: deterministic
// marketing-phrase blocklist stands in for the design's "tone check against
// styles/<format>.md"; a style-conditioned tone model is Phase 2. warn never
// blocks, so a miss here cannot wrongly open or close the gate.
var warnRules = []rule{
	{RuleBrandVoice, pipeline.SeverityWarn, marketingRe},
}

// ipv4Re matches a dotted quad where every octet is 0-255.
var ipv4Re = regexp.MustCompile(
	`\b(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\b`)

// marketingRe matches common marketing-voice phrases (word-boundary, case-
// insensitive).
var marketingRe = regexp.MustCompile(`(?i)\b(?:` + strings.Join([]string{
	`game[- ]?changer`, `game[- ]?changing`, `revolutionary`, `revolutionize`,
	`cutting[- ]?edge`, `best[- ]?in[- ]?class`, `world[- ]?class`,
	`state[- ]?of[- ]?the[- ]?art`, `seamless(?:ly)?`, `effortless(?:ly)?`,
	`blazing[- ]?fast`, `supercharge`, `unleash`, `unlock the (?:power|potential)`,
	`take it to the next level`, `paradigm shift`, `synergy`, `synergies`,
	`turnkey`, `one[- ]?stop[- ]?shop`, `delight(?:s|ing)? (?:your |our )?(?:customers|users)`,
	`industry[- ]?leading`, `must[- ]?have`, `look no further`,
}, `|`) + `)\b`)

// Check implements pipeline.Checker: scan Markdown + flattened Meta, collect
// violations, and set Passed = (no block violation).
func (c *Checker) Check(_ context.Context, m pipeline.Mix) (pipeline.CheckResult, error) {
	var out []pipeline.Violation

	surfaces := []struct{ where, text string }{{"markdown", m.Markdown}}
	for _, kv := range flattenMeta(m.Meta) {
		surfaces = append(surfaces, struct{ where, text string }{"meta." + kv.path, kv.val})
	}

	for _, s := range surfaces {
		if strings.TrimSpace(s.text) == "" {
			continue
		}
		for _, r := range blockRules {
			out = append(out, matches(r, s.where, s.text)...)
		}
		for _, r := range warnRules {
			out = append(out, matches(r, s.where, s.text)...)
		}
	}

	passed := true
	for _, v := range out {
		if v.Severity == pipeline.SeverityBlock {
			passed = false
			break
		}
	}
	return pipeline.CheckResult{Passed: passed, Violations: out}, nil
}

// matches runs one rule over one surface, deduping identical redacted hits
// (the same secret echoed twice in a field is one finding).
func matches(r rule, where, text string) []pipeline.Violation {
	found := r.re.FindAllString(text, -1)
	if len(found) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var vs []pipeline.Violation
	for _, hit := range found {
		red := redact(r.id, hit)
		key := red + "\x00" + where
		if seen[key] {
			continue
		}
		seen[key] = true
		vs = append(vs, pipeline.Violation{
			Rule:     r.id,
			Match:    red,
			Severity: r.severity,
			Where:    where,
		})
	}
	return vs
}

// redact turns a raw match into safe evidence. Secrets (tenant-id, token) are
// masked to a short prefix so the value cannot be reconstructed from the
// (public) issue comment; IPs, hostnames, and brand-voice phrases are shown
// in full (that IS the evidence) but length-capped.
func redact(rule, match string) string {
	match = strings.TrimSpace(match)
	switch rule {
	case RuleTenantID, RuleToken:
		// Keep enough to recognise the shape (e.g. "ghp_…", "sk-…") without
		// revealing the secret: first 4 chars, then a masked tail.
		head := match
		if len(head) > 4 {
			head = head[:4]
		}
		return head + "…[redacted]"
	default:
		const cap = 80
		if len(match) > cap {
			return match[:cap] + "…"
		}
		return match
	}
}

// metaKV is one flattened leaf of Mix.Meta.
type metaKV struct {
	path string
	val  string
}

// flattenMeta walks Mix.Meta into (dotted-path, string-value) leaves in a
// deterministic order so scan results are stable. Non-string scalars are
// stringified (an int issue number matches nothing; a stray string secret in
// a nested map is caught).
func flattenMeta(meta map[string]any) []metaKV {
	var out []metaKV
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				child := k
				if prefix != "" {
					child = prefix + "." + k
				}
				walk(child, t[k])
			}
		case []any:
			for i, e := range t {
				walk(fmt.Sprintf("%s[%d]", prefix, i), e)
			}
		case nil:
			// skip
		default:
			out = append(out, metaKV{path: prefix, val: fmt.Sprintf("%v", t)})
		}
	}
	walk("", meta)
	return out
}
