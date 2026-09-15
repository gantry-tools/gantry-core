# Gantry CLI/API, clustering and propagation roadmap

This is the canonical cross-project campaign for Cortex, Warden, Trestle,
Watchpost, Watchpost Agent and Webfleet. Product handovers may reference it but
must not carry drifting copies. Checkpoint status is evidence-based: code,
tests, documentation and the checkpoint commit are all required.

## Current architecture status

The phases below record the completed campaign history. The current architecture
is the **shared clustering layer**: the generic identity, pairing, membership,
secure transport, targeting and fan-out contracts live in `gantry-core/cluster`
and are consumed by Watchpost, Trestle and Webfleet through product-local
storage, route and policy adapters. Standalone mode remains the default
everywhere.

- **COMPLETED** — peer identity/pairing/membership/transport/health in
  `gantry-core/cluster`; product adoption by Watchpost, Trestle and Webfleet;
  and configuration/policy propagation (export/preview/apply, profiles, drift
  reconciliation) per Phase 6. The Watchpost/Webfleet distributed certification
  campaign is closed.
- **PARTIAL** — full two-sided Watchpost credential rotation; bounded Webfleet
  DB-isolation and stale→recovery lifecycle evidence.
- **NOT IMPLEMENTED** — Watchpost distributed scheduled-work ownership and
  duplicate-work fencing (cluster-summary self-ownership is not fencing).
- **FUTURE (not implemented)** — replicated durable state / database HA for
  SQLite-backed clustered products. Phase 7A recorded the frozen architecture
  (`docs/REPLICATED_STATE_ARCHITECTURE.md`); **no database replication exists in
  any Gantry product today**. PostgreSQL-backed deployments rely on
  PostgreSQL-native HA (provisioned by Trails) rather than a Gantry replication
  protocol.

## Invariants

- Each website operation maps to an authenticated HTTP operation.
- Each functional HTTP operation that can sensibly be automated maps to a CLI
  command; browser protocol endpoints such as OAuth callbacks are documented
  exceptions rather than fake CLI commands.
- UI, HTTP and CLI adapters call one product-owned service operation.
- CLI automation has stable JSON, errors, exit codes, non-interactive input,
  timeouts and secret-redaction behavior.
- Human authentication remains installation- and product-local.
- Cluster nodes have independent identities, credentials and audit actors.
- Standalone mode remains the default and requires no cluster infrastructure.
- Propagation is never implicit last-write-wins database replication.

## Phase 1 - Shared CLI/API contract

- [x] **CP1 Functional surface inventory.** Record every functional HTTP route,
  website consumer and existing CLI command in a machine-readable manifest per
  project. Classify reads, mutations, destructive operations, secrets,
  streaming/browser protocols and current coverage gaps.
- [x] **CP2 Canonical operation model.** Implement the versioned operation,
  route, input/output, authorization, audit, idempotency and automation model in
  Gantry Core, with compatibility fixtures.
- [x] **CP3 CLI grammar and behavior.** Implement shared parsing and behavioral
  contracts for resource/verb commands, JSON/file/stdin input, output modes,
  quiet operation, confirmations, timeouts and stable exit codes.
- [x] **CP4 Authentication and execution contexts.** Model local execution,
  remote tokens and future node execution without sharing human accounts or
  allowing one actor type to inherit another's authority.
- [x] **CP5 Input, output and error primitives.** Add bounded strict JSON,
  pagination/filter primitives, structured errors, renderers, confirmations,
  redaction, request IDs and idempotency keys.
- [x] **CP6 Client and command transport.** Add a dependency-light HTTP client
  and local executor abstraction with TLS, tokens, timeouts, cancellation,
  pagination, streaming and safe retry classification.
- [x] **CP7 Contract-test harness.** Make manifests testable for uniqueness,
  authorization, route/CLI parity, schemas, redaction and declared exceptions.
- [x] **CP8 Cross-project adoption smoke pass.** In every consumer, register and
  test at least one read, one mutation and one destructive or security-sensitive
  operation through the shared contract without changing product behavior.

Phase 1 closes only when all consumer suites pass against the local Gantry Core
checkout and the generated coverage baseline is truthful. It does not claim
full CLI coverage; Phase 5 closes the measured gaps.

CP1 evidence: `cmd/surface-inventory` and `docs/inventory/*.json`. The scanner
records exact method-qualified registrations where present, marks older dynamic
handler registrations `ANY`, and conservatively leaves CLI coverage false until
an executable operation declaration proves it. This preserves gaps rather than
converting source-code guesses into certification.

