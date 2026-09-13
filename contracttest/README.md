# Contract test harness

`contracttest` compares a product's executable operation declarations with the
HTTP routes and website operations observed by that product's tests. It reports
undeclared or unobserved routes, UI operations without contracts, stale or
unexplained exceptions, duplicate contracts, missing CLI mappings and missing
mutation audit requirements in deterministic order.

The package does not claim that the CP1 source scanner proves coverage. Each
consumer supplies runtime-aware observations during CP8 and Phase 5.
