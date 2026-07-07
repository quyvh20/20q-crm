package ai

import (
	"context"
	"encoding/json"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// p8_test.go covers the P8 audit-attribution addition: the AI writes custom
// objects via customObjUC directly (bypassing RecordService's audit chokepoint),
// so it must stamp the object_audit trail itself. These verify that a successful
// AI create/update emits exactly one audit row attributed to the acting user.

// fakeCustomObjUC satisfies domain.CustomObjectUseCase by embedding it (nil) and
// overriding only the two write methods the audit test exercises.
type fakeCustomObjUC struct {
	domain.CustomObjectUseCase
	rec *domain.CustomObjectRecord
}

func (f *fakeCustomObjUC) CreateRecord(context.Context, uuid.UUID, uuid.UUID, string, domain.CreateRecordInput) (*domain.CustomObjectRecord, error) {
	return f.rec, nil
}
func (f *fakeCustomObjUC) UpdateRecord(context.Context, uuid.UUID, string, uuid.UUID, domain.UpdateRecordInput) (*domain.CustomObjectRecord, error) {
	return f.rec, nil
}

// fakeDealRepo satisfies domain.DealRepository by embedding it (nil) and overriding
// the two methods toolUpdateDeal uses.
type fakeDealRepo struct {
	domain.DealRepository
	deal *domain.Deal
}

func (f *fakeDealRepo) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Deal, error) {
	return f.deal, nil
}
func (f *fakeDealRepo) Update(context.Context, *domain.Deal) error { return nil }

func TestAI_DealUpdate_EmitsAudit(t *testing.T) {
	dealID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()
	az := &fakeAuthz{allow: map[string]bool{"deal:edit": true}}
	cc := &CommandCenter{
		authz:    az,
		dealRepo: &fakeDealRepo{deal: &domain.Deal{ID: dealID, OrgID: orgID, Title: "Acme"}},
		logger:   zap.NewNop(),
	}
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: userID, RoleID: uuid.New()})

	out := cc.toolUpdateDeal(ctx, orgID, userID, "custom", map[string]interface{}{
		"deal_id":     dealID.String(),
		"status":      "won",
		"probability": float64(100),
	})
	var res map[string]any
	require.NoError(t, json.Unmarshal(out, &res))
	require.Equal(t, true, res["updated"], "deal update should succeed: %s", string(out))

	require.Len(t, az.audits, 1, "an AI deal edit (via dealRepo, bypassing RecordService) must emit an audit row")
	got := az.audits[0]
	assert.Equal(t, domain.ActionEdit, got.Action)
	assert.Equal(t, userID, got.ActorID)
	assert.Equal(t, "deal", got.ObjectSlug)
	assert.Equal(t, dealID, got.RecordID)
	assert.Contains(t, got.Changes, "status")
}

func TestAI_CustomObjectCreate_EmitsAudit(t *testing.T) {
	recID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()
	az := &fakeAuthz{allow: map[string]bool{"ticket:create": true}}
	cc := &CommandCenter{
		authz:       az,
		customObjUC: &fakeCustomObjUC{rec: &domain.CustomObjectRecord{ID: recID, DisplayName: "T1"}},
		logger:      zap.NewNop(),
	}

	out := cc.toolCreateObjectRecord(context.Background(), orgID, userID, map[string]interface{}{
		"object_slug":  "ticket",
		"display_name": "T1",
		"fields":       map[string]interface{}{"status": "open"},
	})
	var res map[string]any
	require.NoError(t, json.Unmarshal(out, &res))
	require.Equal(t, true, res["success"], "create should succeed: %s", string(out))

	require.Len(t, az.audits, 1, "a successful AI custom-object create must emit one audit row")
	got := az.audits[0]
	assert.Equal(t, domain.ActionCreate, got.Action)
	assert.Equal(t, userID, got.ActorID, "audit actor is the acting user")
	assert.Equal(t, orgID, got.OrgID)
	assert.Equal(t, "ticket", got.ObjectSlug)
	assert.Equal(t, recID, got.RecordID)
	assert.Contains(t, got.Changes, "status")
}

func TestAI_CustomObjectUpdate_EmitsAudit(t *testing.T) {
	recID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()
	az := &fakeAuthz{allow: map[string]bool{"ticket:edit": true}}
	cc := &CommandCenter{
		authz:       az,
		customObjUC: &fakeCustomObjUC{rec: &domain.CustomObjectRecord{ID: recID, DisplayName: "T1"}},
		logger:      zap.NewNop(),
	}
	// The update path attributes the actor from the request-context caller.
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: userID, RoleID: uuid.New()})

	out := cc.toolUpdateObjectRecord(ctx, orgID, map[string]interface{}{
		"object_slug": "ticket",
		"record_id":   recID.String(),
		"fields":      map[string]interface{}{"status": "closed"},
	})
	var res map[string]any
	require.NoError(t, json.Unmarshal(out, &res))
	require.Equal(t, true, res["success"], "update should succeed: %s", string(out))

	require.Len(t, az.audits, 1, "a successful AI custom-object update must emit one audit row")
	got := az.audits[0]
	assert.Equal(t, domain.ActionEdit, got.Action)
	assert.Equal(t, userID, got.ActorID)
	assert.Equal(t, "ticket", got.ObjectSlug)
	assert.Equal(t, recID, got.RecordID)
	assert.Contains(t, got.Changes, "status")
}
