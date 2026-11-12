# Identity link + DevRel-scoped persona API + first-run detection (D5, ISI-7360)

Status: **implemented**. This realizes the frozen D1 contract
([isi-7356-devrel-model-contract.md](isi-7356-devrel-model-contract.md) §6) and
the self-service persona path from the [ISI-7350 plan](/ISI/issues/ISI-7350#document-plan)
§1/§4. It touches only Booth (`booth/*`); no CRD or app-module change.

## 1. Stable `Subject` on `auth.User` (contract §6.1–6.3)

`auth.User` gains `Subject string` — the stable owner key a DevRel's personas
(and scrape jobs, content threads) are keyed on, distinct from `Username`.

- **Minting.** `auth.LocalSubject(username) = "booth:local:" + username`.
  `MemoryStore.Create` stamps it whenever the caller leaves `Subject` empty (the
  normal path — no caller chooses a subject), so every row satisfies the
  invariants with no separate backfill pass. The seed Admin (bootstrapped via
  `Bootstrap`) and every admin-created user flow through `Create`, so all are
  covered automatically.
- **Invariants held** (contract §6.3):
  - *Non-empty & stable*: `booth:local:<username>` is deterministic — a persona
    created in one session is still owned after a Booth restart.
  - *Unique*: usernames are the table's primary key, so derived subjects are too.
  - *Survives rename*: `Subject` is a distinct field, not an alias of `Username`;
    there is no rename path in the store today, and a future one would keep the
    minted subject rather than re-derive it.
  - *Deleting a user does not delete personas*: the CRD is a separate object; the
    orphan policy stays a D7 decision.
- **Why the prefix.** `booth:local:` makes a locally-minted subject visibly
  distinct from a future OIDC `sub`, so the eventual cutover is greppable and can
  backfill rather than collide. With OIDC, `Subject` becomes the IdP `sub`; the
  §6.3 predicate is unchanged — only how the caller's subject is obtained.
- **Propagation.** `BcryptAuthenticator.Authenticate` returns the session with
  `Subject: u.Subject`. The token codec already carries `sub` as an opaque
  string. Scrape jobs and content threads already stamp `OwnerSubject =
  sess.Subject`, so they inherit the stable subject with no further change.

**Migration note.** Sessions previously carried `Subject == username`. Booth's
user table is in-memory (resets on restart) and no DevRel rows existed before
this change (only the seed Admin, who sees everything via §6.3 clause 1), so
there is no persisted-owner migration to run. Any pre-existing content/scrape
rows owned by a bare `admin` string remain Admin-visible via clause 1.

## 2. DevRel-scoped persona create (contract §6.4)

The §6.3 owner clause already scopes list/get/update/delete for DevRelPersona
(an owner-scoped, non-admin-only kind). D5 adds **owner stamping on create** so a
DevRel self-serves:

- `ResourceKind` gains an optional `setOwner` hook; only `DevRelPersonaKind` sets
  it (admin-only platform config — ModelProfile/ReferenceRepo — is never stamped;
  its empty owner is deliberate).
- The create handler fills an **omitted** `spec.owner.subject` with the caller's
  session subject. An **explicitly-supplied** owner is left intact and still runs
  the full §6.3 gate — so a DevRel naming someone else's subject is denied (403),
  not silently re-homed; an Admin may create on a DevRel's behalf. This preserves
  the existing `TestDevRelPersonaRoute` 403 contract while making the common
  self-service case (POST a persona with no owner) just work.

## 3. First-run detection (plan §1/§4)

New `GET /v1/onboarding-status` (behind the session Middleware), distinct from
the seed-Admin `GET /v1/auth/bootstrap-status`:

- `bootstrap-status` → "is this a freshly provisioned install?" (seed Admin).
- `onboarding-status` → "does the logged-in DevRel have a validated persona yet?"

It reads the caller's own personas through the §6.3-gating store and reports
`{subject, role, hasPersona, hasValidatedPersona, needsOnboarding}`.
`needsOnboarding = role == DevRel && !hasValidatedPersona`. "Validated" is the
D1 contract's cheap declarative gate — the `Validated=True` status condition
(`v1alpha1.PersonaConditionValidated`), mirror of the Postgres `StyleProfile`
row. The answer is scoped to the caller's own subject, so an Admin's
all-personas List (clause 1) never flips the Admin into onboarding.

The SPA probe hook `useOnboardingStatus` (`booth/web/src/app/onboarding.ts`)
mirrors the existing `useBootstrapStatus`: it degrades to `needsOnboarding:false`
on any non-200 so a probe failure never traps a user in onboarding. The wizard UI
it gates is D6 (ISI-7361); this hook is the consumed contract, shipped ahead of
it exactly as `bootstrap.ts` was.

## Test coverage

- `auth`: `TestCreateMintsStableSubject` (mint/stable/unique/distinct-from-username
  + Authenticate surfaces it); existing authenticator/login tests updated to the
  minted subject.
- `server`: `TestDevRelPersonaOwnerStampedOnCreate` (omitted owner → self;
  explicit foreign owner → 403), `TestAdminPersonaOwnerHonored`,
  `TestOnboardingStatusFirstRun` (none / unvalidated / validated),
  `TestOnboardingStatusScopedToSubject` (no cross-user leak; Admin never onboards).
