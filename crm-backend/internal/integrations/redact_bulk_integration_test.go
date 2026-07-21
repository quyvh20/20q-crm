package integrations

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Bulk erasure. The single-contact hook has redacted since L2.7, so a customer
// honouring a data-protection request one person at a time was covered — and the
// same customer honouring it over a LIST was not, because the bulk delete never
// called anything. Exactly the case with the most subjects in it erased none of them.

func seedRedactableEvent(t *testing.T, db *gorm.DB, orgID uuid.UUID, recordID *uuid.UUID) uuid.UUID {
	t.Helper()
	e := &IntegrationEvent{
		ID:             uuid.New(),
		OrgID:          orgID,
		Status:         EventStatusProcessed,
		RawPayload:     []byte(`{"email":"subject@example.com","phone":"+15551234567"}`),
		Context:        []byte(`{"page_url":"https://x.test/form?utm_source=ads"}`),
		ResultRecordID: recordID,
	}
	require.NoError(t, db.Create(e).Error)
	// Consent is unmapped on the struct, so it is written the way production writes it.
	if recordID != nil {
		_, err := NewRepository(db).SetEventConsent(context.Background(), e.ID,
			[]byte(`{"basis":"consent","text":"I agree","_crm":{"enforced":false}}`))
		require.NoError(t, err)
	}
	return e.ID
}

func readRedaction(t *testing.T, db *gorm.DB, id uuid.UUID) (raw, ctxJSON, consent string) {
	t.Helper()
	var row struct {
		RawPayload string
		Context    string
		Consent    *string
	}
	require.NoError(t, db.Raw(
		`SELECT raw_payload::text AS raw_payload, context::text AS context, consent::text AS consent
		   FROM integration_events WHERE id = ?`, id).Scan(&row).Error)
	c := ""
	if row.Consent != nil {
		c = *row.Consent
	}
	return row.RawPayload, row.Context, c
}

func TestRedactForRecords_ErasesEverySubjectInOneCall(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	a, b, keep := uuid.New(), uuid.New(), uuid.New()
	evA := seedRedactableEvent(t, db, orgID, &a)
	evB := seedRedactableEvent(t, db, orgID, &b)
	evKeep := seedRedactableEvent(t, db, orgID, &keep)

	require.NoError(t, repo.RedactForRecords(ctx, orgID, []uuid.UUID{a, b}))

	for _, id := range []uuid.UUID{evA, evB} {
		raw, c, consent := readRedaction(t, db, id)
		require.Equal(t, "{}", raw, "the payload the subject supplied must be gone")
		require.Equal(t, "{}", c, "the capture context must be gone")
		// A tombstone, not NULL and not {}: either would make the ledger assert that
		// consent was never obtained, which is a different and false claim from
		// "it was obtained and the record of it was erased on request".
		require.Contains(t, consent, `"redacted": true`)
	}

	raw, _, _ := readRedaction(t, db, evKeep)
	require.Contains(t, raw, "subject@example.com",
		"a contact that was not deleted must keep its ledger intact")
}

// The delivery history survives the erasure — that is the whole design: the row and
// its status answer "what happened to this lead", and only what the subject supplied
// is removed.
func TestRedactForRecords_KeepsTheDeliveryHistory(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	rec := uuid.New()
	ev := seedRedactableEvent(t, db, orgID, &rec)
	require.NoError(t, repo.RedactForRecords(context.Background(), orgID, []uuid.UUID{rec}))

	var got IntegrationEvent
	require.NoError(t, db.First(&got, "id = ?", ev).Error)
	require.Equal(t, EventStatusProcessed, got.Status)
	require.NotNil(t, got.ResultRecordID)
}

func TestRedactForRecords_IsOrgScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)

	rec := uuid.New()
	ev := seedRedactableEvent(t, db, orgA, &rec)

	// Same record id, another workspace asking. Erasure is a destructive write, so a
	// missing org predicate would let any workspace destroy another's ledger evidence
	// by guessing an id.
	require.NoError(t, repo.RedactForRecords(context.Background(), orgB, []uuid.UUID{rec}))
	raw, _, _ := readRedaction(t, db, ev)
	require.Contains(t, raw, "subject@example.com", "another org must not be able to erase this")
}

func TestRedactForRecords_EmptyInputIsANoOp(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	rec := uuid.New()
	ev := seedRedactableEvent(t, db, orgID, &rec)
	// `IN ()` is a syntax error in Postgres, and an empty slice must not become
	// "redact everything" either.
	require.NoError(t, repo.RedactForRecords(context.Background(), orgID, nil))
	raw, _, _ := readRedaction(t, db, ev)
	require.Contains(t, raw, "subject@example.com")
}

// The batch form must erase exactly what the singular form does — they are one rule
// with two shapes, and a divergence would mean bulk erasure quietly leaving something
// behind that single erasure removes.
func TestRedactForRecords_MatchesTheSingularForm(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	one, many := uuid.New(), uuid.New()
	evOne := seedRedactableEvent(t, db, orgID, &one)
	evMany := seedRedactableEvent(t, db, orgID, &many)

	require.NoError(t, repo.RedactForRecord(ctx, orgID, one))
	require.NoError(t, repo.RedactForRecords(ctx, orgID, []uuid.UUID{many}))

	rawA, ctxA, consentA := readRedaction(t, db, evOne)
	rawB, ctxB, consentB := readRedaction(t, db, evMany)
	require.Equal(t, rawA, rawB)
	require.Equal(t, ctxA, ctxB)

	var a, b map[string]any
	require.NoError(t, json.Unmarshal([]byte(consentA), &a))
	require.NoError(t, json.Unmarshal([]byte(consentB), &b))
	require.Equal(t, a, b, "one erasure rule, two shapes")
}
