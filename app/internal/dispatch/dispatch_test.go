package dispatch

import (
	"context"
	"errors"
	"testing"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

// fakeStrategy records the order it ran and returns a fixed runtime/error.
type fakeStrategy struct {
	runtime Runtime
	err     error
	ran     *WorkOrder
}

func (f *fakeStrategy) Runtime() Runtime { return f.runtime }
func (f *fakeStrategy) Run(_ context.Context, wo WorkOrder) error {
	f.ran = &wo
	return f.err
}

func TestDispatch_SelectsStrategyByActorTemplate(t *testing.T) {
	content := &fakeStrategy{runtime: RuntimeInline}
	media := &fakeStrategy{runtime: RuntimeK8sJob}
	d := New(map[string]Strategy{
		contentremixerv1alpha1.ActorTemplateContent: content,
		contentremixerv1alpha1.ActorTemplateMedia:   media,
	})

	rt, err := d.Dispatch(context.Background(), WorkOrder{ActorTemplate: contentremixerv1alpha1.ActorTemplateMedia, Skill: "video-designer"})
	if err != nil {
		t.Fatalf("Dispatch: unexpected error: %v", err)
	}
	if rt != RuntimeK8sJob {
		t.Fatalf("runtime = %q, want %q", rt, RuntimeK8sJob)
	}
	if media.ran == nil {
		t.Fatal("media strategy was not run")
	}
	if content.ran != nil {
		t.Fatal("content strategy ran for a media template")
	}
}

func TestDispatch_UnknownTemplateIsError(t *testing.T) {
	d := New(map[string]Strategy{
		contentremixerv1alpha1.ActorTemplateContent: &fakeStrategy{runtime: RuntimeInline},
	})
	rt, err := d.Dispatch(context.Background(), WorkOrder{ActorTemplate: "substrate-only", Repo: "o/r", IssueNumber: 7})
	if err == nil {
		t.Fatal("expected error for unregistered template, got nil")
	}
	if rt != "" {
		t.Fatalf("runtime should be empty on dispatch error, got %q", rt)
	}
}

func TestDispatch_PropagatesRunError(t *testing.T) {
	sentinel := errors.New("boom")
	d := New(map[string]Strategy{
		contentremixerv1alpha1.ActorTemplateContent: &fakeStrategy{runtime: RuntimeInline, err: sentinel},
	})
	rt, err := d.Dispatch(context.Background(), WorkOrder{ActorTemplate: contentremixerv1alpha1.ActorTemplateContent})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
	// Runtime is still reported even when the run fails (for logging).
	if rt != RuntimeInline {
		t.Fatalf("runtime = %q, want %q", rt, RuntimeInline)
	}
}