CP2 evidence: the `operation` package and its compatibility fixture. A registry
rejects duplicate IDs, routes and CLI mappings; automatable operations require
a CLI mapping; mutations require audit; retry safety is explicit; and node
authorization cannot inherit a human capability.

CP3 evidence: the `cli` package. The common grammar accepts resource/verb
commands with file/stdin JSON, table/JSON/plain output, quiet mode, explicit
confirmation, timeouts, request IDs and remote origins. Exit codes 0-7 are
reserved by contract. Raw tokens are rejected as arguments; protected token
files or consumer-supplied credential sources are the supported boundary.

CP4 evidence: `auth.Actor`, `auth.Execution` and `auth.Authorize`. Human,
API-token, service and node identities carry mutually exclusive authority;
project/installation locality is validated; and cluster execution requires a
cluster-node actor rather than a human session.

CP5 evidence: the `protocol` package. Strict decoding is size-bounded and
rejects unknown/trailing input; structured errors map consistently to HTTP and
CLI; pagination, exact confirmation, request/idempotency keys, deep-copy secret
redaction and deterministic output modes have focused tests.

CP6 evidence: the `client` package. HTTP and local execution consume the same
operation contract; remote calls are bounded, cancellable and credential-source
driven; token files must be private; route variables are escaped; and retries
are limited to reads or explicitly idempotent keyed mutations.

CP7 evidence: the `contracttest` package. Consumer-supplied runtime observations
are checked against executable contracts, with deterministic failures for
undeclared/unobserved routes, uncovered website operations, stale or unjustified
exceptions, duplicate mappings and missing automation/audit declarations. CLI
mappings reserve the canonical resource/verb grammar; only mappings explicitly
marked implemented and observed by a product test count as executable coverage.

CP8 evidence: each consumer now pins Gantry Core v0.1.1 and owns an
`internal/operations` adoption manifest covering a read, mutation and
destructive or security-sensitive operation. The complete ordinary, race and
vet suites passed from the versioned module archive. Checkpoint commits are
Cortex `32375b1`, Warden `27b2a7d`, Trestle `d6f96d7`, Watchpost `9cb1bc4`,
Watchpost Agent `da41d8a` and Webfleet `1fd4840`. Reserved CLI mappings remain
explicitly unimplemented, so the inventory truthfully retains the Phase 5 gap.

Post-Phase-1 review: CP9 remains the next smallest coherent step. Watchpost must
first define the topology and ownership boundary between peer servers and its
existing server-to-Agent pairing before node identities, ceremonies or shared
cluster abstractions can be designed without guessing.

## Phase 2 - Watchpost proves clustering

Phase 2 implementation status: CP9-CP17 are implemented in Watchpost and were
exercised by the September 2026 distributed dogfood campaigns plus deterministic
local regression coverage. The complete Watchpost Go 1.25 test/race/vet wall now
runs in CI and is green against the real dependency set.

- [x] **CP9 Boundaries and topology.** Define server-to-server clustering,
  ownership and deferred data-replication behavior separately from existing
  Watchpost-to-Agent pairing.
- [x] **CP10 Node identity.** Add stable installation/node IDs, public identity,
  endpoints, capabilities, versions, lifecycle timestamps and revocation.
- [x] **CP11 Pairing ceremony.** Add short-lived single-use invite, join,
  inspect, approve, reject and replay-safe audit flows in CLI and `/app/`.
- [x] **CP12 Authenticated transport.** Add TLS, peer authentication, signed or
  mutually authenticated requests, nonces, bounded requests and negotiation.
- [x] **CP13 Membership and health.** Add member state, last contact, latency,
  compatibility, disable/remove and credential rotation.
- [x] **CP14 First distributed read.** Prove targeted and bounded fan-out health
  with deterministic per-node and partial-failure results.
- [x] **CP15 Watchpost distributed operations.** Add useful aggregate status and
  explicit ownership without duplicating agents or monitoring work.
- [x] **CP16 Cluster UI.** Add pairing, membership, health, rotation, revocation
  and audit views beside Agent pairing in the Watchpost `/app/` SPA.
- [x] **CP17 Failure and recovery.** Test expiry, replay, partition, offline
  peers, rotation interruption, incompatibility, removal and re-pairing.

## Phase 3 - Extract only the proven model

- [x] **CP18 Proven-model audit.** Separate generic identity, ceremony,
  transport and fan-out from Watchpost policy and Agent behavior.
