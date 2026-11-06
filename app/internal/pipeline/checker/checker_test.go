package checker

import (
	"context"
	"strings"
	"testing"

	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

// cleanDraft is a realistic dev-content Mix with NO secrets. It deliberately
// includes shapes that look secret-ish but are not — a k8s semver ("1.31.0"),
// a 40-hex git SHA, a C++/Rust scope operator ("std::vector"), and a clock
// ("10:30:45") — to prove the block matchers do not fire on clean prose
// (acceptance: "clean drafts pass").
func cleanDraft() pipeline.Mix {
	return pipeline.Mix{
		OutputFormat: "blog",
		Title:        "Kubernetes secrets keep leaking into git",
		Markdown: strings.Join([]string{
			"# Kubernetes secrets keep leaking into git",
			"",
			"We shipped on Kubernetes 1.31.0 and learned a few things.",
			"The build pinned commit 3ae0a89b00000000000000000000000000000000.",
			"In C++ you would reach for std::vector here; the daily standup is at 10:30:45.",
			"Rotate early, rotate often.",
		}, "\n"),
		Meta: map[string]any{
			"title":         "Kubernetes secrets keep leaking into git",
			"output_format": "blog",
			"source": map[string]any{
				"path":         "content/blog/demo-post",
				"commit":       "3ae0a89b00000000000000000000000000000000",
				"input_format": "blog",
			},
			"tags":          []any{"kubernetes", "secrets"},
			"recycle_issue": 7082,
		},
	}
}

func TestCleanDraftPasses(t *testing.T) {
	res, err := New().Check(context.Background(), cleanDraft())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, v := range res.Violations {
		if v.Severity == pipeline.SeverityBlock {
			t.Errorf("clean draft produced a block violation: %+v", v)
		}
	}
	if !res.Passed {
		t.Fatalf("clean draft did not pass: %+v", res.Violations)
	}
}

// TestPlantedSecretsBlocked drives the acceptance criterion: each planted
// secret / tenant id / IP / internal hostname blocks the run.
func TestPlantedSecretsBlocked(t *testing.T) {
	cases := []struct {
		name     string
		planted  string
		wantRule string
	}{
		{"tenant-uuid", "Our tenant is 615e9564-65df-4469-9b19-a5afb73446c6 today.", RuleTenantID},
		{"labelled-tenant-id", "tenant_id: acme-prod-4821 is the one.", RuleTenantID},
		{"openai-key", "Set OPENAI_API_KEY=sk-abcdef0123456789ABCDEF now.", RuleToken},
		{"github-pat", "token ghp_0123456789abcdef0123456789abcdef0123 leaked.", RuleToken},
		{"jwt", "Authorization uses eyJhbGciOiJod_zzz.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4.", RuleToken},
		{"aws-key", "AWS key AKIAIOSFODNN7EXAMPLE committed.", RuleToken},
		{"bearer", "curl -H 'Authorization: Bearer abcDEF0123456789ghiJKL' api", RuleToken},
		{"rfc1918-ipv4", "ssh into 10.0.12.34 to debug.", RuleIP},
		{"public-ipv4", "DNS resolver 8.8.8.8 responded.", RuleIP},
		{"ipv6-loopback", "bound to ::1 on startup.", RuleIP},
		{"ipv6-linklocal", "neighbor fe80::1ff:fe23:4567:890a appeared.", RuleIP},
		{"host-local", "reach db.prod.local from the pod.", RuleInternalHostname},
		{"host-svc", "curl http://payments.default.svc.cluster.local/health", RuleInternalHostname},
		{"host-internal", "the registry at registry.corp.internal is down.", RuleInternalHostname},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := cleanDraft()
			m.Markdown += "\n\n" + tc.planted
			res, err := New().Check(context.Background(), m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Passed {
				t.Fatalf("planted %q did not block; violations=%+v", tc.planted, res.Violations)
			}
			if !hasRule(res.Violations, tc.wantRule, pipeline.SeverityBlock) {
				t.Fatalf("expected block rule %q, got %+v", tc.wantRule, res.Violations)
			}
		})
	}
}

// TestSecretInMetaBlocked proves scanning covers Mix.Meta, not just Markdown
// (design §4: run on Markdown *and* Meta).
func TestSecretInMetaBlocked(t *testing.T) {
	m := cleanDraft()
	src := m.Meta["source"].(map[string]any)
	src["path"] = "content/blog/demo-post ghp_0123456789abcdef0123456789abcdef0123"
	res, err := New().Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Fatalf("secret in meta did not block: %+v", res.Violations)
	}
	var where string
	for _, v := range res.Violations {
		if v.Rule == RuleToken {
			where = v.Where
		}
	}
	if !strings.HasPrefix(where, "meta.") {
		t.Fatalf("expected a meta.* Where for the meta secret, got %q", where)
	}
}

// TestEvidenceRedacted proves the raw secret never survives into Violation.Match
// (it is surfaced in a public issue comment).
func TestEvidenceRedacted(t *testing.T) {
	secret := "ghp_0123456789abcdef0123456789abcdef0123"
	m := cleanDraft()
	m.Markdown += "\n\nleaked " + secret
	res, _ := New().Check(context.Background(), m)
	for _, v := range res.Violations {
		if strings.Contains(v.Match, secret) {
			t.Fatalf("redaction leaked the raw secret in Match: %q", v.Match)
		}
	}
}

// TestBrandVoiceWarnDoesNotBlock proves warn severity is advisory only.
func TestBrandVoiceWarnDoesNotBlock(t *testing.T) {
	m := cleanDraft()
	m.Markdown += "\n\nThis revolutionary, best-in-class, game-changing platform is seamless."
	res, err := New().Check(context.Background(), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("brand-voice warn flipped Passed to false: %+v", res.Violations)
	}
	if !hasRule(res.Violations, RuleBrandVoice, pipeline.SeverityWarn) {
		t.Fatalf("expected a brand-voice warn, got %+v", res.Violations)
	}
}

func hasRule(vs []pipeline.Violation, rule, severity string) bool {
	for _, v := range vs {
		if v.Rule == rule && v.Severity == severity {
			return true
		}
	}
	return false
}
