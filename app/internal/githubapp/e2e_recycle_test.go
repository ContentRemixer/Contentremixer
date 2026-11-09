package githubapp

// End-to-end gate for Phase 1 (ISI-7085, subtask J; design §9 row J):
//
//	a `recycle:blog` label on a TELEPROMPTER source must drive the WHOLE chain
//	— webhook ack → enqueue → analyzer → writer → checker → PR assembly — and
//	open a correctly-STYLED blog PR carrying the mandatory Sample-clearance
//	(source path + source commit SHA + model, design §5).
//
// This is "end-to-end against the deployed app" with the two external
// dependencies faked at their real seams (the clients all take a BaseURL, so
// nothing about the production code path is bypassed):
//
//   - GitHub REST API — one httptest server records the ack comment, the full
//     git-data write sequence (ref → branch → blobs → tree → commit → ref),
//     the opened PR, and the run-report comment.
//   - LiteLLM gateway — one httptest server returns a canned, clean blog draft
//     and echoes the served model (→ Mix.ModelUsed → Sample-clearance).
//
// The reference-repo checkout is a real on-disk git repo (analyzer.LocalFS
// needs `git rev-parse HEAD` for the source commit), so Sample-clearance is
// proven to carry the actual source SHA, not a stub.
//
// The Server + Runner are wired EXACTLY as cmd/main.go wires them (analyzer +
// writer(LiteLLM) + checker + PR assembler), so a green here is a green for
// the shipped binary.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/litellm"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/analyzer"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/checker"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/pr"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline/writer"
)

// e2eIssueNumber is the recycle issue the gate drives.
const e2eIssueNumber = 77

// e2eModel is the model the mocked gateway echoes; it must survive into the
// Sample-clearance block as Mix.ModelUsed (design §5 mandatory field).
const e2eModel = "homelab/llama-3.3-70b"

// styleMarker is a sentinel planted in the fixture styles/blog.md; its
// presence in the model's system prompt proves the draft was generated
// *through* styles/blog.md (the "styled" in "styled-blog PR").
const styleMarker = "STYLE-GUIDE-BLOG-MARKER-7085"

// e2eBlogDraft is the clean blog Mix the mocked gateway returns. It leads with
// the H1 title the writer requires, carries no secrets/IPs/internal hostnames
// (so Mastering passes closed → a PR is actually opened), and recycles the
// teleprompter source's substance.
const e2eBlogDraft = `# Running OpenTelemetry on Kubernetes

Shipping traces from a Kubernetes cluster is still harder than it should be.
This post walks through the moving parts from the demo.

## The problem

Teams bolt on the OpenTelemetry Collector and then wonder why spans never
arrive. The usual culprit is exporter wiring, not the instrumentation.

## What we learned

Start the Collector as a DaemonSet, keep the pipeline config in one place, and
verify the exporter endpoint before touching application code.
`

// --- mock GitHub REST API ---------------------------------------------------

// recordedGitHub captures every GitHub REST call the chain makes so the test
// can assert on the ack, the committed files, and the opened PR.
type recordedGitHub struct {
	mu          sync.Mutex
	comments    []string // issue-comment bodies (ack + run report)
	blobs       []string // created blob contents, in creation order
	pullTitle   string
	pullBody    string
	pullHead    string
	pullBase    string
	pullOpened  bool
	branchRefs  []string // refs POSTed to /git/refs (branch creation)
	baseHeadSHA string
}

