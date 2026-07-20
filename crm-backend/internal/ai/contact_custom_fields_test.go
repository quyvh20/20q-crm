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

// fakeContactRepo satisfies domain.ContactRepository by embedding it (nil) and
// overriding only the two methods toolUpdateContact uses. It keeps the contact it
// was handed on Update so the test can inspect the blob that would be persisted.
type fakeContactRepo struct {
	domain.ContactRepository
	contact *domain.Contact
	saved   *domain.Contact
}

func (f *fakeContactRepo) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Contact, error) {
	return f.contact, nil
}

func (f *fakeContactRepo) Update(_ context.Context, c *domain.Contact) error {
	f.saved = c
	return nil
}

// An AI update names only the fields it is changing. Anything else on custom_fields
// must survive — most sharply the attribution stamped at lead capture, which the
// customer cannot reconstruct once it is gone.
func TestAI_ContactUpdate_PreservesUnmentionedCustomFields(t *testing.T) {
	contactID := uuid.New()
	orgID := uuid.New()
	userID := uuid.New()

	repo := &fakeContactRepo{contact: &domain.Contact{
		ID:        contactID,
		OrgID:     orgID,
		FirstName: "Ada",
		LastName:  "Lovelace",
		CustomFields: domain.JSON(`{
			"lead_source": "google_ads",
			"lead_source_detail": "Q3 Brand",
			"utm_campaign": "q3-brand",
			"shirt_size": "M"
		}`),
	}}
	cc := &CommandCenter{
		authz:       &fakeAuthz{allow: map[string]bool{"contact:edit": true}},
		contactRepo: repo,
		logger:      zap.NewNop(),
	}
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: userID, RoleID: uuid.New()})

	out := cc.toolUpdateContact(ctx, orgID, contactID, map[string]interface{}{
		"fields": map[string]interface{}{"shirt_size": "L"},
	})
	var res map[string]any
	require.NoError(t, json.Unmarshal(out, &res))
	require.Equal(t, true, res["success"], "update should succeed: %s", string(out))

	require.NotNil(t, repo.saved, "the contact should have been persisted")
	var got map[string]any
	require.NoError(t, json.Unmarshal(repo.saved.CustomFields, &got))

	assert.Equal(t, "L", got["shirt_size"], "the field the AI set should be written")
	assert.Equal(t, "google_ads", got["lead_source"], "capture attribution must survive an unrelated AI edit")
	assert.Equal(t, "Q3 Brand", got["lead_source_detail"])
	assert.Equal(t, "q3-brand", got["utm_campaign"])
}

// An explicit value still wins over the stored one — merge preserves omissions, it
// does not make fields immutable.
func TestAI_ContactUpdate_OverwritesMentionedCustomField(t *testing.T) {
	contactID := uuid.New()
	orgID := uuid.New()

	repo := &fakeContactRepo{contact: &domain.Contact{
		ID:           contactID,
		OrgID:        orgID,
		CustomFields: domain.JSON(`{"lead_source":"google_ads","shirt_size":"M"}`),
	}}
	cc := &CommandCenter{
		authz:       &fakeAuthz{allow: map[string]bool{"contact:edit": true}},
		contactRepo: repo,
		logger:      zap.NewNop(),
	}
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: uuid.New(), RoleID: uuid.New()})

	out := cc.toolUpdateContact(ctx, orgID, contactID, map[string]interface{}{
		"fields": map[string]interface{}{"shirt_size": "XL"},
	})
	var res map[string]any
	require.NoError(t, json.Unmarshal(out, &res))
	require.Equal(t, true, res["success"], "update should succeed: %s", string(out))

	var got map[string]any
	require.NoError(t, json.Unmarshal(repo.saved.CustomFields, &got))
	assert.Equal(t, "XL", got["shirt_size"])
	assert.Equal(t, "google_ads", got["lead_source"])
}

// A contact with no custom fields yet must still take the write.
func TestAI_ContactUpdate_EmptyBlobTakesTheWrite(t *testing.T) {
	contactID := uuid.New()
	orgID := uuid.New()

	repo := &fakeContactRepo{contact: &domain.Contact{ID: contactID, OrgID: orgID}}
	cc := &CommandCenter{
		authz:       &fakeAuthz{allow: map[string]bool{"contact:edit": true}},
		contactRepo: repo,
		logger:      zap.NewNop(),
	}
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: uuid.New(), RoleID: uuid.New()})

	out := cc.toolUpdateContact(ctx, orgID, contactID, map[string]interface{}{
		"fields": map[string]interface{}{"shirt_size": "S"},
	})
	var res map[string]any
	require.NoError(t, json.Unmarshal(out, &res))
	require.Equal(t, true, res["success"], "update should succeed: %s", string(out))

	var got map[string]any
	require.NoError(t, json.Unmarshal(repo.saved.CustomFields, &got))
	assert.Equal(t, "S", got["shirt_size"])
}
