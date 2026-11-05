# ISI-7085 — E2E test summary (Phase 1 subtask J)

**Gate:** the original ticket's criterion #1 — a `recycle:blog` label on a
teleprompter source opens a correctly-styled blog PR with Sample-clearance —
passes end-to-end through the whole chain.

**File:** `app/internal/githubapp/e2e_recycle_test.go`
**Framework:** Go `testing` + `net/http/httptest` (matches the repo's existing
stdlib test style; no new deps). Runs under `make test` / `go test ./...` in
`operator-ci`'s `test` job — no real GitHub or LLM required.

## What it exercises

The Server + Runner are wired **exactly as `cmd/main.go`** wires them
(analyzer → writer(LiteLLM) → checker → PR assembler), with the two external
dependencies faked at their real seams (every client takes a `BaseURL`, so no
production code path is bypassed):

- **GitHub REST** — one `httptest` server records the ack comment, the full
  git-data write sequence (ref → branch → blobs → tree → commit → ref), the
  opened PR, and the run-report comment.
- **LiteLLM gateway** — one `httptest` server returns a canned draft and echoes
  the served model (→ `Mix.ModelUsed` → Sample-clearance).
- **Reference-repo checkout** — a real on-disk `git init` repo so
  `analyzer.LocalFS` resolves the source commit via `git rev-parse HEAD`;
  the gate proves Sample-clearance carries the **actual** source SHA.

The run is driven by one HMAC-signed `issues`/`labeled` delivery.

## Tests (both PASS, 100%)

| Test | ID | Asserts |
|---|---|---|
| Happy path | `TestE2E_RecycleBlogOnTeleprompterSource_OpensStyledBlogPR` | model called through `styles/blog.md` (style marker in system prompt) with the teleprompter body; source analyzed as `teleprompter`; PR opened `main` ← `recycle/77-blog-otel-demo`; title is a blog recycle for #77; PR body has `Closes #77`, `## Sample clearance`, source path, **source SHA**, model, `teleprompter → blog`; committed `index.md` is the styled draft and `meta.yaml` carries `sample_clearance` (source_commit, model, `output_format: blog`); ack + opened-PR comments posted |
| Fail-closed | `TestE2E_RecycleBlog_MasteringBlocksLeak_NoPR` | a draft leaking a `ghp_…` PAT is blocked by Mastering: **no PR opened, no blobs committed**; a `blocked by Mastering` / `no PR was opened` comment is posted; evidence is redacted (raw secret never re-leaks); the `token` rule is named |

## Run

```
cd app
KUBEBUILDER_ASSETS="$(pwd)/bin/k8s/1.31.0-linux-amd64" go test ./... # full suite green
go test ./internal/githubapp/ -run TestE2E -v                       # the gate
```

## Scope note

Per design §10 and the ISI-7084 handoff, **real-GitHub wiring is ISI-7112**;
this E2E mocks GitHub + the LLM so it is deterministic and CI-runnable. It
validates the full shipped pipeline wiring, not the live cluster credentials.
