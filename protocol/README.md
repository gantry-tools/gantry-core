# Protocol primitives

`protocol` supplies bounded strict JSON decoding, structured errors with HTTP
and CLI mappings, pagination defaults, exact confirmations, request/idempotency
key validation, JSON-pointer redaction and deterministic JSON/table/plain
rendering.

It deliberately contains no product schemas or policy. Consumers declare those
through `operation.Contract` and use these primitives consistently at their
HTTP and CLI edges.
