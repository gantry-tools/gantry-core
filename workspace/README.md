# workspace

`workspace` provides a symlink-aware filesystem boundary. `New` canonicalizes an existing root; `Resolve` accepts existing paths, `ResolveForCreate` validates an existing parent, and `Status` classifies historical paths without turning them into executable paths.

Authorization and filesystem APIs remain with consumers. A successful status is informational; callers must resolve again immediately before use.
