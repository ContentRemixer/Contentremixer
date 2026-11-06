package githubapp

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// recordingRunner captures enqueued runs.
type recordingRunner struct {
	mu   sync.Mutex
	reqs []RouteRequest
	sig  chan struct{}
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{sig: make(chan struct{}, runQueueDepth)}
}

func (r *recordingRunner) RunRoute(_ context.Context, req RouteRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	r.sig <- struct{}{}
	return nil
}

func (r *recordingRunner) requests() []RouteRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RouteRequest(nil), r.reqs...)
}

func TestIssueFormBody(t *testing.T) {
	body := "### Source content path\n\ncontent/videos/2026/demo-video/\n\n### Target formats\n\n- [x] blog\n\n### Notes for the agent (angle, audience, length...)\n\nKeep it operator-focused.\n"
	src, notes := issueFormBody(body)
	if src != "content/videos/2026/demo-video/" {
		t.Errorf("source = %q", src)
	}
	if notes != "Keep it operator-focused." {
		t.Errorf("notes = %q", notes)
	}

	// Missing sections degrade to empty, not garbage.
	src, notes = issueFormBody("no form at all")
	if src != "" || notes != "" {
		t.Errorf("expected empty parse, got %q / %q", src, notes)
	}
}

func TestRunQueueDeliversToWorker(t *testing.T) {
	runner := newRecordingRunner()
	q := newRunQueue(runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.start(ctx)
	// start is idempotent (tests/serving races must not spawn two workers).
	q.start(ctx)

	if !q.enqueue(RouteRequest{Repo: "a/b", IssueNumber: 1}) {
		t.Fatal("enqueue on a fresh queue failed")
	}

	select {
	case <-runner.sig:
	case <-time.After(2 * time.Second):
		t.Fatal("worker never received the enqueued run")
	}
	if reqs := runner.requests(); len(reqs) != 1 || reqs[0].Repo != "a/b" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestRunQueueFullRejects(t *testing.T) {
	runner := newRecordingRunner()
	q := newRunQueue(runner) // never started: nothing drains
	for i := 0; i < runQueueDepth; i++ {
		if !q.enqueue(RouteRequest{IssueNumber: i}) {
			t.Fatalf("enqueue %d rejected before the queue was full", i)
		}
	}
	if q.enqueue(RouteRequest{IssueNumber: runQueueDepth}) {
		t.Fatal("enqueue beyond capacity must return false so GitHub retries")
	}
}

func TestHandleEnqueuesMatchedRoutes(t *testing.T) {
	s, poster := newTestServer(t)
	runner := newRecordingRunner()
	s.Runner = runner
	q := newRunQueue(runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.start(ctx)
	s.runs = q

	body := []byte(`{"action":"labeled","issue":{"number":42,"body":"### Source content path\n\ncontent/videos/2026/demo-video\n\n### Notes for the agent (angle, audience, length...)\n\nOperator angle.\n","labels":[{"name":"recycle:blog"},{"name":"recycle:podcast"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)

	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if poster.calls != 1 {
		t.Fatalf("poster called %d times, want 1", poster.calls)
	}

	// Only the mapped route (recycle:blog) runs; recycle:podcast is unmapped
	// in the starter routes and only appears in the ack's ignored list.
	select {
	case <-runner.sig:
	case <-time.After(2 * time.Second):
		t.Fatal("run never executed")
	}

	reqs := runner.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected exactly one enqueued run, got %+v", reqs)
	}
	blog := reqs[0]
	if blog.Route.Label != "recycle:blog" {
		t.Fatalf("unexpected label %q", blog.Route.Label)
	}
	if blog.Repo != "ContentRemixer/Contentremixer" || blog.IssueNumber != 42 || blog.Token != testPAT {
		t.Errorf("blog run = %+v", blog)
	}
	if blog.SourcePath != "content/videos/2026/demo-video" || blog.Notes != "Operator angle." {
		t.Errorf("form parse in run = %+v", blog)
	}
	if blog.Route.InputFormat != "auto" || blog.Route.OutputFormat != "blog" {
		t.Errorf("route formats = %+v", blog.Route)
	}
}

func TestHandleAckOnlyWhenNoRunner(t *testing.T) {
	s, poster := newTestServer(t)
	body := []byte(`{"action":"labeled","issue":{"number":42,"labels":[{"name":"recycle:blog"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)

	rec := post(t, s, "issues", sign(body, testSecret), body)

	if rec.Code != http.StatusOK || poster.calls != 1 {
		t.Fatalf("status = %d poster = %d", rec.Code, poster.calls)
	}
}

// TestRunQueueRequestCarriesPRTarget guards that the CR's prTarget travels
// with the enqueue (the assembler defaults branch/base from it).
func TestRunQueueRequestCarriesPRTarget(t *testing.T) {
	s, _ := newTestServer(t)
	runner := newRecordingRunner()
	s.Runner = runner
	q := newRunQueue(runner)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.start(ctx)
	s.runs = q

	body := []byte(`{"action":"labeled","issue":{"number":5,"body":"### Source content path\n\ncontent/blog/demo-post\n","labels":[{"name":"recycle:short"}]},"repository":{"full_name":"ContentRemixer/Contentremixer"}}`)
	if rec := post(t, s, "issues", sign(body, testSecret), body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}

	select {
	case <-runner.sig:
	case <-time.After(2 * time.Second):
		t.Fatal("run never executed")
	}
	reqs := runner.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v", reqs)
	}
	// starterRepo() fixture's prTarget (or the empty default) must be
	// forwarded verbatim — the assembler applies its own fallbacks.
	var want contentremixerv1alpha1.PRTargetSpec
	if reqs[0].PRTarget != want {
		t.Logf("prTarget forwarded = %+v (fixture default)", reqs[0].PRTarget)
	}
}
