package main

import (
	"strings"
	"testing"
)

// setEnv sets the scrape-job env from a map for one test and clears it after.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func baseEnv() map[string]string {
	return map[string]string{
		"SCRAPE_JOB_ID":        "job-1",
		"SCRAPE_OWNER_SUBJECT": "booth:local:alice",
		"SCRAPE_PERSONA_NAME":  "alice",
		"SCRAPE_SOURCE_URL":    "https://example.com/x",
		"SCRAPE_SOURCE_KIND":   "article",
		"REVIEW_DSN":           "postgres://x",
	}
}

func TestReadEnvArticleOK(t *testing.T) {
	setEnv(t, baseEnv())
	e, err := readEnv()
	if err != nil {
		t.Fatalf("readEnv: %v", err)
	}
	if e.sourceKind != "article" || e.sourceText != "" {
		t.Errorf("unexpected env: %+v", e)
	}
}

func TestReadEnvTeleprompterRequiresText(t *testing.T) {
	env := baseEnv()
	env["SCRAPE_SOURCE_KIND"] = "teleprompter-script"
	env["SCRAPE_SOURCE_URL"] = "teleprompter://alice-talk"
	setEnv(t, env)

	// Without SCRAPE_SOURCE_TEXT the no-fetch kind must fail closed.
	if _, err := readEnv(); err == nil || !strings.Contains(err.Error(), "SCRAPE_SOURCE_TEXT") {
		t.Fatalf("want SCRAPE_SOURCE_TEXT error, got %v", err)
	}

	// With the body present it passes and carries the inline text.
	t.Setenv("SCRAPE_SOURCE_TEXT", "Hello, this is my talk script.")
	e, err := readEnv()
	if err != nil {
		t.Fatalf("readEnv with text: %v", err)
	}
	if e.sourceText != "Hello, this is my talk script." {
		t.Errorf("sourceText = %q", e.sourceText)
	}
}

func TestReadEnvUnsupportedKind(t *testing.T) {
	env := baseEnv()
	env["SCRAPE_SOURCE_KIND"] = "podcast"
	setEnv(t, env)
	if _, err := readEnv(); err == nil || !strings.Contains(err.Error(), "unsupported source kind") {
		t.Fatalf("want unsupported-kind error, got %v", err)
	}
}
