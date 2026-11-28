# ISI-7970 — Approval → PR publish (Part 2 / C3)

Author: Winston (Architect) · Status: implementing · Parent: ISI-7887 (Part 2)

Make PR creation the **terminal action of in-product approval**. A reviewed
`ContentDraft` (the `booth.contents` row from C1 / ISI-7968) is approved by a
human through the dual-approval gate; that approval — and only that approval —
commits the draft body to its target repo/path, opens exactly one PR, records
the PR link on the draft and on the origin GitHub issue, and moves the draft to
`published`. No PR is ever created without a human approval.

This note is the authoritative contract for the three adjacent workstreams that
meet here: C2 (ISI-7969, deposit) feeds the draft; C4 (ISI-7972, review UX)
drives the gate; C3 (this issue) owns the publish action.

## 1. Where the commit/PR step lives — booth-native (decision)

C3 "reuses the relocated commit/PR step (from C2)". Across the module boundary
that is realized **booth-native**, not by importing the app pipeline:

- `booth` is a separate Go module and cannot import `app/internal/pipeline/pr`
  (the Sample/Mix `Assembler`). It already talks to GitHub directly for the
  ISI-7153 approval reflection (`booth/internal/approve/github.go`), so the
  publish step follows the same precedent and lives beside it.
- A `ContentDraft` publish is strictly simpler than the Assembler: the draft
  already carries `Body`, `TargetRepo`, `TargetPath`, and `Origin`. There is no
  slug-from-source derivation and no Sample/Mix. One file, one commit, one PR.
- **C2 therefore does NOT build a booth publisher.** C2's acceptance ("the
  relocated commit/PR logic is preserved for C3, not deleted") is satisfied by
  leaving `app/internal/pipeline/pr.Assembler` intact and no longer calling it
  at recycle terminus. C3 owns the booth-side publisher (`approve.GitHubPublisher`).

New booth component: `approve.GitHubPublisher` (`Publisher` interface) —
`refSHA → createBlob → createTree → createCommit → updateRef → createPull`,
idempotent ("already exists = reuse"), mirroring `pr/github.go`'s discipline.

## 2. Credential — token-based, App-token ready

The publisher is token-based exactly like `GitHubReflector`: it takes a Bearer
token. Today booth resolves a per-repo PAT (`BOOTH_GITHUB_TOKEN`); the ISI-7836
GitHub-App installation token is a drop-in Bearer replacement (see
`app/internal/githubapp/appauth` — installation tokens are used identically to
a PAT downstream). C3 does not block on the App path (ISI-7836 is owner-gated on
App creation); it consumes whatever Bearer token booth is wired with, and the
switch to installation tokens is a wiring change in `cmd/booth/main.go`, not a
code change in the publisher.

## 3. The scrub gate — C3↔C4 contract (implemented in C3)

> Acceptance: "Approve is refused while unresolved scrub findings remain
> (resolved or explicitly waived)."

Gate predicate (shipped with C3, colocated with the model in `app/content`):

```go
func (f ScrubFinding) Blocks() bool { return !f.Waived }
func UnresolvedFindings(findings []ScrubFinding) []ScrubFinding // every unwaived finding
```

Contract, fail-closed:

- **Every unwaived finding blocks publish, regardless of severity.** A fresh
  finding of any severity (`block`, `warn`, anything) gates the action. This is
  the inverse of C1's original silent hard-block: nothing is dropped, and
  nothing is published over.
- Two — and only two — ways past the gate:
  1. **Resolved** = the finding is removed from the slice. C2/C4 re-scrub a
     fixed body and the finding no longer appears.
  2. **Waived** = `ScrubFinding.Waived = true` with `WaivedBy = <subject>`, an
     explicit, attributable keep-but-accept decision.
- `Waived`/`WaivedBy` are **additive JSONB fields** on `ScrubFinding`
  (`scrub_findings` round-trips the whole struct) — no migration, pre-ISI-7970
  rows read back unwaived (and therefore blocking, which is the safe default).