- [x] **CP19 Shared cluster domain.** Extract versioned node, membership,
  invitation, capability, health, target and result contracts behind storage
  adapters.
- [x] **CP20 Shared secure transport.** Extract envelopes, peer authentication,
  replay protection, limits, retry classification and structured errors.
- [x] **CP21 Shared pairing lifecycle.** Extract invite through re-pair while
  leaving branding, persistence and permissions in consumers.
- [x] **CP22 Shared routing and fan-out.** Extract selectors, bounded parallelism,
  cancellation, idempotency and partial-result aggregation.
- [x] **CP23 Shared cluster CLI.** Supply identical `cluster init`, `invite`,
  `join`, `approve`, `members`, `status`, `rotate`, `revoke` and `remove` forms.
- [x] **CP24 Shared UI/API contracts.** Supply common wire shapes and reusable
  interaction behavior while retaining each product's design.
- [x] **CP25 Rebase Watchpost.** Delete its duplicated generic implementation
  and prove unchanged behavior through Gantry Core.
- [x] **CP26 Compatibility and upgrades.** Prove restart persistence, rolling
  compatible upgrades, fail-closed incompatibility and retained Agent pairs.

## Phase 4 - Trestle and Webfleet adoption

- [x] **CP27 Trestle cluster adoption.** Integrate shared node, ceremony,
  membership, transport, health, CLI, UI and audit behavior.
- [x] **CP28 Trestle distribution policy.** Classify cluster-readable,
  propagatable, node-local and authoritative operations without implying record
  or database replication.
- [x] **CP29 Trestle distributed operations.** Add health/version aggregation,
  targeted administration and schema/config comparison.
- [x] **CP30 Webfleet cluster adoption.** Integrate the identical shared model.
- [x] **CP31 Webfleet distribution policy.** Classify request, environment,
  monitor, schedule, secret, result and runtime ownership.
- [x] **CP32 Webfleet distributed operations.** Add aggregate health/monitoring,
  targeted execution, comparison and duplicate-execution prevention.
- [x] **CP33 Cross-project parity.** Contract-test identical ceremony, commands,
  states, selectors, rotation, revocation, audit and cluster UI layout.

Phase 4 acceptance: Watchpost, Trestle, and Webfleet now share the Gantry cluster lifecycle and selector/result contracts while retaining product-local storage and policy. Standalone mode remains valid; cluster removal does not imply data migration; no database replication or configuration propagation is claimed.

## Phase 5 - Complete functional API/CLI coverage

- [x] **CP34 Authoritative coverage matrices.** Generate UI/API/CLI/permission/
  schema/test matrices and fail CI on undeclared drift.
- [x] **CP35 Cortex completion.** Cover setup, accounts, providers/policy,
  workspaces, conversations, agent execution, launcher, service and diagnostics.
- [x] **CP36 Warden completion.** Cover accounts/security, workspaces/editor,
  agents, terminals, provider policy, launcher, service and diagnostics.
- [x] **CP37 Trestle completion.** Cover database setup, collections, records,
  auth/access, files, jobs, functions, webhooks, realtime, backups and cluster.
- [x] **CP38 Watchpost/Agent completion.** Cover monitors, alerts, agents,
  telemetry, evidence, lifecycle, pairing, clustering and diagnostics.
- [x] **CP39 Webfleet completion.** Cover sites, requests, environments,
  analytics, monitors, audits, schedules, runs, clustering and diagnostics.
- [x] **CP40 Authorization parity.** Prove identical permissions, revocation,
  policy enforcement, secret masking, confirmations and actor-specific audit.
- [x] **CP41 Automation dogfood.** Exercise stdin/files, JSON, pagination,
  idempotency, expiry, timeouts, partial failure and stable exits through scripts.
- [x] **CP42 Public certification.** Publish generated API/CLI references,
  authentication guidance, automation examples and truthful coverage reports.

Phase 5 acceptance: generated and CI-pinned coverage matrices now replace the smoke baseline across all six product surfaces; every declared automatable operation has an observed CLI mapping and schema/evidence metadata, browser/service/streaming exceptions are explicit, shared security/automation rules are enforced, and each product publishes CLI/API/auth/permissions/exit/schema/automation documentation beside its generated coverage statement.

## Phase 6 - Configuration and policy propagation

- [x] **CP43 Propagation envelope.** Define kind, identity, schema version,
  revision, source, digest, dependencies, secret references, targets, conflict
  policy, actor and signature.
- [x] **CP44 Export/diff/dry-run/apply.** Require exact previews of creates,
  updates, deletions, incompatibilities, missing dependencies/secrets and denial.
