package integrations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// The reconciliation poller is the leadgen webhook's backstop: it re-pulls a live
// form's recent leads and imports any a dropped webhook delivery never delivered.
// These pin the two properties that make it safe — it recovers the missed lead, and
// it does so EXACTLY once (connection-scoped dedupe + the caught-up stop), so a poll
// that runs every 30 minutes forever never writes a duplicate.

func TestReconcile_RecoversAWebhookDroppedLeadExactlyOnce(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	src := s.enableForm(t, org, c.ID, "form1")

	// The fleet sweep must pick this live facebook_form up.
	reconcilable, err := s.repo.ListReconcilableFacebookForms(ctx)
	require.NoError(t, err)
	require.Len(t, reconcilable, 1)
	require.Equal(t, src.ID, reconcilable[0].ID)

	h := s.backfillHandler(t)

	// A webhook never delivered BL1; the poller (advisory lock included) recovers it.
	h.runReconcile(ctx)
	require.Equal(t, 1, s.writer.creates, "the poller must import the lead the webhook missed")

	ev := s.eventByLeadgen(t, "BL1")
	require.NotNil(t, ev)
	require.Equal(t, EventStatusProcessed, ev.Status)

	// A second tick is a no-op: the lead is already in the ledger (deduped), and the
	// caught-up stop means the walk ends on the first all-seen page.
	h.runReconcile(ctx)
	require.Equal(t, 1, s.writer.creates, "a healthy re-poll must not re-import an already-recovered lead")
}

func TestReconcile_DedupesAgainstAWebhookDeliveredLead(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	_ = s.enableForm(t, org, c.ID, "form1")

	// BL1 already arrived by webhook: a connection-scoped event exists for it.
	leadgen := "BL1"
	existing := &IntegrationEvent{
		OrgID: org, ConnectionID: &c.ID, ProviderEventID: &leadgen,
		Status: EventStatusProcessed, RawPayload: datatypes.JSON(`{}`),
	}
	rid := uuid.New()
	existing.ResultRecordID = &rid
	inserted, err := s.repo.InsertEventDeduped(ctx, existing)
	require.NoError(t, err)
	require.True(t, inserted)

	h := s.backfillHandler(t)
	h.runReconcile(ctx)
	require.Equal(t, 0, s.writer.creates, "the poller must not re-write a lead the webhook already delivered")
}

// The fleet query must sweep ONLY live forms behind live connections — a disabled
// source or a disconnected connection has nothing to reconcile.
func TestListReconcilableFacebookForms_ExcludesDisabledAndDisconnected(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	src := s.enableForm(t, org, c.ID, "form1")

	got, err := s.repo.ListReconcilableFacebookForms(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// A disabled source drops out.
	require.NoError(t, db.Exec(`UPDATE lead_sources SET status = ? WHERE id = ?`, SourceStatusDisabled, src.ID).Error)
	got, err = s.repo.ListReconcilableFacebookForms(ctx)
	require.NoError(t, err)
	require.Empty(t, got, "a disabled source must not be reconciled")

	// Re-enabled, but a disconnected connection also drops it out.
	require.NoError(t, db.Exec(`UPDATE lead_sources SET status = ? WHERE id = ?`, SourceStatusActive, src.ID).Error)
	require.NoError(t, db.Exec(`UPDATE integration_connections SET status = 'disconnected' WHERE id = ?`, c.ID).Error)
	got, err = s.repo.ListReconcilableFacebookForms(ctx)
	require.NoError(t, err)
	require.Empty(t, got, "a form behind a disconnected connection must not be reconciled")
}
