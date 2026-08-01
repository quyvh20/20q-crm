package automation

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
)

// The org's inbound-webhook secret authenticates every inbound automation call —
// whoever holds it can forge a request that creates contacts and fires workflows as
// the org. It was stored in plaintext, so a database dump handed it over.

func testCodec(t *testing.T) *envelope.Codec {
	t.Helper()
	// 32 zero bytes, base64. Fine for a test: what is under test is the plumbing,
	// not the key material.
	ring, err := envelope.ParseKeyring("1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}
	return envelope.NewCodec(ring)
}

func handlerWithCodec(c *envelope.Codec) *Handler { return &Handler{codec: c} }

func TestWebhookSecret_RoundTrips(t *testing.T) {
	h := handlerWithCodec(testCodec(t))
	orgID := uuid.New()
	secret := GenerateToken(64)

	stored, err := h.sealWebhookSecret(orgID, secret)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if stored == secret {
		t.Fatal("the stored value is the plaintext — nothing was sealed")
	}
	if !envelope.IsSealed(stored) {
		t.Fatalf("stored value is not recognisable as a blob: %q", stored)
	}
	if strings.Contains(stored, secret) {
		t.Fatal("the plaintext appears inside the stored value")
	}

	got, err := h.openWebhookSecret(orgID, stored)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != secret {
		t.Fatalf("round-trip changed the secret")
	}
}

// The number that dictated the schema change. A sealed 64-char secret does not fit
// the column the code used to declare, so getting this wrong breaks every write.
func TestWebhookSecret_SealedValueExceedsTheOldColumnWidth(t *testing.T) {
	h := handlerWithCodec(testCodec(t))
	stored, err := h.sealWebhookSecret(uuid.New(), GenerateToken(64))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if len(stored) <= 128 {
		t.Fatalf("sealed length %d now fits varchar(128) — re-check whether type:text is still required", len(stored))
	}
	t.Logf("sealed length is %d characters (old column was varchar(128))", len(stored))
}

// Rows written before sealing shipped hold plaintext and must keep working, or
// every existing org's inbound integrations break on deploy.
func TestWebhookSecret_LegacyPlaintextStillOpens(t *testing.T) {
	h := handlerWithCodec(testCodec(t))
	legacy := GenerateToken(64)

	got, err := h.openWebhookSecret(uuid.New(), legacy)
	if err != nil {
		t.Fatalf("a legacy plaintext secret must still open: %v", err)
	}
	if got != legacy {
		t.Fatal("legacy plaintext was altered")
	}
}

// A deployment without INTEGRATION_ENC_KEY keeps working exactly as before rather
// than losing the feature.
func TestWebhookSecret_NilCodecStaysPlaintext(t *testing.T) {
	h := handlerWithCodec(nil)
	secret := GenerateToken(64)

	stored, err := h.sealWebhookSecret(uuid.New(), secret)
	if err != nil {
		t.Fatalf("seal with no codec must not fail: %v", err)
	}
	if stored != secret {
		t.Fatal("with no codec the value should be stored unchanged")
	}

	got, err := h.openWebhookSecret(uuid.New(), stored)
	if err != nil || got != secret {
		t.Fatalf("open with no codec: got %q, err %v", got, err)
	}
}

// The inverse, and the reason it fails rather than guessing: computing an HMAC over
// ciphertext would reject every legitimate inbound call with a signature error
// pointing nowhere.
func TestWebhookSecret_SealedValueWithNoCodecFailsClosed(t *testing.T) {
	sealedElsewhere, err := handlerWithCodec(testCodec(t)).sealWebhookSecret(uuid.New(), "s3cret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	got, err := handlerWithCodec(nil).openWebhookSecret(uuid.New(), sealedElsewhere)
	if err == nil {
		t.Fatalf("a sealed value with no key must fail, got %q", got)
	}
}

// The binding is what stops a blob being lifted between rows. Since the table is
// keyed by org, that means one org cannot replay another's stored secret.
func TestWebhookSecret_DoesNotOpenForAnotherOrg(t *testing.T) {
	h := handlerWithCodec(testCodec(t))
	orgA, orgB := uuid.New(), uuid.New()

	stored, err := h.sealWebhookSecret(orgA, GenerateToken(64))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got, err := h.openWebhookSecret(orgB, stored); err == nil {
		t.Fatalf("org B opened org A's secret: %q", got)
	}
}

// Purposes are namespaces. A blob sealed for one column must not open as another,
// or the isolation between them is decorative.
func TestWebhookSecret_DoesNotOpenUnderAnotherPurpose(t *testing.T) {
	codec := testCodec(t)
	orgID := uuid.New()

	stored, err := codec.SealString(envelope.Binding{
		OrgID: orgID, Purpose: envelope.PurposeConnectionWebhookSecret, ID: orgID,
	}, "s3cret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	if got, err := handlerWithCodec(codec).openWebhookSecret(orgID, stored); err == nil {
		t.Fatalf("a connection-webhook blob opened as a workflow secret: %q", got)
	}
}

// The migration risk, tested directly.
//
// A fresh schema is created from the struct tag and is already text, so no normal
// test exercises the case that actually matters: a PRE-EXISTING varchar(128)
// column, which is what production has. If the widen does not run there, the first
// sealed write fails with a value-too-long error on every org.
func TestWebhookSecret_ColumnWidenAcceptsASealedValue_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Recreate production's shape: the column as it was BEFORE this change.
	if err := db.Exec(`DROP TABLE IF EXISTS automation_workflow_org_tokens`).Error; err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := db.Exec(`CREATE TABLE automation_workflow_org_tokens (
		org_id     UUID PRIMARY KEY,
		token      VARCHAR(64) NOT NULL,
		secret     VARCHAR(128) NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	orgID := uuid.New()
	legacy := GenerateToken(64)
	if err := db.Exec(`INSERT INTO automation_workflow_org_tokens (org_id, token, secret) VALUES (?, ?, ?)`,
		orgID, GenerateToken(32), legacy).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// Prove the premise before proving the fix: the sealed value does NOT fit.
	h := handlerWithCodec(testCodec(t))
	sealed, err := h.sealWebhookSecret(orgID, legacy)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := db.Exec(`UPDATE automation_workflow_org_tokens SET secret = ? WHERE org_id = ?`,
		sealed, orgID).Error; err == nil {
		t.Fatal("a 212-character value fit varchar(128) — the fixture is not reproducing production")
	}

	// The widen main.go runs at boot.
	if err := db.Exec(`ALTER TABLE automation_workflow_org_tokens ALTER COLUMN secret TYPE text`).Error; err != nil {
		t.Fatalf("widen: %v", err)
	}

	// And now the backfill converts the legacy row in place.
	n, err := BackfillWebhookSecrets(context.Background(), db, testCodec(t))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Fatalf("backfill sealed %d rows, want 1", n)
	}

	var stored string
	if err := db.Raw(`SELECT secret FROM automation_workflow_org_tokens WHERE org_id = ?`, orgID).
		Scan(&stored).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !envelope.IsSealed(stored) {
		t.Fatalf("row was not sealed: %q", stored)
	}
	got, err := h.openWebhookSecret(orgID, stored)
	if err != nil || got != legacy {
		t.Fatalf("sealed row does not open to the original secret: got %q err %v", got, err)
	}

	// Idempotent: a second boot must not re-seal an already-sealed row.
	again, err := BackfillWebhookSecrets(context.Background(), db, testCodec(t))
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if again != 0 {
		t.Fatalf("backfill re-sealed %d already-sealed rows", again)
	}
}
