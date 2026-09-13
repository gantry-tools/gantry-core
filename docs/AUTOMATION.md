# Gantry CLI automation contract

The shared functional CLI is designed for SSH, CI, Ansible, shell scripts and agents.

- Use `--input file.json` or `--input -`; secret credentials are never accepted as command-line values.
- Use `--token-file` for scoped API tokens or `--session-file` for a persisted browser/CLI session. On Unix, credential files must not be accessible by group or others; generated session files are `0600`.
- Use `--json` for machine output, repeatable `--query key=value` for pagination/filter parameters, and `--timeout` to bound network operations.
- Destructive operations require `--yes` in non-interactive automation.
- `--request-id` is sent as `X-Request-ID` and, when the operation declares idempotency support, also as `Idempotency-Key`.
- Exit codes are stable: 0 success, 1 failure, 2 usage/confirmation, 3 authentication/authorization, 4 not found, 5 conflict, 6 unavailable/timeout, 7 partial success (used by distributed commands).
- Cluster fan-out keeps per-node results and partial failures; callers must not treat a partial result as complete success.

Example:

```sh
printf '%s\n' '{"name":"example"}' | \
  product resources create --input - --session-file "$HOME/.config/product/session.json" --json --request-id build-42
```
