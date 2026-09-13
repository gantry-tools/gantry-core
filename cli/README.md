# CLI contract

`cli` defines the common side-effect-free command grammar and stable exit-code
taxonomy used by Gantry applications. It accepts options before or after the
`resource verb` pair, supports JSON from files or stdin and provides explicit
machine-readable output, confirmation, timeout and remote-target options.

Raw credentials are intentionally not accepted as command arguments. A remote
token is supplied through a protected file or a consumer-provided credential
source so it does not appear in ordinary shell history or process listings.
