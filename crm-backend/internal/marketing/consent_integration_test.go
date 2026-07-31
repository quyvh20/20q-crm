package marketing_test

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/marketing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The consent grant is a raw INSERT…SELECT with an ON CONFLICT arm, so its two
// riskiest properties cannot be reached by a unit test: that it refuses to
// resurrect an opt-out, and that it survives a candidate set containing two
// case-variant spellings of one address. The latter is not hypothetical —
// contacts(org_id, email) is CASE-SENSITIVE unique while contact_marketing_state is
// unique on the NORMALIZED address, so a realistic contact list produces exactly
// that collision, and without DISTINCT ON Postgres rejects the whole statement.

func setupConsentSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
		`CREATE TABLE IF NOT EXISTS contacts (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id UUID NOT NULL,
			email TEXT,
			deleted_at TIMESTAMPTZ
		)`,
		// Mirrors the cmd/server/main.go boot guard, including the UNIQUE the grant
		// uses as its ON CONFLICT arbiter and the consent_granted_by column.
		`CREATE TABLE IF NOT EXISTS contact_marketing_state (
			id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id           UUID NOT NULL,
			email_normalized VARCHAR(320) NOT NULL,
			contact_id       UUID,
			marketing_status VARCHAR(16) NOT NULL DEFAULT 'pending',
			consent_basis    VARCHAR(40),
			consent_source   VARCHAR(64),
			consent_at       TIMESTAMPTZ,
			consent_ip       VARCHAR(64),
			region           VARCHAR(16),
			casl_expires_at  TIMESTAMPTZ,
			double_opt_in_at TIMESTAMPTZ,
			consent_granted_by UUID,
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (org_id, email_normalized)
		)`,
		`CREATE TABLE IF NOT EXISTS marketing_suppressions (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id UUID NOT NULL,
			email_normalized VARCHAR(320) NOT NULL,
			reason VARCHAR(40) NOT NULL DEFAULT 'manual',
			scope VARCHAR(20) NOT NULL DEFAULT 'all'
		)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error, s)
	}
}

func insConsentContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, email) VALUES (?, ?, ?)`, id, orgID, email).Error)
	return id
}

func contactScope(ids ...uuid.UUID) marketing.GrantScope {
	return marketing.GrantScope{ContactIDs: ids}
}

func stdGrant(userID uuid.UUID) marketing.GrantInput {
	return marketing.GrantInput{
		Basis:     marketing.BasisExistingBusinessRelationship,
		Source:    "2024 customer import",
		GrantedBy: userID,
		Now:       time.Now().UTC(),
	}
}

func TestGrantLawfulBasis_DB(t *testing.T) {
	db := newCampaignTestDB(t)
	setupConsentSchema(t, db)
	repo := marketing.NewRepository(db)
	ctx := context.Background()
	orgID, userID := uuid.New(), uuid.New()

	t.Run("stamps basis and provenance without touching status", func(t *testing.T) {
		cid := insConsentContact(t, db, orgID, "alice@example.com")

		n, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(cid), stdGrant(userID))
		require.NoError(t, err)
		require.Equal(t, int64(1), n)

		var got marketing.ContactMarketingState
		require.NoError(t, db.Raw(
			`SELECT * FROM contact_marketing_state WHERE org_id = ? AND email_normalized = ?`,
			orgID, "alice@example.com").Scan(&got).Error)

		require.NotNil(t, got.ConsentBasis)
		require.Equal(t, marketing.BasisExistingBusinessRelationship, *got.ConsentBasis)
		require.NotNil(t, got.ConsentSource)
		require.Equal(t, "2024 customer import", *got.ConsentSource)
		require.NotNil(t, got.ConsentGrantedBy)
		require.Equal(t, userID, *got.ConsentGrantedBy)
		// Status stays 'pending' on purpose: HasLawfulBasis already passes a standing
		// basis at pending, and writing 'subscribed' would survive the GDPR collapse
		// as a permanently mailable address with no recorded reason.
		require.Equal(t, marketing.StatusPending, got.MarketingStatus)

		require.True(t, got.HasLawfulBasis(time.Now()),
			"the row the grant writes must satisfy the send-time gate — otherwise the grant is a no-op")
	})

	t.Run("never resurrects an opt-out", func(t *testing.T) {
		cid := insConsentContact(t, db, orgID, "gone@example.com")
		require.NoError(t, db.Exec(
			`INSERT INTO contact_marketing_state (org_id, email_normalized, marketing_status)
			 VALUES (?, ?, 'unsubscribed')`, orgID, "gone@example.com").Error)

		n, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(cid), stdGrant(userID))
		require.NoError(t, err)
		require.Equal(t, int64(0), n, "an unsubscribed address must not be granted a basis")

		var basis *string
		require.NoError(t, db.Raw(
			`SELECT consent_basis FROM contact_marketing_state WHERE org_id = ? AND email_normalized = ?`,
			orgID, "gone@example.com").Scan(&basis).Error)
		require.Nil(t, basis, "the opt-out row must be untouched")
	})

	t.Run("case-variant addresses collapse to one row", func(t *testing.T) {
		a := insConsentContact(t, db, orgID, "Dup@Example.com")
		b := insConsentContact(t, db, orgID, "dup@example.com")

		// Without DISTINCT ON in candidateSource this fails outright with SQLSTATE
		// 21000 ("ON CONFLICT DO UPDATE command cannot affect row a second time").
		n, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(a, b), stdGrant(userID))
		require.NoError(t, err)
		require.Equal(t, int64(1), n)

		var rows int64
		require.NoError(t, db.Raw(
			`SELECT count(*) FROM contact_marketing_state WHERE org_id = ? AND email_normalized = ?`,
			orgID, "dup@example.com").Scan(&rows).Error)
		require.Equal(t, int64(1), rows)
	})

	t.Run("re-granting updates in place and is idempotent", func(t *testing.T) {
		cid := insConsentContact(t, db, orgID, "again@example.com")
		_, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(cid), stdGrant(userID))
		require.NoError(t, err)

		in := stdGrant(userID)
		in.Basis = marketing.BasisLegitimateInterest
		in.Source = "corrected basis"
		n, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(cid), in)
		require.NoError(t, err)
		require.Equal(t, int64(1), n)

		var rows int64
		require.NoError(t, db.Raw(
			`SELECT count(*) FROM contact_marketing_state WHERE org_id = ? AND email_normalized = ?`,
			orgID, "again@example.com").Scan(&rows).Error)
		require.Equal(t, int64(1), rows, "a re-grant must update, not duplicate")

		var basis string
		require.NoError(t, db.Raw(
			`SELECT consent_basis FROM contact_marketing_state WHERE org_id = ? AND email_normalized = ?`,
			orgID, "again@example.com").Scan(&basis).Error)
		require.Equal(t, marketing.BasisLegitimateInterest, basis)
	})

	t.Run("does not cross org boundaries", func(t *testing.T) {
		otherOrg := uuid.New()
		cid := insConsentContact(t, db, otherOrg, "foreign@example.com")

		n, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(cid), stdGrant(userID))
		require.NoError(t, err)
		require.Equal(t, int64(0), n, "a contact in another org must not be reachable")
	})

	t.Run("preview agrees with the grant", func(t *testing.T) {
		c1 := insConsentContact(t, db, orgID, "p1@example.com")
		c2 := insConsentContact(t, db, orgID, "p2@example.com")
		require.NoError(t, db.Exec(
			`INSERT INTO marketing_suppressions (org_id, email_normalized, reason, scope)
			 VALUES (?, ?, 'manual', 'all')`, orgID, "p2@example.com").Error)

		counts, err := repo.PreviewLawfulBasisGrant(ctx, orgID, contactScope(c1, c2))
		require.NoError(t, err)
		require.Equal(t, int64(2), counts.Candidates)
		require.Equal(t, int64(0), counts.Skipped)
		require.Equal(t, int64(1), counts.Suppressed,
			"a suppressed address is still a candidate, but the operator must see it will not be mailed")

		n, err := repo.GrantLawfulBasis(ctx, orgID, contactScope(c1, c2), stdGrant(userID))
		require.NoError(t, err)
		require.Equal(t, counts.Candidates, n, "preview and grant must not disagree")
	})
}

