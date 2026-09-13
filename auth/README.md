# Authentication primitives

`auth` contains the product-neutral security mechanics shared by Gantry Go
applications:

- account, identity, role and capability contracts;
- password hashing and verification;
- bounded server-side sessions and login challenges;
- strict SameSite cookie and CSRF handling;
- normalized, redacted audit events.

Applications retain storage migrations, OAuth provider configuration, TOTP
secret storage, product capability catalogues and HTTP presentation. They adapt
those concerns through `AccountProvider`, `SessionPersistence` and
`SessionOptions`.

The package intentionally does not provide a central identity service. Every
installation remains independently authoritative.

`Actor` and `Execution` also keep human sessions, scoped API tokens, service
identities and cluster-node identities structurally separate. Project and
installation identity are part of every actor: equal account IDs in two
installations do not create a shared account, and node/service credentials can
never inherit human roles or capabilities.
