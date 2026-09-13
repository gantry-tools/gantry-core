# Phase 5 functional API/CLI certification

Phase 5 replaces the Phase 1 smoke slice with generated, CI-pinned operation matrices and a common non-interactive HTTP CLI executor. Browser-only, streaming and service/node protocols are explicitly classified and are not counted as missing human CLI coverage.

| Product | Declared operations | Website-used | Observed CLI mappings |
| --- | ---: | ---: | ---: |
| Cortex | 31 | 29 | 29 |
| Warden | 73 | 72 | 70 |
| Trestle | 101 | 100 | 101 |
| Watchpost | 102 | 86 | 90 |
| Watchpost Agent | 21 | 19 | 21 |
| Webfleet | 88 | 81 | 80 |

## Certification rules

- Every declared operation has independent test evidence.
- Every automatable operation has a named input/output schema and an observed CLI mapping.
- Generated JSON/Markdown matrices are checked byte-for-byte in product tests.
- Mutations/destructive operations require audit metadata; destructive CLI execution requires `--yes`.
- Public destructive operations and invalid token-boundary combinations fail shared security certification.
- Token/session credentials come from protected files; Unix files accessible by group/others are rejected.
- Session CSRF handling is product-configured but executed by the same shared CLI path.
- Request IDs become idempotency keys only where the operation contract declares idempotency support.
- Stable exit codes distinguish usage, authorization, not-found, conflict, unavailable and distributed partial-success outcomes.

## Scope boundary

The matrices certify declared functional HTTP operations and their CLI mappings. Existing service-management commands and product-local direct machine-state commands remain separate first-class command families. Browser redirects/OAuth callbacks, streaming protocols, collector/Agent ingestion and cluster-node transport are classified rather than falsely exposed as ordinary human CLI commands.

This phase does not add database replication or configuration propagation. Those remain Phase 6 concerns.

## Validation environment

Gantry Core's focused automation/contract/operation/CLI suites run directly in this workspace. Product operation-package suites are also exercised in disposable validation copies. The execution sandbox cannot reach the Go module proxy or download the Go 1.25 toolchain, so product-wide Go 1.25 `go test ./...`, `-race`, and `go vet ./...` remain a required first rerun in the normal development environment where uncached dependencies are available. No production `go.mod` or dependency was downgraded to claim those gates.
