package automation

import (
	"context"
	"errors"
	"fmt"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// webhook_secret.go is the only place that converts between the STORED form of an
// org's inbound-webhook signing secret and its usable form.
//
// The secret authenticates every inbound automation call: whoever holds it can
// forge a request that creates contacts and fires workflows as the org. It was
// stored in plaintext, so a database dump — or any read of that column — handed it
// over directly. It has to stay recoverable (the UI reveals it once so an operator
// can paste it into the sending system), which rules out hashing; envelope
// encryption is the recoverable-but-not-plaintext option, and it is what the
// integrations module already uses for provider credentials.

// webhookSecretBinding welds a sealed secret to its org, purpose and row.
//
// The row id IS the org id here — automation_workflow_org_tokens is keyed by
// org_id, one row per org. Binding requires all three fields non-zero, which that
// satisfies, and the practical effect is unchanged: a blob lifted into another
// org's row fails its authentication tag rather than opening.
func webhookSecretBinding(orgID uuid.UUID) envelope.Binding {
	return envelope.Binding{
		OrgID:   orgID,
		Purpose: envelope.PurposeWorkflowOrgWebhookSecret,
		ID:      orgID,
	}
}

// sealWebhookSecret converts a freshly generated secret into the value to store.
//
// With no codec configured it returns the plaintext unchanged. That is deliberate:
// a deployment may legitimately run without INTEGRATION_ENC_KEY, and refusing to
// create a webhook token there would break the feature outright rather than
// degrade it. Production sets the key — it already must, for integrations.
func (h *Handler) sealWebhookSecret(orgID uuid.UUID, plaintext string) (string, error) {
	if !h.codec.Configured() {
		return plaintext, nil
	}
	sealed, err := h.codec.SealString(webhookSecretBinding(orgID), plaintext)
	if err != nil {
		return "", fmt.Errorf("seal webhook secret: %w", err)
	}
	return sealed, nil
}

// openWebhookSecret converts a stored value into the usable secret.
//
// Handles both shapes on purpose. Rows written before sealing shipped hold
// plaintext and must keep authenticating inbound calls, or every existing org's
// integrations break on deploy. IsSealed discriminates; an actual open still
// authenticates, so a value that merely looks sealed fails there.
func (h *Handler) openWebhookSecret(orgID uuid.UUID, stored string) (string, error) {
	if !envelope.IsSealed(stored) {
		// Legacy plaintext. The boot-time backfill converts these; until it has run
		// for a given row, it still has to work.
		return stored, nil
	}
	if !h.codec.Configured() {
		// A sealed value with no key is unrecoverable, and guessing is worse than
		// failing: silently treating the blob as the secret would compute an HMAC
		// over ciphertext and reject every legitimate inbound call with a signature
		// error that points nowhere.
		return "", errors.New("webhook secret is sealed but INTEGRATION_ENC_KEY is not configured")
	}
	plaintext, err := h.codec.OpenString(webhookSecretBinding(orgID), stored)
	if err != nil {
		return "", fmt.Errorf("open webhook secret: %w", err)
	}
	return plaintext, nil
}

// BackfillWebhookSecrets seals any org webhook secret still stored in plaintext.
//
// Runs once at boot and is idempotent — a row already sealed is skipped by the
// same discriminator the read path uses. Bounded by the number of ORGS, one row
// each, so it stays a small query however large the deployment gets.
//
// Without it the lazy read alone would leave plaintext rows in the table
// indefinitely, since nothing rewrites a secret that is never rotated: the column
// would be "encrypted" while most of its rows were not.
func BackfillWebhookSecrets(ctx context.Context, db *gorm.DB, codec *envelope.Codec) (int, error) {
	if db == nil || !codec.Configured() {
		return 0, nil
	}

	var rows []WorkflowOrgToken
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("load org webhook secrets: %w", err)
	}

	sealed := 0
	for i := range rows {
		r := rows[i]
		if r.Secret == "" || envelope.IsSealed(r.Secret) {
			continue
		}
		blob, err := codec.SealString(webhookSecretBinding(r.OrgID), r.Secret)
		if err != nil {
			return sealed, fmt.Errorf("seal webhook secret for org %s: %w", r.OrgID, err)
		}
		if err := db.WithContext(ctx).Model(&WorkflowOrgToken{}).
			Where("org_id = ?", r.OrgID).
			Update("secret", blob).Error; err != nil {
			return sealed, fmt.Errorf("store sealed webhook secret for org %s: %w", r.OrgID, err)
		}
		sealed++
	}
	return sealed, nil
}