- [x] **CP45 Targeting.** Support explicit nodes, groups/labels, capability
  matching and exclusions; future-node policy is separately explicit.
- [x] **CP46 Conflict and ownership.** Implement reject/source/destination/manual
  and declared merge policies; security policy never silently last-write-wins.
- [x] **CP47 Secrets.** Keep ordinary exports secret-free; use references or
  destination encryption with dedicated permission, masking and audit.
- [x] **CP48 Transactions and rollback.** Validate first, sign plans, record
  revisions, expose partial completion and rollback only declared-safe objects.
- [x] **CP49 Product adapters.** Add Watchpost monitor/alert policy, Trestle
  schema/access/integration definitions and Webfleet request/monitor/schedule
  definitions before considering Cortex/Warden administrative policy.
- [x] **CP50 Propagation UI.** Add selection, targets, diff, compatibility,
  confirmation, per-node progress, retry/rollback and history to `/app/`.
- [x] **CP51 Scheduled reconciliation.** Only after manual dogfood, add saved
  profiles, drift detection, notify-only, schedules and approval requirements.
- [x] **CP52 Adversarial recovery.** Test partitions, restarts, stale revisions,
  concurrent edits, invalid schemas, absent secrets, revocation and rollback.
- [x] **CP53 Final certification.** Prove fresh and upgraded three-node clusters,
  rolling upgrades, credential lifecycle, propagation, recovery, isolation and
  UI/API/CLI parity.

Phase 6 acceptance: shared propagation is now a configuration/policy distribution layer rather than database replication; Watchpost, Trestle and Webfleet expose declared product adapters, dry-run/apply/history/profile workflows, scheduled drift evaluation and fail-closed automatic reconciliation. Core certification covers fresh three-node distribution, clean drift, partitions, interruption/restart, stale/conflicting revisions, compatibility, secret/permission blockers, revocation and rollback. Product generated API/CLI matrices include the propagation surface, and runtime/history data remains explicitly node-local. See `docs/PHASE6_CERTIFICATION.md`.

## Phase 7 - Replicated durable state (future)

Status is tracked against the frozen architecture decision in
`docs/REPLICATED_STATE_ARCHITECTURE.md`. **No database replication is implemented
or claimed in any product.** This phase is future work, recorded here so the
proposed layer is never mistaken for an existing capability.

- [x] **7A Architecture decision.** Frozen: `hashicorp/raft` as the embedded
  consensus engine while Gantry owns the semantic operation layer above it;
  filtered logical snapshots; voting membership separated from cluster
  membership; durable operation-ID idempotency; fail-closed version handling;
  PostgreSQL deployments use PostgreSQL-native HA (no Gantry database
  replication layer). Evidence: `docs/REPLICATED_STATE_ARCHITECTURE.md`.
- [ ] **7B Generic replicated-state harness.** Consensus/persistence feasibility
  spike, logical operation contract, deterministic KV state machine, durable
  idempotency, quorum/fault/restart/membership/version matrices over local
  1/2/3/4/5-node clusters. **NEXT.**
- [ ] **7C Snapshot/catch-up/membership/versioning.** Filtered logical
  snapshots, learners, compaction, membership changes, replication-schema
  negotiation, node-local-state exclusion certification.
- [ ] **7D First Watchpost replicated-state adapter.** Smallest valid semantic
  slice after dependency audit (options A-D in the architecture decision);
  standalone preserved; no secrets initially.
- [ ] **7E Expand Watchpost replicated surface.** Identity migrations where
  required; schedule-definition/runtime-state split; secret-reference/encryption
  design; audit/history semantic classification.
- [ ] **7F PostgreSQL HA contract.** Trestle, Webfleet and Trails
  provisioning/verification; no Gantry database replication for PostgreSQL.

Cortex and Warden remain outside Phase 7 unless they first become cluster
participants.

## Checkpoint discipline

At each checkpoint:

1. Re-read this roadmap and the affected product handover.
2. Confirm the next checkpoint is still the smallest coherent dependency step.
3. Update compatibility fixtures before or with changed shared behavior.
4. Run focused tests, `go test ./...`, `go test -race ./...`, `go vet ./...`,
   `git diff --check`, and the affected local-workspace consumer suites.
5. Update this file with status, evidence and any justified reordering.
6. Commit the checkpoint independently and report its exact commit.

Do not mark a future checkpoint complete merely because an abstraction appears
capable of supporting it. Do not silently delete obligations discovered to be
larger than expected; split or revise them explicitly.
