# Operation contracts

`operation` is the versioned product-neutral link between a product service
operation and its HTTP and CLI adapters. It describes routing, authorization,
schemas, auditing, idempotency, automation exceptions and secret fields; it
does not implement product policy or handlers.

Registries reject duplicate operation IDs, HTTP routes and CLI commands. An
automatable operation cannot be registered without a CLI mapping, mutations
must be audited, and cluster-node authorization is structurally distinct from
human capability authorization.
