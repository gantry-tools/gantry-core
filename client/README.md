# Operation clients

`client` executes the same `operation.Contract` locally or through HTTP. The
remote client supports bounded JSON, path/query values, cancellation, request
and idempotency IDs, protected token sources and conservative retries. Reads
may retry; mutations retry only when the contract supports idempotency and the
caller supplies a valid key.

`LocalExecutor` authorizes an `auth.Execution` before invoking a product-owned
handler. Products therefore keep one business operation behind their HTTP and
CLI adapters instead of maintaining a second CLI implementation.
