# conversations

`conversations` defines the portable event model and deterministic merge rules used when server-owned agent events and browser-authored transcript events meet. It also validates opaque record identifiers and bounded conversation payloads.

It is separate from `agent` because conversation history remains useful without a running agent: import/export, search, rendering, archival and migrations should not depend on execution machinery.
