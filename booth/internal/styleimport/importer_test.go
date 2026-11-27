package styleimport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ContentRemixer/Contentremixer/app/api/v1alpha1"
)

const ns = "content-remixer"

func newClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ContentStyle{}).Build()
}

func writeStyles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var fourStyles = map[string]string{
	"blog.md":         "# Blog post style guide\n- rule one\n- rule two\n",
	"short.md":        "# YouTube Short style guide\n- rule\n",
	"tutorial.md":     "# GitHub tutorial style guide\n- rule\n",
	"teleprompter.md": "# Teleprompter script style guide\n- rule\n",
}

func TestImportCreatesEveryFormat(t *testing.T) {
	c := newClient(t)
	dir := writeStyles(t, fourStyles)

	res, err := Import(context.Background(), c, ns, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Created: 4}) {
		t.Fatalf("want 4 created, got %+v", res)
	}
	for name, body := range fourStyles {
		format := strings.TrimSuffix(name, ".md")
		var got v1alpha1.ContentStyle
		if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: format}, &got); err != nil {
			t.Fatalf("get %s: %v", format, err)
		}
		if got.Spec.OutputFormat != format {
			t.Errorf("%s: outputFormat = %q", format, got.Spec.OutputFormat)
		}
		if got.Spec.Guide != strings.TrimSpace(body) {
			t.Errorf("%s: guide mismatch", format)
		}
	}
	var blog v1alpha1.ContentStyle
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "blog"}, &blog); err != nil {
		t.Fatal(err)
	}
	if blog.Spec.Description != "Blog post style guide" {
		t.Errorf("description = %q, want the first H1 text", blog.Spec.Description)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	c := newClient(t)
	dir := writeStyles(t, fourStyles)
	ctx := context.Background()

	if _, err := Import(ctx, c, ns, dir, nil); err != nil {
		t.Fatal(err)
	}
	var before v1alpha1.ContentStyle
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "blog"}, &before); err != nil {
		t.Fatal(err)
	}

	res, err := Import(ctx, c, ns, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Unchanged: 4}) {
		t.Fatalf("re-run should change nothing, got %+v", res)
	}
	var after v1alpha1.ContentStyle
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "blog"}, &after); err != nil {
		t.Fatal(err)
	}
	if after.ResourceVersion != before.ResourceVersion {
		t.Errorf("re-run should not touch the object (rv %s -> %s)",
			before.ResourceVersion, after.ResourceVersion)
	}
}

func TestImportUpdatesDriftedGuide(t *testing.T) {
	c := newClient(t)
	dir := writeStyles(t, fourStyles)
	ctx := context.Background()

	if _, err := Import(ctx, c, ns, dir, nil); err != nil {
		t.Fatal(err)
	}
	// The repo guide changes: the repo stays the source of truth until the
	// cutover, so the importer must follow it.
	if err := os.WriteFile(filepath.Join(dir, "blog.md"),
		[]byte("# Blog post style guide\n- NEW rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Import(ctx, c, ns, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Updated: 1, Unchanged: 3}) {
		t.Fatalf("want 1 updated / 3 unchanged, got %+v", res)
	}
	var blog v1alpha1.ContentStyle
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "blog"}, &blog); err != nil {
		t.Fatal(err)
	}
	if blog.Spec.Guide != "# Blog post style guide\n- NEW rule" {
		t.Errorf("guide should follow the repo file, got %q", blog.Spec.Guide)
	}
}

func TestImportMissingFileCountsAndContinues(t *testing.T) {
	c := newClient(t)
	dir := writeStyles(t, map[string]string{
		"blog.md":  "# B\n- r\n",
		"short.md": "# S\n- r\n",
	})

	res, err := Import(context.Background(), c, ns, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{Created: 2, Missing: 2}) {
		t.Fatalf("want 2 created / 2 missing, got %+v", res)
	}
}

func TestImportBadDirIsAnError(t *testing.T) {
	c := newClient(t)
	if _, err := Import(context.Background(), c, ns, t.TempDir(), nil); err == nil {
		t.Fatal("empty dir should be an error")
	}
	if _, err := Import(context.Background(), c, ns, filepath.Join(t.TempDir(), "nope"), nil); err == nil {
		t.Fatal("missing dir should be an error")
	}
}

func TestImportDisabledIsNoop(t *testing.T) {
	c := newClient(t)
	res, err := Import(context.Background(), c, ns, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res != (Result{}) {
		t.Fatalf("disabled import must be a no-op, got %+v", res)
	}
}
