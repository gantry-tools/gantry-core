# Cross-project behavioral contracts

These contracts capture behavior that Cortex and Warden implemented independently before extraction.

1. Agent terminal outcomes are ordered by observed evidence, not error strings. Provider/stdout failures before valid completion win; explicit local causes retain their distinct outcome; a clean exit without valid completion evidence is failed.
2. Conversation reload merges server-owned and client-authored events occurrence-by-occurrence. Server copies win timestamp ties, recovery replacements supersede same-run fragments only, and only the latest terminal marker for a run remains.
3. Workspace resolution is lexical and symlink-aware. Existing targets and parents may not escape the configured root. Historical paths can be classified without being rewritten or accepted for execution.
4. Editor matching uses Go RE2 for regex mode, literal quoting otherwise, optional case folding, binary-file rejection, and bounded replacement. Atomic writes preserve the existing file mode.
5. Terminal metadata uses validated opaque identifiers, bounded titles/CWD values, explicit state defaults, valid UTF-8 scrollback, and a hard byte tail limit.
6. Self-hosted applications use the route and authorization vocabulary in
   `ROUTING_AND_AUTH.md`; application adapters may add product capabilities but
   may not weaken the shared session, CSRF or management boundaries.

Every package keeps executable JSON fixtures under `testdata/compatibility.json`. Application adapters are deliberately thin so the same fixture-defined behavior reaches both products.
