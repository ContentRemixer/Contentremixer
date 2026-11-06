# Booth

The **single front door** to the ContentRemixer CRs (design
`docs/design/phase1-operator-design.md` §6). A Go service in this monorepo
subdir (board decision #7), a separate module from the operator (`app/`), which
it consumes for CRD types via `replace => ../app`.

Phase 3 built it across two sibling tickets that share **only** the
`{subject, role}` session contract:

| Ticket | Package | Responsibility |
|---|---|---|
| **P3-2** ISI-7150 | [`auth/`](./auth) | **AuthN** — bcrypt user table, HMAC-signed sessions, seed Admin, login/middleware. |
| **P3-1** ISI-7149 | `internal/{authz,store,server,audit}` + `cmd/booth` | **AuthZ + API** — HTTP CRUD over the CRs, the frozen §6.3 predicate, audit log. |

## The frozen invariant (§6.3)

```
authorized == (session.role == "Admin") OR (resource.owner.subject == session.subject)
```

with the §6.3 refinements:

- **Admin-only classes** (`ModelProfile`, `ReferenceRepo`): only clause 1 admits.
  A DevRel is `403` regardless of ownership.
- **Empty-owner** (platform-owned) resources: Admin-only.

Enforced **server-side**, never by client-side hiding: `List` filters the result
set; `Get`/`Update`/`Delete`/`Approve` gate the single object; `Create` gates on
the object's own declared owner.

### Why it can't be bypassed

`internal/store` is the **only** path from an HTTP handler to a CR. Every store
method takes the `auth.Session`, runs `authz.Authorize`, and writes an audit
record before it calls the k8s client. Adding a route cannot reach a CR without
the gate — the invariant is structural.

```
auth/                AuthN provider (P3-2): Session, Authenticator, bcrypt,
                     TokenCodec, Middleware, Login/Logout/WhoAmI, BootstrapFromEnv
internal/authz       the frozen §6.3 predicate (pure function, 100% covered)
internal/audit       §6.4 record {subject, role, verb, resourceKind, resourceName, decision}
internal/store       authz-gating single front door to the CRs
internal/server      HTTP surface: mounts auth routes + Middleware, CRUD handlers
cmd/booth            wiring: Booth ServiceAccount k8s client + BootstrapFromEnv + router
```

## How the two halves connect

`booth/auth` turns a credential into a `Session`; it knows nothing about
authorization. The API (`internal/server`) mounts the auth routes and wraps its
CR routes with the auth `Middleware`, then reads the session and defers every CR
access to the gating `store`:

```go
prov, err := auth.BootstrapFromEnv(os.Getenv) // seeds the Admin from the k8s Secret
// public:    POST /v1/auth/login, POST /v1/auth/logout
// protected: prov.Middleware.Wrap(...) injects the Session, 401s if absent/invalid
sess, _ := auth.SessionFromContext(r.Context())
// store.Get/List/Create/Update/Delete/Approve(ctx, sess, kind, ...) each run §6.3
```

- `Session{Subject, Role}` — **subject == username** (an OIDC `sub` substitutes
  cleanly later). `auth.RoleAdmin` / `auth.RoleDevRel` are the shared vocabulary.
- Sessions are short-lived HMAC-signed tokens (cookie or `Authorization: Bearer`).

## AuthN mechanism (board decision #8)

Built-in **bcrypt** user table + signed session — no OIDC / no homelab IdP (#8a).
The sole initial Admin is the board/owner (#8b), seeded from a k8s Secret at
startup; see [`../deploy/secrets/booth-auth.example.yaml`](../deploy/secrets/booth-auth.example.yaml).
No credential is ever committed (§8). The upgrade path is swapping the
`Authenticator` implementation behind the same `Session` contract — not building
OIDC now.

## Endpoints

`/healthz`, `/readyz` (unauthenticated probes); `POST /v1/auth/login`,
`POST /v1/auth/logout` (public), `GET /v1/auth/whoami` (authenticated); and
authenticated CRUD over the admin-only kinds:

```
GET/POST  /v1/modelprofiles      GET/PUT/DELETE /v1/modelprofiles/{name}
GET/POST  /v1/referencerepos     GET/PUT/DELETE /v1/referencerepos/{name}
```

The route list is deliberately thin; the contract is the **invariant**, not the
routes. The Phase-2 `DevRelPersona` (owner-scoped) kind and the run/draft/PR
views are a single entry in `internal/store/registry.go` — the enforcement code
is generic over the resource-kind table.

## Develop

```
cd booth
go test -race -coverprofile=cover.out ./...   # 80% gate in booth-ci.yml
go build ./...
```
