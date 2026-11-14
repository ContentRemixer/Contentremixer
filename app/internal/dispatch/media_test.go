package dispatch

import (
	"context"
	"errors"
	"testing"
)

func TestMediaStrategy_Runtime(t *testing.T) {
	if got := (MediaStrategy{}).Runtime(); got != RuntimeK8sJob {
		t.Fatalf("Runtime() = %q, want %q", got, RuntimeK8sJob)
	}
}

func TestMediaStrategy_FailsClosedUntilWired(t *testing.T) {
	err := MediaStrategy{}.Run(context.Background(), WorkOrder{Skill: "demo-recorder", Repo: "o/r", IssueNumber: 9})
	if !errors.Is(err, ErrMediaNotWired) {
		t.Fatalf("err = %v, want wrap of ErrMediaNotWired", err)
	}
}
