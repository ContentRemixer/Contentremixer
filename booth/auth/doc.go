// Package auth is the Booth's authentication provider (ISI-7150, P3-2).
//
// It is deliberately decoupled from authorization. Its single job is to turn a
// credential into the frozen session contract that the Booth API (P3-1,
// ISI-7149) consumes before it evaluates the §6.3 ownership predicate:
//
//	authorized == (session.Role == RoleAdmin) OR (resource.owner.subject == session.Subject)
//
// The contract shared with P3-1 is exactly three things:
//
//   - the Session value ({Subject, Role}),
//   - Middleware, which resolves a Session from the request and injects it into
//     the request context (401 if absent/invalid), and
//   - SessionFromContext, which the API handlers call to read it back.
//
// subject == username. §6.2/§6.3 are subject-agnostic, so a bcrypt username is a
// clean substitution for an OIDC `sub`; spec.owner.subject now carries a
// username and the predicate is unchanged.
//
// Mechanism (board decision #8a): a built-in bcrypt user table + a short-lived
// HMAC-signed session token. No OIDC / no homelab IdP. The upgrade path is
// swapping Authenticator behind the same Session contract — not building OIDC
// plumbing now (the "ponytail" ceiling).
package auth
