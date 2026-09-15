# Gantry replicated durable-state architecture

This is the frozen Phase 7A decision for `gantry-core/replication`. It is the
canonical contract for the replicated durable-state layer; the roadmap
(`CLI_API_CLUSTER_ROADMAP.md`) tracks checkpoint status.

**Status:** Phase 7A is COMPLETE. Phase 7B (generic replicated-state harness) is
NEXT. **No database replication exists in any Gantry product today.** This
document is the decision record, not a claim that replication is implemented.

## Central architecture decision

```text
Gantry owns:
    semantic replicated operations
    state classification
    deterministic application
    idempotency
    product adapters
    secret policy
    filtered logical snapshots
    observability / cluster integration

established consensus implementation owns:
    election
    terms
    replicated log
    quorum commitment
    membership safety
    snapshot/log coordination

PostgreSQL-backed deployments:
    PostgreSQL-native HA
    no Gantry database replication layer
```

## 1. Consensus engine vs persistence choice

- **Consensus engine:** `hashicorp/raft` — **frozen as preferred.**
- **Persistence layout:** **deliberately undecided at 7A.** 7B opens with a
  bounded feasibility spike deciding the Raft log store, stable (term/vote)
  store and snapshot storage. Candidates are compared on **correctness,
  durability, dependency footprint and Gantry integration** — not merely because
  Gantry applications already use SQLite.
- The storage implementation is **not part of the public replicated-operation
  contract.**

### Persistence decision (CP7B-1 spike)

Raft term/vote state, the replicated log and snapshot coordination are stored
by a small bbolt-backed `raft.LogStore`/`raft.StableStore`
(`replication/store.go`), kept physically and logically separate from any
product database. The mature alternative normally paired with hashicorp/raft
(hashicorp/raft-boltdb) uses the same bbolt engine; the store is owned so the
persistence abstraction stays swappable. A SQLite-backed store (e.g.
modernc.org/sqlite) was rejected: it would add a full SQL engine to a
dependency-light core, and Raft persistence should not inherit a product
database. bbolt is pure Go, cgo-free, mature and minimal.

## 2. Semantic operation boundary

The replicated log contains **logical operations, never arbitrary SQL.** Frozen
envelope:

```text
operation identity      object identity
product / namespace     expected/precondition revision (if applicable)
replication schema ver  payload
operation kind          originating node
object kind             actor/audit context (where semantically required)
```

- Only a timestamp **inside the committed operation** may influence
  deterministic results; a receiving node's independent clock must not.
- Deterministic apply must not consult `time.Now()`, random generators, local
  autoincrement allocation, hostname, local node identity, environment
  variables, network state, unordered map iteration, or other node-local
  mutable state. Any required ID/timestamp/token is established **before/within
  the committed operation**.

## 3. Acknowledgement and retry

- `proposed != committed != responded`. A leader may commit and die before
  returning success.
- Retries using the **same operation ID** resolve to the already-committed
  result, never re-apply.
- The operation-ID/idempotency record is **durable and survives restart and
  snapshot/catch-up** — not an in-memory cache.
- Retrying the same operation ID with a **different payload** fails closed.

## 4. Schema compatibility

Two distinct concepts:

- **Replication operation schema** = what committed operations/snapshots mean.
- **Product storage schema** = how a version persists that semantic state
  locally.

A voter must understand the **replication schema** before an operation requiring
it commits; **identical physical SQLite layouts are not required** if adapters
deterministically represent the same semantic operation. Snapshots carry an
explicit semantic format/version, not the snapshotting node's SQLite schema.
Fail-closed on unsupported versions; **downgrade unsupported initially.**

## 5. Voting membership

- `Gantry cluster member != Raft voter.` Roles: **voter / non-voter (learner) /
  ordinary cluster peer.**
- Voting rights are never derived from being paired.
- Membership changes go through the consensus library's safe
  configuration-change mechanism.

## 6. First product slice

`posts + rules` is **not frozen.** 7D opens with a dependency audit choosing
between:

- **A.** posts-as-definitions + rules
- **B.** a genuinely independent Watchpost object
- **C.** a deliberately tiny new replication-proof object
- **D.** rules with an explicit dependency/precondition contract

Referential integrity is never weakened to manufacture a small first example.

## 7. Node-local identity invariant

Filtered logical snapshots exclude **by construction**: node installation
identity, peer credentials, pairing invitations/requests, transport
nonces/replay state, local sessions, machine-local telemetry/runtime state,
node-local scheduler execution state. Restoring a snapshot onto a learner/new
node **never clones another node's identity or transport credentials.** This is
a first-class 7C test.

## 8. Secrets

7B–7D use state with **no secret material.** When secrets are added:

```text
ciphertext may replicate
KEK never lives in replicated state
plaintext never enters the Raft log/snapshot
```

with bootstrap/recovery designed separately.

## 9. Phase 7B acceptance wall

Product-independent Go test/race harness, 1/2/3/4/5 local nodes, deterministic
fault injection. Coverage:

```text
ordered commit
deterministic apply
duplicate operation ID
lost response after commit
leader failure before commit
leader failure after commit
election
stale-leader rejection
follower proposal/forwarding
follower catch-up
quorum loss
quorum restoration
restart from durable log
snapshot
snapshot restore
compaction
add voter
remove voter
add learner
promote learner
incompatible replication version
incompatible snapshot version
clean standalone mode
```

Cluster sizes: 1 = standalone harness; 2 voters = both required, loss of either
-> writes unavailable; 3 = tolerate 1; 4 = still tolerate only 1; 5 = tolerate
2. Recommendation: **automatic HA -> >=3 voters, normally odd.**

## 10. Phase 7 sequence

```text
7A  architecture decision                      COMPLETE
7B  generic replicated-state harness           NEXT
7C  snapshot/catch-up/membership/versioning
7D  first Watchpost replicated-state adapter
7E  expand Watchpost replicated surface
7F  PostgreSQL HA contract
```

Cortex and Warden remain outside this phase unless they become cluster
participants.

## Frozen summary

```text
consensus engine:                hashicorp/raft (embedded; Gantry owns semantics above it)
replication unit:                logical durable operations (semantic envelope; never raw SQL or HTTP)
snapshot unit:                   filtered logical snapshot (replicated state + committed index +
                                 semantic format/version; node-local state excluded by construction)
voting model:                    voter / non-voter (learner) / ordinary cluster peer; cluster
                                 membership != voting rights; conf-changes via the library
write acknowledgement:           committed by quorum AND applied on the leader; proposed !=
                                 committed != responded
retry/idempotency:               same operation ID resolves to the already-committed result;
                                 durable across restart and snapshot/catch-up
minimum recommended HA topology: >=3 voters, normally odd (3/5); 2 voters = no safe auto-failover
node-local invariant:            node identity, peer credentials, pairing state, nonces, sessions,
                                 telemetry, scheduler execution state never replicated or snapshot-cloned
PostgreSQL policy:               PostgreSQL-native HA only; no Gantry database replication layer
first 7B proving target:         deterministic KV state-machine harness, 1/2/3/4/5 nodes, fault
                                 injection (quorum, election, stale-leader, restart, snapshot,
                                 membership, learner, idempotency, version fail-closed)
first 7D product slice:          pending dependency audit (options A-D; posts+rules not frozen)
```