# Gantry application routing and authentication convention

This document defines the shared HTTP entry points and security vocabulary for
the self-hosted Gantry Go applications: Cortex, Warden, Trestle, Watchpost,
Watchpost Agent and Webfleet.

## Canonical routes

| Route | Contract | Default boundary |
| --- | --- | --- |
| `/` | Adaptive instance entry point: zero configured instances redirects to the local `/app/`; one redirects to that instance's `/app/`; two or more render the launcher. | Public catalogue view |
| `/app/` | Product application, including first-run setup and sign-in. `/app` redirects to `/app/`. | Product session |
| `/manage/` | Shared administration shell plus product-specific administration. `/manage` redirects to `/manage/`. | Management capability |
| `/api/` | Product and shared JSON/WebSocket APIs. | Explicit per-route policy |
| `/healthz` | Sparse liveness response suitable for a local supervisor or monitoring system. | Public; no secrets or configuration |

Static product assets remain under `/assets/`. Applications must not rely on
the bare origin as their own URL: generated links, OAuth returns and instance
launcher entries target `/app/` explicitly. Unknown top-level paths return 404
rather than falling through to a single-page application.

Public `my.*` launchers and embedded launchers use the same versioned JSON
document. Public launchers retain browser-local catalogues; embedded launchers
store shared configuration in the product backend.

## Authentication and authorization

Each installation remains independently authoritative. Standardization means
the products use the same account, identity, role, capability, session, CSRF
and audit semantics; it does not require a central Gantry identity service.

- An account may have multiple login identities.
- Roles contain action-oriented capabilities; account permissions are the
  union of assigned roles.
- The built-in administrator role has `*` and cannot be weakened or removed.
- At least one enabled password-backed administrator must remain after setup.
- Browser sessions are server-side, bounded per account, revocable and carried
  in an HttpOnly SameSite cookie.
- Every state-changing cookie-authenticated request must carry the session's
  CSRF token.
- Authorization is enforced by the server. Hiding a management control is not
  an authorization boundary.
- Security-sensitive changes produce structured audit events.

Shared management capabilities begin with:

| Capability | Meaning |
| --- | --- |
| `accounts.manage` | Create and manage accounts and their identities. |
| `roles.manage` | Create and manage roles and capability assignments. |
| `sessions.manage` | Inspect or revoke sessions beyond the current session. |
| `authentication.manage` | Configure installation-wide authentication providers and policy. |
| `launcher.view` | View a non-public launcher catalogue. |
| `launcher.configure.self` | Manage personal launcher additions when the product enables them. |
| `launcher.configure.all` | Manage the installation-wide launcher catalogue, including import/export. |
| `launcher.propagate` | Initiate same-product catalogue propagation to trusted fleet peers. |
| `audit.read` | Read security audit history. |
| `settings.manage` | Change product settings not covered by a narrower capability. |

Products add capabilities for product actions. A narrow capability takes
precedence over `settings.manage`; launcher administration must therefore use
`launcher.configure.all`, not the broad product-settings permission.

## Management shell

Every product exposes the same primary `/manage/` sections when applicable:

- Users
- Roles and permissions
- Sessions
- Authentication
- Launcher
- Audit
- Product settings

The shell may omit sections unavailable to the signed-in account, but direct
requests still receive 401 or 403. Personal settings remain in `/app/` and are
governed by self-scoped capabilities; `/manage/` is for installation-wide
policy and administration.

## Fleet trust

Configuration propagation is only between instances of the same product.
Enrollment may use one existing member as a coordinator, but every instance
retains its own identity and the resulting fleet must not depend on that member
remaining online. Watchpost's cross-product monitoring relationships are a
separate, read-only protocol and never grant launcher or product configuration
write authority.

Fleet propagation is deliberately outside the initial launcher/auth adoption.
When implemented, it requires both human authorization (`launcher.propagate`)
and peer authentication bound to the product, fleet and relationship type.
