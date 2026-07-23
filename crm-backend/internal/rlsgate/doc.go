// Package rlsgate holds the Row Level Security "grep gate" — a source-tree
// guard (implemented as a Go test so it rides the existing `go test -short ./...`
// CI step, whose success the deploy job depends on) that pins the fix for the
// 2026-07-20 Supabase alert rls_disabled_in_public.
//
// Background: golang-migrate is dirty at v2 on the production database, so the
// numbered migrations/*.up.sql files never execute there — only the idempotent
// boot guards in cmd/server/main.go reach prod. The ENABLE ROW LEVEL SECURITY
// statements for the oldest, most sensitive tables (users, refresh_tokens,
// org_users, contacts, chat_messages, …) were stranded in migrations 000008 and
// 000013, so those tables shipped to a Supabase project with RLS off — meaning
// the project's public anon key was a read/write handle on them through
// PostgREST, this backend's org scoping and audit bypassed entirely.
//
// The fix is a pg_class sweep boot guard in main.go that enables RLS on every
// public table where relrowsecurity is false. This package pins two invariants
// that keep that fix load-bearing:
//
//   - the sweep must stay present (TestRLSSweepBootGuardPresent); and
//   - FORCE ROW LEVEL SECURITY must never appear (TestNoForceRowLevelSecurity),
//     because the app connects as the table owner and an owner only loses its
//     RLS bypass under FORCE — with zero policies that is an instant total
//     outage, the exact way this remediation could be turned into one.
//
// TestEveryBootGuardTableHasRLS is the fallback: if a future change ever
// replaces the sweep with an explicit per-table list, it fails the build for any
// CREATE TABLE boot guard that lacks a matching ENABLE ROW LEVEL SECURITY.
package rlsgate