**C4 owns the UX** that lets a reviewer resolve (re-scrub / edit) or waive
(set `Waived`+`WaivedBy`, ideally through the existing `ContentPatch.ScrubFindings`
replace seam so the §6.4 audit records who waived). C3 only reads the gate.

## 4. The publish action — sequence

New action in `booth/internal/approve` (reuses the §6.3 anchor): approve a
**draft** (by content id), not an existing PR number. Order (gate → publish →
backfill → comment → status):

1. **Resolve + authorize** the draft via the content gating store
   `GetContent(ctx, sess, id)` — this is the §6.3 owner gate (DevRel may publish
   only their own persona's draft; Admin any). 404/403 map through unchanged.
2. **State guard**: the draft must be `in_review`/`approved` with a non-empty
   `TargetRepo` and `TargetPath` (C2's deposit obligation — see §6). Missing
   target → 400, never a guessed path.
3. **Scrub gate**: `UnresolvedFindings(draft.ScrubFindings)` must be empty, else
   refuse with a typed `ErrBlocked` → **409 Conflict** carrying the blocking
   findings. This is the "approve is refused while unresolved findings remain"
   acceptance.
4. **Publish** via `Publisher.Publish(repo, {Path, Body, Title, PRBody,
   OriginIssue})` → commit + one PR (idempotent: re-publish reuses the branch
   and the open PR, so a double-approve never opens a second PR).
5. **Backfill** the draft through the content `Backfill(id, {PRURL, Status:
   published})` system seam.
6. **Comment** the PR link onto the origin issue (step 4 does this as a SOFT
   step — a flaky issue comment must not blackhole an already-opened PR).
7. **Audit** one §6.4 `approve`/`publish` entry (allow after success; deny on
   gate refusal or publish fault).

Idempotency: the publisher's deterministic branch name + "already-open PR =
reuse" makes step 4 safe to retry; step 5 is a column patch; the terminal
`published` status makes a repeat call a no-op publish.

Endpoint: `POST /v1/content/{id}/publish` (behind `protect`, mounted only when
both the approve store and content store are wired). Distinct from the existing
`POST /v1/approve` (which reflects onto an existing PR, ISI-7153). Both share the
§6.3 anchor and the audit vocabulary.

## 5. Acceptance → mechanism

| Acceptance | Mechanism |
|---|---|
| Approving a reviewed draft opens exactly one PR, link on draft + origin issue | §4 steps 4–6; idempotent publisher |
| No PR is ever created without a human approval | publish is only reachable through the §6.3-gated approve action; recycle terminus no longer opens PRs (C2) |
| Approve refused while unresolved scrub findings remain | §3 gate → 409 before any GitHub call |

## 6. Obligations on the adjacent workstreams

- **C2 (ISI-7969)** must: (a) stop opening a PR at recycle terminus; (b) deposit
  drafts with `TargetRepo`+`TargetPath` populated (else C3 fail-closes at §4.2);
  (c) record scrub results as `ScrubFindings` annotations (unwaived ⇒ blocking),
  not a silent hard-block; (d) leave `pr.Assembler` intact (not deleted).
- **C4 (ISI-7972)** must: surface `ScrubFindings` in the review UX and provide
  resolve (re-scrub/edit) and explicit **waive** (`Waived`+`WaivedBy`) actions,
  and invoke `POST /v1/content/{id}/publish` as the approve button's terminal
  call.

## 7. Open follow-ups (not blocking C3)

- Switch booth's publish credential from PAT to the ISI-7836 App installation
  token once App creation lands (wiring-only).
- Preserve full Sample-clearance for *recycled* drafts in the published PR body:
  C1's model carries `Origin{repo,issue}` but not source-commit/model. If
  clearance parity with the old Assembler PR is required, C2 should stamp those
  onto the draft (additive) and C3's `PRBody` will render them. Tracked as a C2
  enrichment, not a C3 blocker.
