package pr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/checker"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

const (
	testRepo  = "ContentRemixer/Contentremixer"
	testPAT   = "ghp_testtoken"
	testIssue = 42
	baseSHA   = "b45e000000000000000000000000000000000001"
)

const testCommit = "3ae0a89b00000000000000000000000000000000"

// ---- fake GitHub ----

type recordedCall struct {
	Method string
	Path   string
	Body   map[string]any
}

// fakeGitHub implements just enough of the REST surface the assembler uses.
type fakeGitHub struct {
	mu    sync.Mutex
	calls []recordedCall

	branchExists bool // POST /git/refs answers 422 (branch already there)
	prExists     bool // POST /pulls answers 422 (PR already open)

	blobCount     int
	commitCount   int
	existingPRNo  int
	existingPRURL string
}

func (f *fakeGitHub) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := recordedCall{Method: r.Method, Path: r.URL.Path}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&call.Body)
	}
	f.calls = append(f.calls, call)

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/git/ref/heads/main"):
		_, _ = fmt.Fprintf(w, `{"object":{"sha":%q}}`, baseSHA)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/ref/heads/"):
		_, _ = fmt.Fprintf(w, `{"object":{"sha":"reuse00000000000000000000000000000000000002"}}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git/refs"):
		if f.branchExists {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = fmt.Fprint(w, `{"message":"Reference already exists"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"ref":"refs/heads/x"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git/blobs"):
		f.blobCount++
		_, _ = fmt.Fprintf(w, `{"sha":"blob%d"}`, f.blobCount)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/git/commits/"):
		_, _ = fmt.Fprint(w, `{"tree":{"sha":"basetree"}}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git/trees"):
		_, _ = fmt.Fprint(w, `{"sha":"newtree"}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/git/commits"):
		f.commitCount++
		_, _ = fmt.Fprintf(w, `{"sha":"commit%d"}`, f.commitCount)
	case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/git/refs/heads/"):
		_, _ = fmt.Fprint(w, `{"object":{"sha":"updated"}}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pulls"):
		if f.prExists {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = fmt.Fprint(w, `{"message":"A pull request already exists for ..."}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprint(w, `{"number":7,"html_url":"https://github.com/ContentRemixer/Contentremixer/pull/7"}`)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls"):
		_, _ = fmt.Fprintf(w, `[{"number":%d,"html_url":%q}]`, f.existingPRNo, f.existingPRURL)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"message":"fake: unexpected %s %s"}`, r.Method, r.URL.Path)
	}
}

func (f *fakeGitHub) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeGitHub) countCalls(pathSuffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c.Path, pathSuffix) || strings.HasSuffix(c.Path, pathSuffix) {
			n++
		}
	}
	return n
}

func (f *fakeGitHub) lastCall(method, pathPart string) (recordedCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		c := f.calls[i]
		if c.Method == method && strings.Contains(c.Path, pathPart) {
			return c, true
		}
	}
	return recordedCall{}, false
}

// ---- fixtures ----

func runContext() pipeline.RunContext {
	return pipeline.RunContext{
		Repo:         testRepo,
		IssueNumber:  testIssue,
		SourcePath:   "content/videos/2026/demo-video",
		InputFormat:  "auto",
		OutputFormat: "blog",
		Notes:        "operator audience",
		ModelName:    "",
	}
}

func teleprompterSample() pipeline.Sample {
	return pipeline.Sample{
		SourcePath:    "content/videos/2026/demo-video",
		SourceCommit:  testCommit,
		DetectedInput: "teleprompter",
		Meta: map[string]any{
			"title":  "Demo video",
			"author": "henrik",
			"tags":   []any{"kubernetes", "secrets"},
		},
		Body: "Welcome back. Today: how secrets end up in git, and the boring fix that actually works.",
	}
}

func cleanMix() pipeline.Mix {
	return pipeline.Mix{
		OutputFormat: "blog",
		Title:        "Kubernetes secrets keep leaking into git — here is the boring fix",
		Markdown:     "# Kubernetes secrets keep leaking into git — here is the boring fix\n\nYou committed a token. It happens.\n\n## What to do\n\nRotate, then commit.\n",
		Meta: map[string]any{
			"title":         "Kubernetes secrets keep leaking into git — here is the boring fix",
			"output_format": "blog",
			"sample_clearance": map[string]any{
				"source_path":   "content/videos/2026/demo-video",
				"source_commit": testCommit,
				"model":         "anthropic/claude-opus-4",
			},
		},
		ModelUsed: "anthropic/claude-opus-4",
	}
}

// recordingComments captures issue comments instead of hitting GitHub.
type recordingComments struct {
	mu    sync.Mutex
	calls []struct {
		repo string
		num  int
		body string
	}
}

func (c *recordingComments) PostComment(_ context.Context, repo string, num int, _, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, struct {
		repo string
		num  int
		body string
	}{repo, num, body})
	return nil
}

func (c *recordingComments) bodies() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.calls))
	for _, call := range c.calls {
		out = append(out, call.body)
	}
	return out
}

// fixtureFS serves the testdata tree (styles) without a git dependency.
type fixtureFS struct {
	root string
}

func (f fixtureFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
}

// ---- slug / clearance rendering ----

func TestSlug(t *testing.T) {
	rc := runContext()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"folder source", "content/videos/2026/2026-11-04-otel-k8s/", "42-blog-2026-11-04-otel-k8s"},
		{"meta.yaml normalized away", "content/videos/2026/demo-video", "42-blog-demo-video"},
		{"standalone file", "content/videos/2026/loose/notes.md", "42-blog-notes"},
		{"url source", "https://www.youtube.com/watch?v=abc123", "42-blog-www-youtube-com-watch"},
		{"hostile chars", "content/videos/2026/Weird__Name!! é", "42-blog-weird-name"},
		{"empty", "", "42-blog-source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := teleprompterSample()
			s.SourcePath = tt.source
			if got := Slug(rc, s); got != tt.want {
				t.Fatalf("Slug(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestSlugStaysFileSystemSafe(t *testing.T) {
	s := teleprompterSample()
	s.SourcePath = "content/videos/2026/Super..Weird Name/.."
	got := Slug(runContext(), s)
	for _, r := range got {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			t.Fatalf("Slug %q contains unsafe rune %q", got, r)
		}
	}
}

func TestBranchNameDefaults(t *testing.T) {
	if got := BranchName(contentremixerv1alpha1.PRTargetSpec{}, "42-blog-demo-video"); got != "recycle/42-blog-demo-video" {
		t.Fatalf("BranchName default prefix = %q", got)
	}
	if got := BranchName(contentremixerv1alpha1.PRTargetSpec{BranchPrefix: "mix/"}, "42-blog-demo-video"); got != "mix/42-blog-demo-video" {
		t.Fatalf("BranchName custom prefix = %q", got)
	}
}

func TestClearanceBlockAndPRBody(t *testing.T) {
	rc, s, m := runContext(), teleprompterSample(), cleanMix()

	block := ClearanceBlock(rc, s, m)
	for _, want := range []string{
		"## Sample clearance\n",
		"- **Source path:** content/videos/2026/demo-video\n",
		"- **Source commit:** " + testCommit + "\n",
		"- **Model:** anthropic/claude-opus-4\n",
		"- **Input → Output:** teleprompter → blog\n",
		"- **Recycle issue:** #42\n",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("clearance block missing %q; got:\n%s", want, block)
		}
	}

	body := PRBody(rc, s, m)
	if !strings.Contains(body, "Closes #42") {
		t.Errorf("PR body must link back to the issue with Closes #42; got:\n%s", body)
	}
	if !strings.Contains(body, "## Sample clearance") {
		t.Errorf("PR body must embed the clearance block; got:\n%s", body)
	}
}

// ---- assembler ----

func TestAssembleOpensPRWithClearance(t *testing.T) {
	fake := &fakeGitHub{}
	srv := fake.server(t)
	a := &Assembler{GitHub: &GitHubClient{BaseURL: srv.URL}}

	target := contentremixerv1alpha1.PRTargetSpec{BaseBranch: "main", BranchPrefix: "recycle/", Draft: true}
	res, err := a.Assemble(context.Background(), runContext(), teleprompterSample(), cleanMix(), target, testPAT)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if res.Slug != "42-blog-demo-video" {
		t.Errorf("Slug = %q", res.Slug)
	}
	if res.Branch != "recycle/42-blog-demo-video" {
		t.Errorf("Branch = %q", res.Branch)
	}
	if res.PRNumber != 7 || res.PRURL == "" {
		t.Errorf("PR = #%d %q", res.PRNumber, res.PRURL)
	}

	// Branch created off the base SHA.
	ref, ok := fake.lastCall(http.MethodPost, "/git/refs")
	if !ok {
		t.Fatal("no branch creation call recorded")
	}
	if ref.Body["ref"] != "refs/heads/recycle/42-blog-demo-video" || ref.Body["sha"] != baseSHA {
		t.Errorf("branch ref call = %+v", ref.Body)
	}

	// Two blobs committed under recycled/<slug>/ with the Mix + meta content.
	blobCalls := 0
	fake.mu.Lock()
	bodies := map[string]string{}
	for _, c := range fake.calls {
		if strings.HasSuffix(c.Path, "/git/blobs") {
			blobCalls++
			bodies[c.Body["content"].(string)] = c.Body["encoding"].(string)
		}
	}
	fake.mu.Unlock()
	if blobCalls != 2 {
		t.Fatalf("expected 2 blobs (index.md + meta.yaml), got %d", blobCalls)
	}

	tree, ok := fake.lastCall(http.MethodPost, "/git/trees")
	if !ok {
		t.Fatal("no tree creation call recorded")
	}
	rawTree, _ := json.Marshal(tree.Body["tree"])
	for _, want := range []string{"recycled/42-blog-demo-video/index.md", "recycled/42-blog-demo-video/meta.yaml"} {
		if !strings.Contains(string(rawTree), want) {
			t.Errorf("tree missing %s: %s", want, rawTree)
		}
	}

	// Commit message references the issue and the source.
	cm, ok := fake.lastCall(http.MethodPost, "/git/commits")
	if !ok || !strings.Contains(cm.Body["message"].(string), "issue #42") {
		t.Errorf("commit message = %+v", cm.Body)
	}

	// meta.yaml blob content: Mix meta + exact sample_clearance anchor.
	var metaYAML string
	fake.mu.Lock()
	for _, c := range fake.calls {
		if strings.HasSuffix(c.Path, "/git/blobs") {
			if content, ok := c.Body["content"].(string); ok && strings.Contains(content, "sample_clearance") {
				metaYAML = content
			}
		}
	}
	fake.mu.Unlock()
	if metaYAML == "" {
		t.Fatal("no meta.yaml blob found")
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(metaYAML), &meta); err != nil {
		t.Fatalf("meta.yaml is not valid YAML: %v\n%s", err, metaYAML)
	}
	sc, _ := meta["sample_clearance"].(map[string]any)
	if sc == nil {
		t.Fatalf("meta.yaml has no sample_clearance map:\n%s", metaYAML)
	}
	if sc["source_commit"] != testCommit || sc["source_path"] != "content/videos/2026/demo-video" || sc["model"] != "anthropic/claude-opus-4" || sc["recycle_issue"] != 42 {
		t.Errorf("sample_clearance = %#v", sc)
	}

	// PR opened with clearance + issue link + draft flag.
	pr, ok := fake.lastCall(http.MethodPost, "/pulls")
	if !ok {
		t.Fatal("no PR creation call recorded")
	}
	if pr.Body["head"] != "recycle/42-blog-demo-video" || pr.Body["base"] != "main" {
		t.Errorf("PR head/base = %v -> %v", pr.Body["head"], pr.Body["base"])
	}
	if pr.Body["draft"] != true {
		t.Errorf("PR draft = %v, want true", pr.Body["draft"])
	}
	body, _ := pr.Body["body"].(string)
	for _, want := range []string{"Closes #42", "## Sample clearance", "- **Source commit:** " + testCommit, "- **Model:** anthropic/claude-opus-4", "- **Recycle issue:** #42"} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body missing %q; got:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "- **Source path:** content/videos/2026/demo-video") {
		t.Errorf("PR body missing source path; got:\n%s", body)
	}

	// Authorization used the PAT on every call.
	if fake.countCalls("/") == 0 {
		t.Fatal("no calls recorded")
	}
}

func TestAssembleFailClosedOnMissingClearance(t *testing.T) {
	fake := &fakeGitHub{}
	srv := fake.server(t)
	a := &Assembler{GitHub: &GitHubClient{BaseURL: srv.URL}}

	cases := map[string]func(*pipeline.Sample, *pipeline.Mix){
		"no source path":   func(s *pipeline.Sample, _ *pipeline.Mix) { s.SourcePath = "" },
		"no source commit": func(s *pipeline.Sample, _ *pipeline.Mix) { s.SourceCommit = "" },
		"no model":         func(_ *pipeline.Sample, m *pipeline.Mix) { m.ModelUsed = "" },
		"empty body":       func(_ *pipeline.Sample, m *pipeline.Mix) { m.Markdown = "  " },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, m := teleprompterSample(), cleanMix()
			mutate(&s, &m)
			if _, err := a.Assemble(context.Background(), runContext(), s, m, contentremixerv1alpha1.PRTargetSpec{}, testPAT); err == nil {
				t.Fatalf("expected fail-closed error")
			}
		})
	}
	if n := fake.countCalls("/"); n != 0 {
		t.Fatalf("fail-closed validation must precede any network call; got %d calls", n)
	}
}

func TestAssembleReusesExistingBranchAndPR(t *testing.T) {
	fake := &fakeGitHub{branchExists: true, prExists: true, existingPRNo: 5, existingPRURL: "https://github.com/ContentRemixer/Contentremixer/pull/5"}
	srv := fake.server(t)
	a := &Assembler{GitHub: &GitHubClient{BaseURL: srv.URL}}

	res, err := a.Assemble(context.Background(), runContext(), teleprompterSample(), cleanMix(), contentremixerv1alpha1.PRTargetSpec{}, testPAT)
	if err != nil {
		t.Fatalf("Assemble with pre-existing branch+PR: %v", err)
	}
	if res.PRNumber != 5 || res.PRURL != "https://github.com/ContentRemixer/Contentremixer/pull/5" {
		t.Errorf("expected existing PR #5 returned, got #%d %q", res.PRNumber, res.PRURL)
	}
	if fake.countCalls("/pulls?") == 0 && fake.countCalls("/pulls") < 2 {
		t.Errorf("expected a lookup of the existing PR")
	}
}

// ---- runner: full chain E+F(+G)+H against the fake GitHub ----

// stubAnalyzer returns a fixed Sample (clearance-ready).
type stubAnalyzer struct{ sample pipeline.Sample }

func (a stubAnalyzer) Analyze(context.Context, pipeline.RunContext) (pipeline.Sample, error) {
	return a.sample, nil
}

func newRunner(gen writer.Generator, comments IssueCommenter, srvURL string) *Runner {
	return &Runner{
		Analyzer: stubAnalyzer{teleprompterSample()},
		Writer: &writer.Writer{
			FS:       fixtureFS{root: "testdata"},
			Generate: gen,
		},
		Checker:   checker.New(),
		Assembler: &Assembler{GitHub: &GitHubClient{BaseURL: srvURL}},
		Comments:  comments,
	}
}

func TestRunnerEndOfPipelineOpensPR(t *testing.T) {
	fake := &fakeGitHub{}
	srv := fake.server(t)
	comments := &recordingComments{}
	runner := newRunner(writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{
			Content: "# The boring fix for leaking secrets\n\nRotate early.\n\n## Steps\n\nOne at a time.\n",
			Model:   "anthropic/claude-opus-4",
		}, nil
	}), comments, srv.URL)

	out, err := runner.Run(context.Background(), runContext(), contentremixerv1alpha1.PRTargetSpec{Draft: true}, testPAT)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Blocked {
		t.Fatalf("run unexpectedly blocked")
	}
	if out.PRNumber != 7 || out.Slug != "42-blog-demo-video" {
		t.Errorf("outcome = %+v", out)
	}

	// The generated Mix itself is what landed in the PR tree.
	tree, ok := fake.lastCall(http.MethodPost, "/git/trees")
	if !ok || !strings.Contains(fmt.Sprint(tree.Body["tree"]), "recycled/42-blog-demo-video/index.md") {
		t.Fatalf("PR tree missing the recycled content: %+v", tree.Body)
	}

	// The issue was told about the PR.
	bodies := comments.bodies()
	if len(bodies) == 0 || !strings.Contains(bodies[len(bodies)-1], "pull/7") {
		t.Errorf("expected a PR-opened issue comment, got %v", bodies)
	}
}

func TestRunnerBlockedOpensNoPR(t *testing.T) {
	fake := &fakeGitHub{}
	srv := fake.server(t)
	comments := &recordingComments{}
	// The generator "leaks" a token and an internal IP: Mastering must fail
	// closed (real checker, real rules).
	runner := newRunner(writer.GeneratorFunc(func(context.Context, writer.GenerateRequest) (writer.GenerateResponse, error) {
		return writer.GenerateResponse{
			Content: "# Leaky draft\n\nKey sk-proj-0123456789abcdefghijklmnop and host 10.0.0.5 in prose.\n",
			Model:   "anthropic/claude-opus-4",
		}, nil
	}), comments, srv.URL)

	out, err := runner.Run(context.Background(), runContext(), contentremixerv1alpha1.PRTargetSpec{}, testPAT)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Blocked {
		t.Fatalf("expected the run to be blocked by Mastering")
	}
	if out.PRURL != "" || out.PRNumber != 0 {
		t.Fatalf("blocked run must not open a PR: %+v", out)
	}
	if n := fake.countCalls("/pulls"); n != 0 {
		t.Fatalf("blocked run must not touch the PR API; got %d calls", n)
	}
	if n := fake.countCalls("/git/"); n != 0 {
		t.Fatalf("blocked run must not mutate git; got %d calls", n)
	}

	bodies := comments.bodies()
	if len(bodies) != 1 {
		t.Fatalf("expected exactly one blocked comment, got %d", len(bodies))
	}
	blocked := bodies[0]
	for _, want := range []string{"blocked by Mastering", "no PR was opened", "`token`", "`ip`"} {
		if !strings.Contains(blocked, want) {
			t.Errorf("blocked comment missing %q; got:\n%s", want, blocked)
		}
	}
	if strings.Contains(blocked, "sk-proj-0123456789abcdefghijklmnop") {
		t.Errorf("blocked comment must not re-leak the caught token:\n%s", blocked)
	}
}
