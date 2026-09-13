# Phase 4 cluster parity contract

Watchpost, Trestle, and Webfleet use the same Gantry cluster protocol vocabulary and lifecycle.

The common contract is deliberately about **node coordination**, not shared product storage:

- standalone mode remains valid and is the default until members are paired;
- human accounts remain installation-local;
- node credentials are distinct from human and product-agent credentials;
- pairing is invite -> join -> explicit approve/reject -> one-time collect;
- member lifecycle is active/disabled/revoked, with explicit rotate/revoke/remove;
- peer requests use the Gantry signed envelope, replay protection, capability checks, and bounded fan-out;
- selectors are `local`, `node:<id>`, `members`, and `all`;
- partial failures remain per-node results rather than being hidden by aggregate success;
- the management layout exposes overview, pairing, pending approvals, members/health, credential lifecycle, and audit history.

Canonical unauthenticated node-pairing and signed-RPC paths live under `/api/cluster/v1/`. Product-authenticated management paths may remain product-local as long as they expose the same lifecycle and response contracts.

Phase 4 does **not** add database replication or configuration propagation. Trestle records/files and Webfleet execution/runtime state remain node-local. Propagation is a later phase with explicit object, ownership, conflict, and secret semantics.
