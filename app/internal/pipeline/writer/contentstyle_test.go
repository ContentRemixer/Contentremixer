// ContentStyle CR style resolution (ISI-7965 W2): the writer prefers the CR
// source, falls back to the repo file, and fails closed only when neither
// yields. The resolver runs against a fake client (the W1 webhook test
// pattern); the Writer runs against stub resolvers and the fixture tree.
package writer

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	contentremixerv1alpha1 "github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
	"github.com/ContentRemixer/Contentremixer/app/internal/pipeline"
)

const crTestNamespace = "crtest"

func newContentStyle(name, format, guide string) *contentremixerv1alpha1.ContentStyle {
	return &contentremixerv1alpha1.ContentStyle{
		TypeMeta:   metav1.TypeMeta{APIVersion: "contentremixer.io/v1alpha1", Kind: "ContentStyle"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: crTestNamespace},
		Spec: contentremixerv1alpha1.ContentStyleSpec{
			OutputFormat: format,
			Guide:        guide,
		},
	}
}

func fakeStyleReader(objs ...client.Object) client.Reader {
	scheme := runtime.NewScheme()
	if err := contentremixerv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// --- ContentStyleResolver contract ---

func TestContentStyleResolverResolvesByOutputFormat(t *testing.T) {
	r := &ContentStyleResolver{Reader: fakeStyleReader(
		newContentStyle("blog-style", FormatBlog, "# Blog guide\n\nCR GUIDE: live from the Booth"),
		newContentStyle("short-style", FormatShort, "# Short guide"),
	), Namespace: crTestNamespace}
	sg, err := r.ResolveStyleGuide(context.Background(), blogRunContext(), FormatBlog)
	if err != nil {
		t.Fatalf("ResolveStyleGuide: %v", err)
	}
	if !strings.Contains(sg.Guide, "CR GUIDE: live from the Booth") {
		t.Fatalf("guide = %q, want the CR guide body", sg.Guide)
	}
	if want := "contentstyle:" + crTestNamespace + "/blog-style"; sg.Source != want {
		t.Fatalf("Source = %q, want %q", sg.Source, want)
	}
}

func TestContentStyleResolverNoMatchIsNoContentStyle(t *testing.T) {
	r := &ContentStyleResolver{Reader: fakeStyleReader(newContentStyle("blog-style", FormatBlog, "guide")), Namespace: crTestNamespace}
	_, err := r.ResolveStyleGuide(context.Background(), blogRunContext(), FormatTutorial)
	if !errors.Is(err, ErrNoContentStyle) {
		t.Fatalf("error = %v, want ErrNoContentStyle", err)
	}
}

func TestContentStyleResolverEmptyGuideDegrades(t *testing.T) {
	r := &ContentStyleResolver{Reader: fakeStyleReader(newContentStyle("blog-style", FormatBlog, "   \n\t")), Namespace: crTestNamespace}
	_, err := r.ResolveStyleGuide(context.Background(), blogRunContext(), FormatBlog)
	if !errors.Is(err, ErrNoContentStyle) {
		t.Fatalf("error = %v, want ErrNoContentStyle (empty guide degrades, never fails)", err)
	}
}

func TestContentStyleResolverDuplicatePicksDeterministically(t *testing.T) {
	r := &ContentStyleResolver{Reader: fakeStyleReader(
		newContentStyle("zzz-style", FormatBlog, "zzz guide"),
		newContentStyle("aaa-style", FormatBlog, "aaa guide"),
	), Namespace: crTestNamespace}
	sg, err := r.ResolveStyleGuide(context.Background(), blogRunContext(), FormatBlog)
	if err != nil {
		t.Fatalf("ResolveStyleGuide: %v", err)
	}
	if !strings.Contains(sg.Guide, "aaa guide") || !strings.Contains(sg.Source, "aaa-style") {
		t.Fatalf("duplicate formats must resolve by name order, got guide %q source %q", sg.Guide, sg.Source)
	}
}

func TestContentStyleResolverRequiresWiring(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *ContentStyleResolver
	}{
		{"nil reader", &ContentStyleResolver{Namespace: crTestNamespace}},
		{"empty namespace", &ContentStyleResolver{Reader: fakeStyleReader()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.r.ResolveStyleGuide(context.Background(), blogRunContext(), FormatBlog)
			if !errors.Is(err, ErrNoContentStyle) {
				t.Fatalf("error = %v, want ErrNoContentStyle", err)
			}
		})
	}
}

// --- Writer integration: CR first, repo fallback, fail-closed if neither ---

