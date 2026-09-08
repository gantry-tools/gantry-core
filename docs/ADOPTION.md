# Consumer adoption

Cortex and Warden consume a pinned revision of the canonical Go module:

```go
require github.com/gantry-tools/gantry-core <version>
```

The adapters retain product-specific names and wire types while delegating the
behavioral decision to the shared package. This keeps existing HTTP, database,
authentication and frontend contracts stable during dogfooding.

| Contract | Cortex | Warden |
| --- | --- | --- |
| Agent state and outcomes | `internal/app/runoutcome.go` | `internal/server/runoutcome.go` |
| Agent recovery and argv | `internal/app/agent.go` | `internal/server/agent.go` |
| Conversation merge | `internal/app/conversations.go` | `internal/server/conversations.go` |
| Workspace boundary | `internal/app/app.go` | `internal/server/files.go` |
| Editor primitives | future consumer | `internal/server/workspace.go`, `files.go` |
| Terminal primitives | future consumer | `internal/server/terminal_sessions.go` |

`conversations` remains a peer of `agent`, not its child. Durable transcripts
have independent consumers and lifecycles—search, export, archival, rendering
and migrations—and should not require process-running dependencies.