// newMockGitHub returns an httptest server emulating just enough of the git
// data + pulls + issue-comments API for the assembler and the commenters.
func newMockGitHub(t *testing.T) (*httptest.Server, *recordedGitHub) {
	t.Helper()
	rec := &recordedGitHub{baseHeadSHA: "baseheadsha0000000000000000000000000000"}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		readJSON := func() map[string]any {
			var m map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &m)
			return m
		}
		writeJSON := func(code int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}

		rec.mu.Lock()
		defer rec.mu.Unlock()

		switch {
		// Resolve base (or branch) ref → commit SHA.
		case r.Method == http.MethodGet && strings.Contains(path, "/git/ref/heads/"):
			writeJSON(http.StatusOK, map[string]any{"object": map[string]any{"sha": rec.baseHeadSHA}})

		// Create the recycle branch.
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/git/refs"):
			body := readJSON()
			if ref, _ := body["ref"].(string); ref != "" {
				rec.branchRefs = append(rec.branchRefs, ref)
			}
			writeJSON(http.StatusCreated, map[string]any{"ref": body["ref"]})

		// Read a commit's tree.
		case r.Method == http.MethodGet && strings.Contains(path, "/git/commits/"):
			writeJSON(http.StatusOK, map[string]any{"tree": map[string]any{"sha": "basetreesha000000000000000000000000000000"}})

		// Create a blob (one per written file).
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/git/blobs"):
			body := readJSON()
			content, _ := body["content"].(string)
			rec.blobs = append(rec.blobs, content)
			writeJSON(http.StatusCreated, map[string]any{"sha": fmt.Sprintf("blobsha%08d0000000000000000000000000", len(rec.blobs))})

		// Create the tree.
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/git/trees"):
			writeJSON(http.StatusCreated, map[string]any{"sha": "newtreesha0000000000000000000000000000000"})

		// Create the commit.
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/git/commits"):
			writeJSON(http.StatusCreated, map[string]any{"sha": "newcommitsha00000000000000000000000000000"})

		// Advance the branch ref.
		case r.Method == http.MethodPatch && strings.Contains(path, "/git/refs/heads/"):
			writeJSON(http.StatusOK, map[string]any{"object": map[string]any{"sha": "newcommitsha00000000000000000000000000000"}})

		// Open the pull request.
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/pulls"):
			body := readJSON()
			rec.pullOpened = true
			rec.pullTitle, _ = body["title"].(string)
			rec.pullBody, _ = body["body"].(string)
			rec.pullHead, _ = body["head"].(string)
			rec.pullBase, _ = body["base"].(string)
			writeJSON(http.StatusCreated, map[string]any{
				"number":   101,
				"html_url": "https://github.com/ContentRemixer/Contentremixer/pull/101",
			})

		// Issue comments (ack + run report).
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/comments"):
			body := readJSON()
			s, _ := body["body"].(string)
			rec.comments = append(rec.comments, s)
			writeJSON(http.StatusCreated, map[string]any{"id": len(rec.comments)})

		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, path)
			writeJSON(http.StatusNotFound, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// --- mock LiteLLM gateway ---------------------------------------------------

// recordedLiteLLM captures the single chat-completion the writer makes.
type recordedLiteLLM struct {
	mu     sync.Mutex
	called bool
	system string
	user   string
}

func newMockLiteLLM(t *testing.T, draft string) (*httptest.Server, *recordedLiteLLM) {
	t.Helper()
	rec := &recordedLiteLLM{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != litellm.ChatPath {
			t.Errorf("unexpected LiteLLM path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			Messages []litellm.ChatMessage `json:"messages"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)

		rec.mu.Lock()
		rec.called = true
		for _, m := range req.Messages {
			switch m.Role {
			case "system":
				rec.system = m.Content
			case "user":
				rec.user = m.Content
			}
		}
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": e2eModel,
			"choices": []any{
				map[string]any{"message": map[string]any{"role": "assistant", "content": draft}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// --- fixture reference-repo checkout ---------------------------------------

// writeCheckout materializes a real git repo with styles/blog.md and a
// teleprompter source under content/videos/, and returns (root, headSHA).
func writeCheckout(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	write("styles/blog.md", "# Blog post style guide\n"+styleMarker+"\n"+
		"- Title states the problem; intro states the pain in two sentences\n"+
		"- H2 sections; end with \"what we learned\"\n"+
		"- Tone: practitioner to practitioner, no marketing voice\n")
	write("content/videos/2026/otel-demo/meta.yaml", "title: \"OTel on Kubernetes (demo)\"\n"+
		"author: henrik\n"+
		"recorded_date: \"2026-11-01\"\n"+
		"tags: [otel, k8s]\n"+
		"files:\n  teleprompter: teleprompter.md\n")
	write("content/videos/2026/otel-demo/teleprompter.md", "# Teleprompter\n\n"+
		"Welcome back. Today we wire the OpenTelemetry Collector on Kubernetes,\n"+
		"run it as a DaemonSet, and verify the exporter endpoint before touching\n"+
		"any application code.\n")

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("add", "-A")
	run("commit", "-q", "-m", "fixture checkout")

	shaOut, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return root, strings.TrimSpace(string(shaOut))
}

// e2eRouteRunner mirrors cmd/main.go's recycleRunner: it rebuilds the
// RunContext from the enqueue request and runs the pipeline end-to-end.
type e2eRouteRunner struct{ runner *pr.Runner }

func (a e2eRouteRunner) RunRoute(ctx context.Context, req RouteRequest) error {
	rc := pipeline.RunContext{
		Repo:         req.Repo,
		IssueNumber:  req.IssueNumber,
		SourcePath:   req.SourcePath,
		InputFormat:  req.Route.InputFormat,
		OutputFormat: req.Route.OutputFormat,
		Notes:        req.Notes,
	}
	_, err := a.runner.Run(ctx, rc, req.PRTarget, req.Token)
	return err
}

// newE2EServer builds the webhook Server with the pipeline wired EXACTLY as
// cmd/main.go wires it (analyzer + writer(LiteLLM) + checker + PR assembler),
// every external client pointed at the mocks, and the run-queue worker started
// (its context is cancelled via t.Cleanup). The fake cluster carries the
// starter ReferenceRepo (recycle:blog → auto → blog) and its HMAC + PAT
// secrets, exactly as the webhook reads them.
func newE2EServer(t *testing.T, checkout, ghURL, llmURL string) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := contentremixerv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	repo := starterRepo()
	repo.ObjectMeta = metav1.ObjectMeta{Name: "crate", Namespace: testNS}
	repo.Spec.WebhookSecretRef = contentremixerv1alpha1.SecretKeyRef{Name: "github-webhook", Key: "hmac"}
	repo.Spec.TokenRef = contentremixerv1alpha1.SecretKeyRef{Name: "github-pat", Key: "token"}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		repo,
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "github-webhook", Namespace: testNS}, Data: map[string][]byte{"hmac": []byte(testSecret)}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "github-pat", Namespace: testNS}, Data: map[string][]byte{"token": []byte(testPAT)}},
	).Build()

	sourceFS := &analyzer.LocalFS{Root: checkout}
	runner := &pr.Runner{
		Analyzer: &analyzer.Analyzer{FS: sourceFS},
		Writer: &writer.Writer{
			FS:       sourceFS,
			Generate: writer.LiteLLMGenerator(&litellm.Client{BaseURL: llmURL}),
		},
		Checker:   checker.New(),
		Assembler: &pr.Assembler{GitHub: &pr.GitHubClient{BaseURL: ghURL}},
		Comments:  &GitHubClient{BaseURL: ghURL},
	}
	s := &Server{
		Client: cl,
		Poster: &GitHubClient{BaseURL: ghURL},
		Runner: e2eRouteRunner{runner: runner},
	}

	// Start the run-queue worker (Start() would also bind a socket; the handler
	// path only needs the queue running).
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.runs = newRunQueue(s.Runner)
	s.runs.start(ctx)
	return s
}

// e2eLeakDraft is a blog draft that leaks a GitHub PAT — a hard-scrub `token`
// block violation (checker §4). Mastering must fail it closed: NO PR, a
// blocked-run comment, and the redacted evidence never re-leaks the secret.
const e2eLeakDraft = `# Running OpenTelemetry on Kubernetes

Export your traces by setting the token below in the Collector config:

    export GH_TOKEN=ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA

That is all it takes.
`

// TestE2E_RecycleBlog_MasteringBlocksLeak_NoPR drives the SAME chain with a
// draft that leaks a secret: the fail-closed half of the gate — Mastering
// blocks, the run opens NO PR, and the issue gets a redacted blocked comment.
func TestE2E_RecycleBlog_MasteringBlocksLeak_NoPR(t *testing.T) {
	ghServer, gh := newMockGitHub(t)
	llmServer, _ := newMockLiteLLM(t, e2eLeakDraft)
	checkout, _ := writeCheckout(t)
	s := newE2EServer(t, checkout, ghServer.URL, llmServer.URL)

	issueBody := "### Source content path\n\ncontent/videos/2026/otel-demo\n"
	payload := map[string]any{
		"action": "labeled",
		"issue": map[string]any{
			"number": e2eIssueNumber,
			"body":   issueBody,
			"labels": []any{map[string]any{"name": "recycle:blog"}},
		},
		"repository": map[string]any{"full_name": "ContentRemixer/Contentremixer"},
	}
	body, _ := json.Marshal(payload)

	rec := post(t, s, "issues", sign(body, testSecret), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("delivery status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// The blocked-run comment is posted after the ack; wait for the second
	// comment (ack is first) rather than racing the async worker.
	waitFor(t, 5*time.Second, func() bool {
		gh.mu.Lock()
		defer gh.mu.Unlock()
		return len(gh.comments) >= 2
	}, "no blocked-run comment was posted")

	gh.mu.Lock()
	defer gh.mu.Unlock()

	if gh.pullOpened {
		t.Fatal("a PR was opened despite a block-severity Mastering violation (fail-open!)")
	}
	if len(gh.blobs) != 0 {
		t.Errorf("files were committed despite the block: %d blobs", len(gh.blobs))
	}
	joined := strings.Join(gh.comments, "\n---\n")
	if !strings.Contains(joined, "blocked by Mastering") || !strings.Contains(joined, "no PR was opened") {
		t.Errorf("no fail-closed blocked comment:\n%s", joined)
	}
	// The evidence must be redacted — the raw PAT must never reach the comment.
	if strings.Contains(joined, "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Errorf("blocked comment re-leaked the raw secret:\n%s", joined)
	}
	if !strings.Contains(joined, "token") {
		t.Errorf("blocked comment did not name the token rule:\n%s", joined)
	}
}

// TestE2E_RecycleBlogOnTeleprompterSource_OpensStyledBlogPR is the ISI-7085
// acceptance gate: the whole chain, driven by one signed `recycle:blog`
// delivery on a teleprompter source, opens a styled blog PR with clearance.
func TestE2E_RecycleBlogOnTeleprompterSource_OpensStyledBlogPR(t *testing.T) {
	ghServer, gh := newMockGitHub(t)
	llmServer, llm := newMockLiteLLM(t, e2eBlogDraft)
	checkout, sourceSHA := writeCheckout(t)
	s := newE2EServer(t, checkout, ghServer.URL, llmServer.URL)

	// A GitHub `issues` `labeled` delivery: recycle:blog, with the issue-form
	// "Source content path" pointing at the teleprompter source.
	issueBody := "### Source content path\n\ncontent/videos/2026/otel-demo\n\n" +
		"### Notes for the agent\n\nFor the platform-engineering audience.\n"
	payload := map[string]any{
		"action": "labeled",
		"issue": map[string]any{
			"number": e2eIssueNumber,
			"body":   issueBody,
			"labels": []any{map[string]any{"name": "recycle:blog"}},
		},
		"repository": map[string]any{"full_name": "ContentRemixer/Contentremixer"},
	}
	body, _ := json.Marshal(payload)

	rec := post(t, s, "issues", sign(body, testSecret), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("delivery status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// The run worker is async. Wait for the run's TERMINAL side-effect — the
	// opened-PR report comment — not just gh.pullOpened. The PR is opened inside
	// Assemble (sets pullOpened), but the "opened #N" report comment is posted a
	// step later (pr.Runner); gating only on pullOpened races that trailing
	// comment on a slow runner and the comment assertion below flakes.
	waitFor(t, 5*time.Second, func() bool {
		gh.mu.Lock()
		defer gh.mu.Unlock()
		return gh.pullOpened && strings.Contains(strings.Join(gh.comments, "\n"), "opened #101")
	}, "no opened-PR report comment was posted end-to-end")

	gh.mu.Lock()
	defer gh.mu.Unlock()
	llm.mu.Lock()
	defer llm.mu.Unlock()

	// 1. The model was called THROUGH styles/blog.md (styled) with the
	//    teleprompter body (right source analyzed).
	if !llm.called {
		t.Fatal("LiteLLM gateway was never called — the writer did not run")
	}
	if !strings.Contains(llm.system, styleMarker) {
		t.Errorf("model system prompt did not embed styles/blog.md (no %q):\n%s", styleMarker, llm.system)
	}
	if !strings.Contains(llm.system, "blog-format Mix") {
		t.Errorf("model was not asked for a blog-format Mix:\n%s", llm.system)
	}
	if !strings.Contains(llm.user, "OpenTelemetry Collector") {
		t.Errorf("model user prompt missing the teleprompter source body:\n%s", llm.user)
	}
	if !strings.Contains(llm.user, "Input format: teleprompter") {
		t.Errorf("source was not analyzed as teleprompter:\n%s", llm.user)
	}

	// 2. A styled blog PR was opened against main from the recycle branch.
	if gh.pullBase != "main" {
		t.Errorf("PR base = %q, want main", gh.pullBase)
	}
	if want := "recycle/77-blog-otel-demo"; gh.pullHead != want {
		t.Errorf("PR head = %q, want %q", gh.pullHead, want)
	}
	if !strings.Contains(gh.pullTitle, "blog") || !strings.Contains(gh.pullTitle, "#77") {
		t.Errorf("PR title not a blog recycle for issue 77: %q", gh.pullTitle)
	}

	// 3. The PR body links back to the issue AND carries the mandatory
	//    Sample-clearance (source path + source commit + model), design §5.
	for _, want := range []string{
		"Closes #77",
		"## Sample clearance",
		"content/videos/2026/otel-demo",
		sourceSHA,
		e2eModel,
		"teleprompter → blog",
	} {
		if !strings.Contains(gh.pullBody, want) {
			t.Errorf("PR body missing %q:\n%s", want, gh.pullBody)
		}
	}

	// 4. The committed files are the styled draft (index.md) and the clearance
	//    meta.yaml — the recycled/<slug>/ contract (design §5).
	indexMD, metaYAML := classifyBlobs(gh.blobs)
	if indexMD == "" || metaYAML == "" {
		t.Fatalf("expected an index.md + meta.yaml blob, got %d blobs", len(gh.blobs))
	}
	if !strings.HasPrefix(strings.TrimSpace(indexMD), "# Running OpenTelemetry on Kubernetes") {
		t.Errorf("index.md is not the styled blog draft:\n%s", indexMD)
	}
	for _, want := range []string{"sample_clearance:", "source_commit: " + sourceSHA, "output_format: blog", "model: " + e2eModel} {
		if !strings.Contains(metaYAML, want) {
			t.Errorf("recycled meta.yaml missing %q:\n%s", want, metaYAML)
		}
	}

	// 5. The issue was kept informed: the ack AND the opened-PR report.
	joined := strings.Join(gh.comments, "\n---\n")
	if !strings.Contains(joined, "picked up this issue") {
		t.Errorf("no ack comment was posted:\n%s", joined)
	}
	if !strings.Contains(joined, "opened #101") || !strings.Contains(joined, "Sample clearance") {
		t.Errorf("no opened-PR report comment was posted:\n%s", joined)
	}
}

// classifyBlobs splits the two committed blobs into (index.md, meta.yaml) by
// shape — index.md leads with an H1, meta.yaml is the clearance document.
func classifyBlobs(blobs []string) (indexMD, metaYAML string) {
	for _, b := range blobs {
		if strings.Contains(b, "sample_clearance:") {
			metaYAML = b
		} else if strings.HasPrefix(strings.TrimSpace(b), "#") {
			indexMD = b
		}
	}
	return indexMD, metaYAML
}

// waitFor polls cond until true or the deadline passes, failing with msg.
func waitFor(t *testing.T, d time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}