func TestWriteUsesContentStyleGuide(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft, Model: "m"}, nil)
	w := newWriter(gen)
	w.Styles = &ContentStyleResolver{Reader: fakeStyleReader(
		newContentStyle("blog-style", FormatBlog, "CR GUIDE: live from the Booth"),
	), Namespace: crTestNamespace}
	mix, err := w.Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(last.System, "CR GUIDE: live from the Booth") {
		t.Fatalf("system prompt must carry the CR guide body:\n%s", last.System)
	}
	if !strings.Contains(last.System, "--- contentstyle:"+crTestNamespace+"/blog-style ---") {
		t.Fatalf("system prompt must name the CR source:\n%s", last.System)
	}
	if strings.Contains(last.System, "--- styles/blog.md ---") {
		t.Fatalf("repo style header must not appear when the CR served the run:\n%s", last.System)
	}
	if want := "contentstyle:" + crTestNamespace + "/blog-style"; mix.Meta["style_source"] != want {
		t.Fatalf("Meta.style_source = %v, want %q", mix.Meta["style_source"], want)
	}
}

func TestWriteFallsBackToRepoWhenNoContentStyle(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft}, nil)
	w := newWriter(gen)
	w.Styles = &ContentStyleResolver{Reader: fakeStyleReader(), Namespace: crTestNamespace}
	mix, err := w.Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("Write (CR miss must fall back to the repo file): %v", err)
	}
	if !strings.Contains(last.System, "--- repo:styles/blog.md ---") {
		t.Fatalf("system prompt must name the repo fallback source:\n%s", last.System)
	}
	if !strings.Contains(last.System, "Title with the problem in it") {
		t.Fatalf("repo style guide must serve the run:\n%s", last.System)
	}
	if mix.Meta["style_source"] != "repo:styles/blog.md" {
		t.Fatalf("Meta.style_source = %v, want repo:styles/blog.md", mix.Meta["style_source"])
	}
}

func TestWriteFallsBackOnResolverError(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft}, nil)
	w := newWriter(gen)
	w.Styles = StyleResolverFunc(func(context.Context, pipeline.RunContext, string) (StyleGuide, error) {
		return StyleGuide{}, errors.New("apiserver down")
	})
	mix, err := w.Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("resolver error must degrade to the repo file, got %v", err)
	}
	if !strings.Contains(last.System, "Title with the problem in it") {
		t.Fatalf("repo style guide must serve the run:\n%s", last.System)
	}
	if mix.Meta["style_source"] != "repo:styles/blog.md" {
		t.Fatalf("Meta.style_source = %v, want repo:styles/blog.md", mix.Meta["style_source"])
	}
}

func TestWriteFailsClosedWhenNeitherYields(t *testing.T) {
	okGen := GeneratorFunc(func(context.Context, GenerateRequest) (GenerateResponse, error) {
		return GenerateResponse{Content: "# T\n\nb"}, nil
	})
	w := newWriter(okGen)
	w.Styles = &ContentStyleResolver{Reader: fakeStyleReader(), Namespace: crTestNamespace}
	_, err := w.Write(context.Background(), pipeline.RunContext{OutputFormat: FormatTutorial}, teleprompterSample())
	if err == nil {
		t.Fatal("expected fail-closed error when neither CR nor repo yields a guide")
	}
	if !strings.Contains(err.Error(), "no ContentStyle") || !strings.Contains(err.Error(), "reading styles/tutorial.md") {
		t.Fatalf("error should name both failed sources, got: %v", err)
	}
}

func TestWriteWithoutResolverKeepsPhase1Output(t *testing.T) {
	last, gen := captureGen(GenerateResponse{Content: blogDraft}, nil)
	mix, err := newWriter(gen).Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(last.System, "--- styles/blog.md ---") {
		t.Fatalf("unwired writer keeps the literal repo header:\n%s", last.System)
	}
	if _, ok := mix.Meta["style_source"]; ok {
		t.Fatalf("unwired writer must not emit style_source: %+v", mix.Meta)
	}
}

func TestWriteResolverEmptySourceDefaultsLabel(t *testing.T) {
	_, gen := captureGen(GenerateResponse{Content: blogDraft}, nil)
	w := newWriter(gen)
	w.Styles = StyleResolverFunc(func(context.Context, pipeline.RunContext, string) (StyleGuide, error) {
		return StyleGuide{Guide: "CR GUIDE: live from the Booth"}, nil
	})
	mix, err := w.Write(context.Background(), blogRunContext(), teleprompterSample())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if mix.Meta["style_source"] != "contentstyle" {
		t.Fatalf("Meta.style_source = %v, want the generic contentstyle label", mix.Meta["style_source"])
	}
}
