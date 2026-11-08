package analyzer

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// sha40 matches a full git object name (lowercase hex, 40 chars).
var sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Commit implements SourceFS by reading the checkout's git refs directly,
// with NO dependency on a `git` binary. The operator image is distroless
// (gcr.io/distroless/static:nonroot) and ships no git, so shelling out to
// `git rev-parse HEAD` fails on every run (ISI-7239); resolving HEAD from the
// plumbing files instead keeps the minimal base image and still yields the
// Sample.SourceCommit the §5 clearance block requires.
//
// It resolves the standard layouts an init-container `git clone` produces:
//
//   - detached HEAD (shallow clone of a pinned SHA): .git/HEAD holds the SHA;
//   - symbolic HEAD ("ref: refs/heads/<branch>"): resolved from the loose ref
//     file (.git/refs/...) or, if the clone packed its refs, .git/packed-refs.
func (l LocalFS) Commit() (string, error) {
	gitDir, err := l.gitDir()
	if err != nil {
		return "", err
	}

	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return "", fmt.Errorf("analyzer: reading HEAD in %s: %w", gitDir, err)
	}
	headStr := strings.TrimSpace(string(head))

	// Detached HEAD: the file is the commit SHA itself.
	if sha40.MatchString(headStr) {
		return headStr, nil
	}

	// Symbolic HEAD: "ref: refs/heads/<branch>".
	ref, ok := strings.CutPrefix(headStr, "ref:")
	if !ok {
		return "", fmt.Errorf("analyzer: unrecognized HEAD %q in %s", headStr, gitDir)
	}
	ref = strings.TrimSpace(ref)

	sha, err := resolveRef(gitDir, ref)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// gitDir finds the checkout's git directory. Like `git rev-parse`, it walks up
// from Root until it finds a `.git` entry, so pointing Root at a subdirectory
// of a checkout still resolves. The entry is normally a directory but is a
// "gitdir: <path>" pointer file inside linked worktrees.
func (l LocalFS) gitDir() (string, error) {
	start, err := filepath.Abs(l.Root)
	if err != nil {
		return "", fmt.Errorf("analyzer: resolving root %q: %w", l.Root, err)
	}
	dotGit := ""
	for dir := start; ; {
		candidate := filepath.Join(dir, ".git")
		if _, statErr := os.Stat(candidate); statErr == nil {
			dotGit = candidate
			break
		} else if !os.IsNotExist(statErr) {
			return "", fmt.Errorf("analyzer: locating .git from %s: %w", start, statErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("analyzer: no .git found from %s up to the filesystem root", start)
		}
		dir = parent
	}

	info, err := os.Stat(dotGit)
	if err != nil {
		return "", fmt.Errorf("analyzer: locating .git in %s: %w", l.Root, err)
	}
	if info.IsDir() {
		return dotGit, nil
	}
	// Worktree/submodule: .git is a file "gitdir: <path>".
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return "", fmt.Errorf("analyzer: reading .git pointer in %s: %w", dotGit, err)
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", fmt.Errorf("analyzer: unrecognized .git pointer %q in %s", strings.TrimSpace(string(b)), dotGit)
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dotGit), target)
	}
	return target, nil
}

// resolveRef returns the SHA a ref name points at, trying the loose ref file
// first and falling back to packed-refs (a fresh clone often packs its refs).
func resolveRef(gitDir, ref string) (string, error) {
	loose, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err == nil {
		sha := strings.TrimSpace(string(loose))
		if sha40.MatchString(sha) {
			return sha, nil
		}
		return "", fmt.Errorf("analyzer: ref %s in %s is not a 40-hex SHA: %q", ref, gitDir, sha)
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("analyzer: reading ref %s in %s: %w", ref, gitDir, err)
	}

	// Fall back to packed-refs: lines are "<sha> <refname>" (peeled tags carry
	// a following "^<sha>" line, which we skip since the first field is enough).
	packed, err := os.Open(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("analyzer: ref %s has no loose or packed entry in %s: %w", ref, gitDir, err)
	}
	defer packed.Close()

	scanner := bufio.NewScanner(packed)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref && sha40.MatchString(fields[0]) {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("analyzer: scanning packed-refs in %s: %w", gitDir, err)
	}
	return "", fmt.Errorf("analyzer: ref %s not found in %s (loose or packed)", ref, gitDir)
}
