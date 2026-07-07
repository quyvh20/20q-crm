// Package authzgate holds the P9 authorization "grep gate" — a source-tree
// guard (implemented as a Go test so it runs in the existing `go test ./...` CI
// step) that fails the build if the name-keyed authorization bridges deleted in
// the P10 auth overhaul are reintroduced.
//
// The plan's R1 identity re-key (plan §3.1) moved every authorization decision
// off role NAMES and onto role IDs, then deleted the temporary name-fallback
// bridges in P9. This package pins that end state: it forbids the removed
// artifacts anywhere in the app packages, and forbids raw role-name equality
// comparisons in the core identity-enforcement files. Seeders, role templates,
// display helpers, and tests are out of scope by design (they legitimately name
// roles); see gate_test.go for the exact rules and rationale.
package authzgate
