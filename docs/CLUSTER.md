# Cluster package

The `cluster` package contains generic distributed-control primitives proven in Watchpost before extraction.

## Stability boundary

Protocol semantics are versioned independently from product releases through `cluster.ProtocolVersion`. A consumer must fail closed when its peer protocol is incompatible. Product capabilities are negotiated explicitly; protocol compatibility alone does not grant an operation.

The package is deliberately persistence-free. Consumers persist identities, invitations, memberships, nonces and credentials themselves, then use Core to validate values, sign/verify requests, parse targets and aggregate bounded fan-out results.

## Security contract

- Node credentials are distinct from human and product-agent credentials.
- Public cluster endpoints use HTTPS.
- Request signatures bind method, URI, timestamp, nonce, request ID, capability and body digest.
- Receivers enforce bounded bodies, clock skew and one-time replay nonces in their persistence adapter.
- Rotation is an overlap ceremony; consumers decide when a newly used credential replaces the previous one.
- Revoked or disabled members must be rejected before request execution.

## Target contract

`ParseTarget` accepts `local`, `members`, `all`, `node:<id>`, and the legacy bare node-ID form used by Watchpost Phase 2. `FanOut` bounds concurrency and returns sorted per-node result envelopes. Partial failure remains visible rather than being converted to an overall success.
