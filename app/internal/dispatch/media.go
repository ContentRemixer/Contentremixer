package dispatch

import (
	"context"
	"errors"
	"fmt"
)

// ErrMediaNotWired is returned by MediaStrategy until the media Job launcher
// child of ISI-7389 lands. Exported and typed so callers and tests can assert
// it with errors.Is rather than string-matching.
var ErrMediaNotWired = errors.New("media actor template not yet wired: the k8s-Job launcher lands in the media child of ISI-7389")

// MediaStrategy is the typed stub for the "media" actor template (demo-recorder,
// video-designer). It reports the k8s-Job runtime it will run on (P0 target per
// ADR-1) but fails closed with ErrMediaNotWired until the launcher child wires
// the Job. Keeping it a registered-but-stubbed strategy means a recycle:demo /
// recycle:video delivery gets a clear "not yet wired" error on the run worker
// instead of a nil-strategy dispatch failure.
type MediaStrategy struct{}

// Runtime reports the k8s-Job backend the media template targets at P0.
func (MediaStrategy) Runtime() Runtime { return RuntimeK8sJob }

// Run fails closed until the media Job launcher lands.
func (MediaStrategy) Run(_ context.Context, wo WorkOrder) error {
	return fmt.Errorf("skill %q (%s#%d): %w", wo.Skill, wo.Repo, wo.IssueNumber, ErrMediaNotWired)
}
