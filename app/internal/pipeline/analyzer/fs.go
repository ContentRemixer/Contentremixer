package analyzer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SourceFS abstracts read access to the reference repository checkout (the
// content/ tree and the source meta.yaml files) plus the commit it sits at.
// The analyzer only ever reads; git mutation belongs to the PR assembler
// (subtask H). The abstraction keeps the analyzer testable against fixture
// trees and lets the run worker (subtask D) supply whatever checkout it owns.
type SourceFS interface {
	// ReadFile returns the contents of the repo-relative file name.
	ReadFile(name string) ([]byte, error)
	// Commit returns the git SHA the checkout is at (Sample.SourceCommit,
	// design §5 Sample-clearance).
	Commit() (string, error)
}

// LocalFS is a SourceFS over a local repository checkout rooted at Root
// (the workspace / mounted-checkout case used in Phase 1).
type LocalFS struct {
	// Root is the absolute or relative path of the repository root
	// (the directory containing content/, styles/, ...).
	Root string
}

// ReadFile implements SourceFS. It rejects paths that would escape Root.
func (l LocalFS) ReadFile(name string) ([]byte, error) {
	clean := filepath.Clean("/" + filepath.ToSlash(name))
	rootAbs, err := filepath.Abs(l.Root)
	if err != nil {
		return nil, fmt.Errorf("analyzer: resolving root %q: %w", l.Root, err)
	}
	abs := filepath.Join(rootAbs, filepath.FromSlash(clean))
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return nil, fmt.Errorf("analyzer: path %q escapes the repository root", name)
	}
	return os.ReadFile(abs)
}

// Commit implements SourceFS via `git rev-parse HEAD` in the checkout.
func (l LocalFS) Commit() (string, error) {
	out, err := exec.Command("git", "-C", l.Root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("analyzer: git rev-parse HEAD in %s: %w", l.Root, err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("analyzer: git rev-parse HEAD in %s returned empty commit", l.Root)
	}
	return sha, nil
}
