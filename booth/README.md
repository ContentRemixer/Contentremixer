# Booth

The Booth is the single front door to the ContentRemixer CRs (design
`docs/design/phase1-operator-design.md` §6). It is a Go service in this monorepo
subdir (board decision #7), separate module from the operator (`app/`).

Phase 3 builds it across sibling tickets that share **only** the `{subject, role}`
session contract, so they can land in parallel:

| Ticket | Package | Responsibility |
|---|---|---|
| **P3-2** ISI-7150 | [`auth/`](./auth) | **AuthN** — bcrypt user table, signed sessions, seed Admin, middleware. *(this ticket)* |
| **P3-1** ISI-7149 | `api/` (incoming) | **AuthZ** — HTTP CRUD over CRs, the frozen §6.3 predicate, audit log. |

## The contract P3-1 consumes

`booth/auth` is the authentication provider. It is deliberately decoupled from
authorization — it only turns a credential into a `Session`; the §6.3 predicate
is the API's job. P3-1 wires it in three calls:

```go
prov, err := auth.BootstrapFromEnv(os.Getenv) // seeds the Admin from the k8s Secret
if err != nil { log.Fatal(err) }

// public routes
mux.HandleFunc("POST /auth/login", prov.Handlers.Login)
mux.HandleFunc("POST /auth/logout", prov.Handlers.Logout)

// protected routes — Middleware injects the Session, 401s if absent/invalid
mux.Handle("/api/", prov.Middleware.Wrap(apiHandler))

// inside apiHandler, before touching the k8s API:
sess, _ := auth.SessionFromContext(r.Context())
authorized := sess.Role == auth.RoleAdmin || resource.Owner.Subject == sess.Subject // §6.3
```

- `Session{Subject, Role}` — **subject == username** (§6.2 is subject-agnostic, so
  a bcrypt username substitutes cleanly for an OIDC `sub`).
- `auth.RoleAdmin` / `auth.RoleDevRel` — the shared role vocabulary (§6.1).
- Sessions are short-lived HMAC-signed tokens (cookie or `Authorization: Bearer`).

## Auth mechanism (board decision #8)

Built-in **bcrypt** user table + signed session — no OIDC / no homelab IdP (#8a).
The sole initial Admin is the board/owner (#8b), seeded from a k8s Secret at
startup; see [`../deploy/secrets/booth-auth.example.yaml`](../deploy/secrets/booth-auth.example.yaml).
No credential is ever committed (§8). The upgrade path is swapping the
`Authenticator` implementation behind the same `Session` contract — **not**
building OIDC now.
