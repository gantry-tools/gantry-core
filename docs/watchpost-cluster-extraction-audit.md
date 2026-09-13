# Watchpost clustering extraction audit

Phase 3 starts from Watchpost CP17. The extraction rule is behavioral: code moves to Gantry Core only when Phase 2 proved the concept independent of Watchpost persistence or policy.

## Generic and proven

The following semantics survived the Watchpost implementation and failure/recovery pass and are suitable for `gantry-core/cluster`:

- node and installation identity shapes;
- capabilities and protocol-version negotiation;
- member lifecycle and health-state vocabulary;
- invitation/join/approval/rejection/revocation/rotation/removal lifecycle vocabulary;
- node fingerprints and credential validation rules;
- signed request canonicalization, bounded payloads, clock-skew checks and replay nonce semantics;
- target selectors (`local`, `node:<id>`, `members`, `all`);
- bounded fan-out and deterministic per-node result aggregation;
- stable cluster CLI command grammar and common API response contracts.

## Watchpost-specific and retained locally

These remain in Watchpost:

- SQLite tables, migrations and transaction boundaries;
- Watchpost audit-log persistence and permission checks;
- product version discovery and HTTP route registration;
- monitor, post, Agent, check, alert and incident queries;
- ownership policy for Watchpost objects;
- Agent pairing, Agent credentials and human account/session authentication;
- `/app/` visual theme and product-specific rendering.

## Explicit non-goals

Phase 3 does not introduce shared SQL storage, consensus, leader election, human-account federation, Agent credential reuse, or configuration propagation. Storage is exposed to the shared package only through narrow interfaces where needed.

## Extraction boundary

Gantry Core defines values, validation, canonical cryptographic envelopes, routing/fan-out helpers and presentation contracts. Watchpost adapts those primitives to its database and application services. This keeps the core reusable by Trestle and Webfleet without teaching it Watchpost schema names or object policy.
