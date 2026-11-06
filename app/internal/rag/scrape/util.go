package scrape

import (
	"context"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// nowFunc is the clock seam. Tests override it so FetchedAt is deterministic;
// production uses time.Now.
var nowFunc = time.Now

// logDropped records a bounded fan-out truncation. Design §3.1 requires "a log
// of what was dropped — no silent unbounded crawl"; we route it through the
// controller-runtime context logger the reconciler installs.
func logDropped(ctx context.Context, kind, url string, cap, dropped int) {
	logf.FromContext(ctx).Info("scrape: fan-out capped",
		"kind", kind, "url", url, "cap", cap, "dropped", dropped)
}
