// Package emailutil holds the one email-normalization rule the app keys on:
// lowercase + trim. It is a tiny leaf (imports only the stdlib) so any package
// can depend on it without pulling in a heavy module.
//
// The same two-op rule is currently inlined in three other places
// (integrations/ingest.go, usecase/auth_usecase.go, repository/contact_repository.go).
// New code should import this; those copies can collapse onto it later. Divergent
// normalization is a real hazard — a suppression written under one casing and a
// lookup done under another silently miss each other.
package emailutil

import "strings"

// Normalize returns the storage/lookup form of an email address: lowercased and
// whitespace-trimmed. It does NOT validate — an empty or malformed input passes
// through as its normalized (possibly empty) form.
func Normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
