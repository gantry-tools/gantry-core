# Gantry Core handover

Gantry Core is a dependency-light Go monorepo for behavior shared by Gantry applications. Packages must remain independently understandable and consumable: each has a README, public API, unit tests, and JSON compatibility fixtures.

Keep product transport, product SQL schemas, product policy, and browser UI in
consuming applications. Shared authentication primitives and contracts belong
here when they are product-neutral, configurable by an application adapter and
have at least two plausible consumers. A package must not import an application.

Changes to shared behavior must update fixtures first, pass `go test ./...` and `go test -race ./...`, and then pass the Cortex and Warden suites against the local module replacement. Compatibility fixtures are contracts: add cases rather than silently changing established outcomes.

## Release state

- Released stable: **v0.1.0** (stable public preview).
- Current development: **0.1.1** on `main`.
- Consumers should pin stable semantic versions rather than unpublished pseudo-versions.

## Release procedure

Run `go test ./...`, `go test -race ./...`, `go vet ./...`, and
`./scripts/test-workspace.sh`, then create an annotated `vMAJOR.MINOR.PATCH` tag
on the reviewed release commit. Push the commit before the tag. Gantry Core is a
Go module and has no platform-specific release archives.
