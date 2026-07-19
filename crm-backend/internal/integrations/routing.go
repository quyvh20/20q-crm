package integrations

import (
	"context"
	"encoding/json"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Owner routing: which human a captured lead lands on.
//
// This is the difference between a lead and a lost lead. An unowned contact is
// invisible to every own-scoped rep, and a contact owned by someone who has left is
// worse — it looks correctly assigned in every list, so nobody triages it, while the
// person named cannot reach it.

// maxOwnerPool bounds a rotation. Not a technical limit: a pool larger than this is
// almost always a mis-click (an admin selecting the whole workspace), and the cap is
// where that gets caught rather than at 3am when every lead lands on a stranger.
const maxOwnerPool = 25

// IsLiveMember reports whether a person may be handed a lead — the Go twin of
// repository.activeMemberSQL. One rule in two languages; change one and you must
// change the other, which is why a shared fixture set tests both.
//
// A nil OrgUser means no membership row at all (removal hard-deletes it), which is
// the real signal that someone has left.
func IsLiveMember(ou *domain.OrgUser) bool {
	return ou != nil && ou.DeletedAt == nil && ou.Status == "active"
}

// parsePoolUUIDs decodes a stored owner_pool.
//
// Junk is skipped, never fatal. A hand-edited row or a half-written migration must
// degrade this source to its fallback owner, not fail its leads: the pool is
// routing configuration, and configuration being wrong is not a reason to drop a
// customer's lead on the floor.
func parsePoolUUIDs(raw datatypes.JSON) []uuid.UUID {
	if len(raw) == 0 {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil
	}
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// OwnerDecision is the outcome of routing one lead, including what to tell the
// human about it.
type OwnerDecision struct {
	// OwnerID is who the lead lands on; nil means deliberately unowned.
	OwnerID *uuid.UUID
	// Note explains a routing fallback on the delivery record. Empty when routing
	// did the ordinary thing.
	Note string
	// Warning is the same fact said to the CALLER, at integration time. A lead that
	// nobody owns must not be discoverable only by reading the ledger later.
	Warning string
}

// resolveOwner picks the owner for a NEW record, walking a three-rung ladder:
// a live pool member by rotation, else the fallback owner, else deliberately
// unowned and loud about it.
//
// It returns NO ERROR, by construction. Every degraded path — a dead pool, a DB
// hiccup on the cursor, unreadable JSONB, a failed liveness query — has to end in a
// WRITTEN lead. A 500 to a one-shot Make or Facebook delivery usually means the lead
// exists nowhere; an unowned contact at least exists, sits in the ledger, and any
// all-scope admin can find it.
//
// priorOwner short-circuits the whole thing on an Idempotency-Key retry: the ticket
// belongs to the DELIVERY, not the attempt.
func (s *LeadIngestService) resolveOwner(ctx context.Context, source *LeadSource, lead RawLead, priorOwner *uuid.UUID) OwnerDecision {
	if priorOwner != nil {
		return OwnerDecision{OwnerID: priorOwner}
	}

	// A test lead PEEKS: it must report the rep a real lead would get without
	// consuming that rep's turn. Burning turns on synthetic leads skews the rotation
	// and hands a live rep a contact they must delete.
	ticket, rawPool, pooled, err := s.nextTicket(ctx, source, lead.IsTest())
	if err != nil {
		s.logf("integrations: owner rotation unavailable, falling back", "source_id", source.ID.String(), "error", err)
		pooled = false
	}
	if !pooled {
		// No rotation configured. L1 behaviour, byte for byte: stamp the configured
		// owner unchecked. Deliberately NOT liveness-checked — a source that never
		// opted into this feature must not silently change behaviour, and a suspension
		// is routinely reversible (leave, investigation).
		return OwnerDecision{OwnerID: source.DefaultOwnerID}
	}

	pool := parsePoolUUIDs(rawPool)
	if len(pool) == 0 {
		return OwnerDecision{OwnerID: source.DefaultOwnerID}
	}

	// The fallback rides the same IN-list, so verifying it costs nothing extra.
	probe := append(append([]uuid.UUID{}, pool...), derefUUIDs(source.DefaultOwnerID)...)
	liveIDs, lerr := s.members.ActiveMemberIDs(ctx, source.OrgID, probe)
	if lerr != nil {
		// FAIL OPEN — the opposite polarity to the daily cap, on purpose. "A liveness
		// result we could not obtain is not evidence the pool is dead." Treating an
		// error as an empty result makes a DB blip indistinguishable from a dead pool
		// and unowns real leads; rotating unverified risks at worst a suspended rep
		// briefly holding one, which is visible and reversible.
		s.logf("integrations: membership check failed, rotating unverified", "source_id", source.ID.String(), "error", lerr)
		return OwnerDecision{OwnerID: &pool[nonNegMod(ticket, len(pool))]}
	}

	return pickOwner(ticket, pool, liveIDs, source.DefaultOwnerID)
}

// pickOwner is the rotation's decision logic, kept pure so the fairness properties
// can be tested as sequences rather than as distributions.
//
// The distinction matters: "each member got roughly a third" passes identically for
// a uniform random picker, which is exactly what a subtly wrong implementation
// produces here (see the filter comment below).
func pickOwner(ticket int64, pool []uuid.UUID, liveIDs map[uuid.UUID]bool, fallback *uuid.UUID) OwnerDecision {
	// Filter FIRST, then modulo. Ranging the liveness MAP instead of this slice would
	// be the subtle killer: Go randomizes map iteration, so the rotation would become
	// a uniform random draw and the persisted cursor decorative.
	//
	// Filtering first is also what makes a suspension fair: pool [A,B,C] with B
	// suspended alternates A,C,A,C (50/50). Taking the modulo first and then skipping
	// forward gives A,C,C,A,C,C — C silently absorbs B's entire share.
	live := make([]uuid.UUID, 0, len(pool))
	for _, id := range pool {
		if liveIDs[id] {
			live = append(live, id)
		}
	}

	// This emptiness check MUST stay adjacent to the modulo below it: `n % 0` is a Go
	// divide-by-zero panic, and a panic here strands the delivery in `processing`,
	// which poisons its Idempotency-Key into a permanent 409 — turning a routing bug
	// into permanent lead loss.
	if len(live) == 0 {
		if fallback != nil && liveIDs[*fallback] {
			return OwnerDecision{
				OwnerID: fallback,
				Note:    "everyone in this source's rotation is suspended or has left the workspace, so this lead went to the fallback owner",
			}
		}
		return OwnerDecision{
			Note:    "nobody in this source's rotation is active and there is no fallback owner, so this lead is unassigned",
			Warning: "this lead is unassigned — reps who only see their own records will not see it",
		}
	}
	return OwnerDecision{OwnerID: &live[nonNegMod(ticket, len(live))]}
}

// nextTicket claims this lead's place in the rotation, or peeks at it.
func (s *LeadIngestService) nextTicket(ctx context.Context, source *LeadSource, peek bool) (int64, datatypes.JSON, bool, error) {
	if peek {
		return s.repo.PeekOwnerTicket(ctx, source.OrgID, source.ID)
	}
	return s.repo.NextOwnerTicket(ctx, source.OrgID, source.ID)
}

// nonNegMod keeps the index in range even if a cursor ever went negative. Go's %
// preserves the sign of the dividend, so a negative ticket would index out of
// bounds and panic on the capture path.
func nonNegMod(n int64, m int) int {
	if m <= 0 {
		return 0
	}
	i := int(n % int64(m))
	if i < 0 {
		i += m
	}
	return i
}

func derefUUIDs(p *uuid.UUID) []uuid.UUID {
	if p == nil {
		return nil
	}
	return []uuid.UUID{*p}
}

// logf is a nil-safe logger shim: routing must never panic because wiring forgot a
// logger, and unit tests construct the service without one.
func (s *LeadIngestService) logf(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Error(msg, args...)
	}
}

// joinNotes merges routing's explanation with any the pipeline already made.
//
// The delivery can legitimately carry two: an ambiguous-phone lead arrives as
// no-match WITH a note and then creates. Overwriting either way deletes a
// disclosure — one turns a duplicate contact from an unexplained bug into a
// documented decision, the other is the only signal a lead is unowned.
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "; " + b
	}
}

// warningsOf lifts a routing decision's caller-facing warning into the result.
func warningsOf(d OwnerDecision) []string {
	if d.Warning == "" {
		return nil
	}
	return []string{d.Warning}
}

// compile-time guard: the batched liveness check is part of the port, not an
// optional extra — resolveOwner's ladder depends on it.
var _ = func(m MemberChecker) {
	var _ func(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]bool, error) = m.ActiveMemberIDs
}