// The preflight numerator has to mirror HasLawfulBasis exactly. If they drift, the
// report goes green while every recipient is suppressed — the precise failure the
// preflight was built to prevent.
func TestCountMailableContacts_MatchesHasLawfulBasis_DB(t *testing.T) {
	db := newCampaignTestDB(t)
	setupConsentSchema(t, db)
	repo := marketing.NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()

	past := time.Now().Add(-24 * time.Hour)
	future := time.Now().Add(24 * time.Hour)

	rows := []struct {
		email   string
		status  string
		basis   *string
		expires *time.Time
	}{
		{"sub@x.com", marketing.StatusSubscribed, nil, nil},
		{"ebr@x.com", marketing.StatusPending, basisPtr(marketing.BasisExistingBusinessRelationship), nil},
		{"li@x.com", marketing.StatusPending, basisPtr(marketing.BasisLegitimateInterest), nil},
		{"casl-live@x.com", marketing.StatusPending, basisPtr(marketing.BasisImpliedTransaction), &future},
		{"casl-dead@x.com", marketing.StatusPending, basisPtr(marketing.BasisImpliedTransaction), &past},
		{"express@x.com", marketing.StatusPending, basisPtr(marketing.BasisExpress), nil},
		{"none@x.com", marketing.StatusPending, nil, nil},
		{"unsub@x.com", marketing.StatusUnsubscribed, basisPtr(marketing.BasisExistingBusinessRelationship), nil},
		{"cleaned@x.com", marketing.StatusCleaned, basisPtr(marketing.BasisLegitimateInterest), nil},
	}

	wantMailable := int64(0)
	for _, r := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO contact_marketing_state (org_id, email_normalized, marketing_status, consent_basis, casl_expires_at)
			 VALUES (?, ?, ?, ?, ?)`, orgID, r.email, r.status, r.basis, r.expires).Error)

		st := &marketing.ContactMarketingState{
			MarketingStatus: r.status, ConsentBasis: r.basis, CASLExpiresAt: r.expires,
		}
		if st.HasLawfulBasis(time.Now()) {
			wantMailable++
		}
	}

	got, err := repo.CountMailableContacts(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, wantMailable, got,
		"the preflight SQL and HasLawfulBasis disagree — they must be changed together")

	known, err := repo.CountKnownMarketingContacts(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, int64(len(rows)), known)

	// The grant's ON CONFLICT arbiter must exist, or every grant fails with 42P10.
	hasUniq, err := repo.HasConsentUniqueConstraint(ctx)
	require.NoError(t, err)
	require.True(t, hasUniq)
}

func basisPtr(s string) *string { return &s }
