# Gantry Core

Shared, dependency-light Go building blocks extracted from Cortex and Warden.

| Package | Owns | Does not own |
| --- | --- | --- |
| `agent` | run-state ordering, outcomes, recovery reconciliation | providers, subprocess launch, HTTP streaming |
| `conversations` | durable event shape, validation and transcript merge rules | SQL schemas, tenancy, HTTP handlers |
| `workspace` | root confinement, symlink checks and availability status | authorization, browsing APIs |
| `editor` | text matching, binary detection, replacement and atomic writes | tabs, browser state, workspace authorization |
| `terminal` | session validation/defaults and bounded UTF-8 scrollback | PTY/WebSocket implementation, persistence, authorization |

Cross-product conventions are documented separately from executable packages.
See [Gantry application routing and authentication convention](docs/ROUTING_AND_AUTH.md)
for the canonical `/`, `/app/` and `/manage/` layout and the shared security
vocabulary.

The packages intentionally remain separate even in one module. In particular, conversations are not nested under agent: a transcript can be imported, searched, rendered or migrated without starting an agent, while an agent runner can be used with a different persistence model.

Run the full contract suite with:

```sh
go test ./...
go test -race ./...
```

When this repository is checked out beside the supplied Cortex and Warden
trees, run the contracts and both consumer suites together with:

```sh
./scripts/test-workspace.sh
```

`CORTEX_DIR` and `WARDEN_DIR` may override the default sibling paths. The
script intentionally tests both consumers against this checkout through their
local module replacements, so one change exercises the complete dogfood set.

Consumers use normal Go imports such as `github.com/gantry-tools/gantry-core/conversations`. Cortex and Warden pin an exact Gantry Core revision so their builds remain reproducible from standalone clones.
