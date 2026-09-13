# Gantry Core handover

Gantry Core is a dependency-light Go monorepo for behavior shared by Gantry applications. Packages must remain independently understandable and consumable: each has a README, public API, unit tests, and JSON compatibility fixtures.

Keep product transport, product SQL schemas, product policy, and browser UI in
consuming applications. Shared authentication primitives and contracts belong
here when they are product-neutral, configurable by an application adapter and
have at least two plausible consumers. A package must not import an application.

Changes to shared behavior must update fixtures first, pass `go test ./...` and `go test -race ./...`, and then pass the Cortex and Warden suites against the local module replacement. Compatibility fixtures are contracts: add cases rather than silently changing established outcomes.

## CLI/API and distributed-management campaign

`docs/CLI_API_CLUSTER_ROADMAP.md` is the canonical execution plan for making
every Gantry Go application fully operable through its HTTP API and CLI, then
adding clustering and controlled configuration propagation. Work through its
checkpoints in order. Commit each checkpoint independently after its focused
acceptance gate, reassess the remaining order after every checkpoint, and edit
the roadmap rather than silently departing from it.

The campaign's architectural boundaries are mandatory:

- human accounts, roles, sessions and API tokens remain local to one product
  installation; Gantry Core supplies contracts, not a shared Gantry login;
- cluster peers use separate node/service identities and never impersonate a
  human account;
- website, HTTP API and CLI entry points authorize and invoke the same product
  service operation instead of reimplementing business logic;
- Watchpost proves clustering locally before generic cluster code is extracted;
- propagation is explicit, versioned, diffable and audited configuration or
  policy distribution, not hidden database replication; and
- standalone installations remain fully supported throughout the campaign.

## Release state

- Released stable: **v0.1.1** (stable public preview).
- Current development: **0.1.1** on the release commit. Advance `main` to
  **0.1.2** only after the coordinated Phase 1 consumer commits are complete.
- Consumers should pin stable semantic versions rather than unpublished pseudo-versions.

## Release procedure

Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and
`./scripts/test-workspace.sh`, then create an annotated `vMAJOR.MINOR.PATCH` tag
on the reviewed release commit. Push the commit before the tag. Gantry Core is a
Go module and has no platform-specific release archives.
