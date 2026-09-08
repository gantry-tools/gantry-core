# editor

`editor` holds text-engine behavior shared by browser editors: literal/RE2 matching, case sensitivity, binary detection, bounded replacement, optimistic revision checks, and permission-preserving atomic writes.

It intentionally does not define tabs, layout, HTTP endpoints, authorization or workspace traversal. Consumers combine it with the `workspace` boundary.
