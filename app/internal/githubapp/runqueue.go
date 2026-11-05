// Route running (subtask-D hook, subtask-H execution): the delivery handler
// resolves routes and acks; matched routes are then enqueued for a run
// worker instead of executing the pipeline inline (design §4: "the webhook
// handler enqueues a RunContext; a run worker calls Analyze → Write →
// Check"). GitHub expects a delivery response in seconds — the analyzer and
// the model call take far longer.
package githubapp

import (
	"context"
	"strings"
	"sync"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// runQueueDepth bounds in-flight runs. When full, the delivery is answered
// 503 so GitHub retries it rather than silently dropping work.
const runQueueDepth = 16

// RouteRequest is one enqueued recycle run: everything the pipeline needs,
// resolved and verified at enqueue time (route formats, PAT, PR target, and
// the source path / notes parsed from the issue form body).
type RouteRequest struct {
	// Repo is the GitHub "owner/name" (from the ReferenceRepo spec).
	Repo string
	// IssueNumber is the originating recycle issue.
	IssueNumber int
	// Token is the per-repo PAT (already resolved from the Secret).
	Token string
	// Route is the resolved label → format pair that triggered the run.
	Route Route
	// SourcePath is the content/ path or external URL from the issue form
	// ("" when the form is unfilled — the pipeline reports that on the issue).
	SourcePath string
	// Notes is the free-text angle/audience guidance from the issue form.
	Notes string
	// PRTarget mirrors the ReferenceRepo spec.prTarget.
	PRTarget contentremixerv1alpha1.PRTargetSpec
}

// RouteRunner executes one enqueued route end-to-end (the analyzer → writer
// → checker → PR-assembly pipeline, subtask H). Wired in cmd/main.go.
type RouteRunner interface {
	RunRoute(ctx context.Context, req RouteRequest) error
}

// runQueue is a bounded FIFO drained by a single worker goroutine: one
// pipeline run at a time keeps model calls and git mutation serialized.
type runQueue struct {
	runner RouteRunner
	ch     chan RouteRequest

	mu      sync.Mutex
	started bool
}

func newRunQueue(runner RouteRunner) *runQueue {
	return &runQueue{runner: runner, ch: make(chan RouteRequest, runQueueDepth)}
}

// start launches the worker goroutine; safe to call once (subsequent calls
// are no-ops so tests can start the queue before serving).
func (q *runQueue) start(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.started {
		return
	}
	q.started = true
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req := <-q.ch:
				// Runs outlive the delivery request but not the server:
				// the worker context is cancelled on shutdown.
				if err := q.runner.RunRoute(ctx, req); err != nil {
					logf.FromContext(ctx).Error(err, "recycle run failed", "repo", req.Repo, "issue", req.IssueNumber, "label", req.Route.Label)
				}
			}
		}
	}()
}

// enqueue offers a run to the worker. It returns false when the queue is
// full — the caller should answer 503 so GitHub redelivers.
func (q *runQueue) enqueue(req RouteRequest) bool {
	select {
	case q.ch <- req:
		return true
	default:
		return false
	}
}

// issueFormBody is the minimal issue-form parse the run needs: the values of
// the "Source content path" input and the "Notes for the agent" textarea.
// GitHub issue forms render sections as `### <label>` headings followed by
// the value until the next heading. Headings are matched by prefix so
// small wording changes in the template do not break routing.
func issueFormBody(body string) (sourcePath, notes string) {
	sections := map[string]string{}
	var current string
	var b strings.Builder
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimSpace(b.String())
		}
		b.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "### ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	flush()
	for heading, value := range sections {
		switch {
		case strings.HasPrefix(heading, "Source content path"):
			sourcePath = value
		case strings.HasPrefix(heading, "Notes for the agent"):
			notes = value
		}
	}
	return sourcePath, notes
}
