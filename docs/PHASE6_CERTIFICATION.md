# Phase 6 distributed propagation certification

Phase 6 adds controlled configuration and policy propagation to the cluster model proven in Phases 2-4. It does **not** replicate application databases, runtime history, records, files, monitor results, run results, sessions, users, or human credentials.

## Certified shared behavior

The `propagation` package now provides:

- canonical envelopes with stable object identity, schema/revision, source, digest, dependencies, secret references, targeting, actor context, conflict policy and signatures;
- deterministic diff/dry-run output for create/update/delete/no-op/conflict plus compatibility, dependency, secret and permission blockers;
- explicit node/group/capability targeting and exclusions;
- reject, source-wins, destination-wins, manual, authoritative-source and adapter-only merge semantics;
- secret references without ordinary secret-value export;
- signed plans, preflight validation, per-node revision results, visible partial completion and bounded rollback for reversible objects;
- bounded distributed preview/apply fan-out;
- persisted propagation history and saved reconciliation profiles;
- maintenance windows, schedules, notify-only, approval-required and automatic reconciliation;
- fail-closed automatic reconciliation: an unreachable or inapplicable target prevents mutation rather than being interpreted as zero drift.

`TestPhase6ThreeNodePropagationCertification` proves a fresh three-destination apply, clean post-apply drift, and partition-visible fail-closed automatic reconciliation. The broader propagation suite covers stale/concurrent revisions, incompatible schemas, missing dependencies/secrets/permissions, interrupted work, destination restart recovery, source removal, revoked credentials, rollback and rollback failure.

## Product boundaries

### Watchpost

Propagatable objects are monitor/check schedules, alert policy, notification configuration and agent policy. Telemetry, incidents, alert history and Agent runtime state remain local. `/app/`, HTTP API and functional CLI contracts expose preview, apply, fan-out, history and profile management.

### Trestle

Propagatable objects are declared safe project settings, schema definitions, roles/access policy, jobs, webhooks and functions where compatible. Records, uploaded files, database contents and runtime event history remain local. Schema application continues through Trestle's schema safeguards rather than bypassing them.

### Webfleet

Propagatable objects are request definitions, environments without raw secrets, monitors, schedules and execution policy. Run results, history and scheduler runtime state remain local.

All three products persist profile `last_run_at`, run due profiles on a bounded background cadence, support manual `run-due`, and expose management UI for object selection, target preview, explicit apply confirmation, saved profiles and history.

## Recovery and isolation evidence

- Automatic mode validates every selected destination before mutation and blocks if any preview fails or is inapplicable.
- Notify-only and approval-required modes never mutate destinations.
- Disabled/revoked cluster members are excluded by target selection and revoked mid-flight credentials remain explicit failures.
- Partial apply and rollback failures remain visible; cross-node atomicity is never claimed.
- Destination-local secret material is preserved and required-secret absence is reported without revealing the value.
- Product adapters reject undeclared/runtime object kinds, preventing propagation from becoming database replication.
- Existing cluster credential lifecycle, re-pairing, mixed-version negotiation and rolling-upgrade behavior remain owned by the certified cluster package from Phases 2-4.

## Validation gate

The release gate is:

```text
GOWORK=off GOTOOLCHAIN=local go test ./...          # gantry-core
GOWORK=off GOTOOLCHAIN=local go test -race ./...    # gantry-core
GOWORK=off GOTOOLCHAIN=local go vet ./...           # gantry-core

# plus each product's normal Go 1.25 test/race/vet wall,
# generated functional-coverage freshness tests,
# frontend JavaScript syntax checks, and git diff hygiene.
```

In the constrained build environment used for this checkpoint, Gantry Core can run its complete local test/race/vet wall. Product-wide Go 1.25 walls still require the normal development environment because toolchain/module downloads are unavailable here. Product operation-contract suites and frontend syntax checks are run with disposable compatibility validation where required; production module versions and dependencies are not downgraded.
