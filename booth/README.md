# Booth API

The **single front door** to the ContentRemixer CRs. A small Go service that
authenticates a caller, resolves a `{subject, role}` session, and enforces the
frozen design-§6.3 authorization predicate on **every** read and write before
touching the Kubernetes API.

> Design contract (frozen): `docs/design/phase1-operator-design.md` §6.
> Issue: ISI-7149 (P3-1). Parent: ISI-7087 (Phase 3).

## The invariant

```
authorized == (session.role == "Admin") OR (resource.owner.subject == session.subject)
```

with the two §6.3 refinements:

- **Admin-only classes** (`ModelProfile`, `ReferenceRepo`): only clause 1 admits.
  A DevRel is `403` on all of them, regardless of ownership.
- **Empty-owner** (platform-owned) resources: Admin-only — clause 2 cannot match
  an empty subject.

The predicate is enforced **server-side**, never by client-side hiding:
`List` filters the result set; `Get`/`Update`/`Delete`/`Approve` gate the single
object; `Create` evaluates against the object's own declared owner.

### Why it can't be bypassed

`internal/store` is the **only** path from an HTTP handler to a CR. Every store
method takes the `Session`, runs `authz.Authorize`, and writes an audit record
before it calls the k8s client. Adding a route cannot reach a CR without going
through the gate — the invariant is structural, not a convention.

```
cmd/booth            wiring: k8s client (Booth ServiceAccount) + auth + router
internal/authz       the frozen §6.3 predicate (pure function, 100% covered)
internal/session     Authenticator interface + bcrypt built-in user table
internal/audit       §6.4 audit record {subject, role, verb, kind, name, decision}
internal/store       authz-gating single front door to the CRs
internal/server      HTTP surface: auth middleware + CRUD handlers
```

## AuthN (Phase 1)

Board decision #8a: a **built-in bcrypt user table** (no OIDC/homelab IdP yet).
The mechanism sits behind `session.Authenticator`, so swapping in OIDC later is a
new implementation of that one interface — `authz`, `store`, and `server` do not
change.

Phase 1 uses HTTP Basic over TLS (terminated at the gateway/ingress). The user
table is supplied at deploy time via the `BOOTH_USERS` env var (from a Secret),
never hard-coded — board decision #8b seeds the initial Admin out of band.

```
BOOTH_USERS   JSON array: [{"username","subject","role","passwordHash"}]
              role ∈ {"Admin","DevRel"}; passwordHash is a bcrypt hash.
BOOTH_NAMESPACE   namespace the CRs live in (default "content-remixer")
BOOTH_ADDR        listen address (default ":8080")
```

Generate a bcrypt hash with `session.HashPassword` (see tests) or any bcrypt tool.

## Endpoints

`/healthz`, `/readyz` (unauthenticated probes), and authenticated CRUD:

```
GET    /v1/modelprofiles            POST /v1/modelprofiles
GET    /v1/modelprofiles/{name}     PUT  /v1/modelprofiles/{name}    DELETE …
GET    /v1/referencerepos           POST /v1/referencerepos
GET    /v1/referencerepos/{name}    PUT  /v1/referencerepos/{name}   DELETE …
```

The route list is deliberately thin; the contract is the **invariant**, not the
routes. The Phase-2 `DevRelPersona` (owner-scoped) kind and the run/draft/PR
views are a single entry in `internal/store/registry.go` — the enforcement code
is generic over the resource-kind table and does not change.

## Develop

```
cd booth
go test -race -coverprofile=cover.out ./...   # 80% gate in booth-ci.yml
go build ./...
```
