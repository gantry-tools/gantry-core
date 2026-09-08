# Gantry Core handover

Gantry Core is a dependency-light Go monorepo for behavior shared by Gantry applications. Packages must remain independently understandable and consumable: each has a README, public API, unit tests, and JSON compatibility fixtures.

Keep transport, authentication, SQL schemas, product policy, and browser UI in consuming applications. A package belongs here only when its behavior has at least two plausible consumers and can be expressed without importing an application.

Changes to shared behavior must update fixtures first, pass `go test ./...` and `go test -race ./...`, and then pass the Cortex and Warden suites against the local module replacement. Compatibility fixtures are contracts: add cases rather than silently changing established outcomes.
