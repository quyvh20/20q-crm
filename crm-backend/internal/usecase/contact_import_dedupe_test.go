package usecase

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CSV import looks a contact up by email twice — once to overwrite it, once to
// attach its tags — and both lookups used to go through ContactFilter.Q. Q is a
// FUZZY full-text search: it matches the related company and the phone digits, and
// it treats the last word of the term as a prefix. So "bob@example.com" also
// matches "bob@example.com.au", and because the list is ordered created_at DESC a
// newer near-miss is returned FIRST — with Limit 1 it is the only row returned.
//
// That produced two silent failures, both modelled below:
//   - overwrite mode: the EqualFold check rejects the near-miss, so import falls
//     through to the insert path — where ON CONFLICT DO NOTHING drops the row
//     because the address already exists. THE EDIT IS SILENTLY LOST. (It does not
//     produce a visible duplicate, which is precisely why nobody noticed.)
//   - the tag pass: no exactness check at all, so ReplaceContactTags WIPES AN
//     UNRELATED CONTACT'S TAGS.
//
// The fix is ContactFilter.Email (exact, case-insensitive, row-scoped).
//
// The fake below models Q as an email-prefix match, newest first. That model is
// not taken on faith: TestContactSearch_ExactEmailFilterIsNotFuzzy in
// internal/repository asserts the SAME scenario (bob@example.com vs
// bob@example.com.au, imposter returned first) against a live Postgres. If the two
// ever disagree, that test is the one telling the truth.
type importFakeRepo struct {
	// Embedded so the fake satisfies the 19-method interface while modelling only
	// what BulkImport touches. It is nil: any unmodelled call panics loudly rather
	// than quietly returning a zero value and weakening the test.
	domain.ContactRepository

	contacts      []domain.Contact // oldest first; List reverses for created_at DESC
	tagsByContact map[uuid.UUID][]uuid.UUID
}

func (f *importFakeRepo) List(_ context.Context, _ uuid.UUID, flt domain.ContactFilter) ([]domain.Contact, string, error) {
	var out []domain.Contact
	switch {
	case flt.Email != "":
		// Exact and case-insensitive, mirroring LOWER(email) = LOWER(?).
		for _, c := range f.contacts {
			if c.Email != nil && strings.EqualFold(*c.Email, flt.Email) {
				out = append(out, c)
			}
		}
	case flt.Q != "":
		// The fuzzy path, newest first — the behaviour that caused the bug.
		for i := len(f.contacts) - 1; i >= 0; i-- {
			c := f.contacts[i]
			if c.Email != nil && strings.HasPrefix(strings.ToLower(*c.Email), strings.ToLower(flt.Q)) {
				out = append(out, c)
			}
		}
	}
	if flt.Limit > 0 && len(out) > flt.Limit {
		out = out[:flt.Limit]
	}
	return out, "", nil
}

func (f *importFakeRepo) Update(_ context.Context, c *domain.Contact) error {
	for i := range f.contacts {
		if f.contacts[i].ID == c.ID {
			f.contacts[i] = *c
			return nil
		}
	}
	return nil
}

// BulkCreate models the real INSERT ... ON CONFLICT DO NOTHING against the partial
// unique index on (org_id, email). Modelling the conflict is what makes these tests
// honest: a row whose address already exists is DROPPED, so the failure mode of the
// old fuzzy lookup was never "a duplicate appears" — it was "the write silently
// vanishes". A fake that appended unconditionally would assert the wrong symptom.
func (f *importFakeRepo) BulkCreate(_ context.Context, contacts []domain.Contact) (int64, error) {
	var inserted int64
	for _, c := range contacts {
		if c.Email != nil && f.hasEmail(*c.Email) {
			continue // ON CONFLICT DO NOTHING
		}
		c.ID = uuid.New()
		f.contacts = append(f.contacts, c)
		inserted++
	}
	return inserted, nil
}

// The unique index is on the raw address, so it is case-SENSITIVE — case-variant
// twins are legal rows. Mirrored here rather than folding case.
func (f *importFakeRepo) hasEmail(email string) bool {
	for _, c := range f.contacts {
		if c.Email != nil && *c.Email == email {
			return true
		}
	}
	return false
}

func (f *importFakeRepo) ReplaceContactTags(_ context.Context, contactID uuid.UUID, tagIDs []uuid.UUID) error {
	if f.tagsByContact == nil {
		f.tagsByContact = map[uuid.UUID][]uuid.UUID{}
	}
	f.tagsByContact[contactID] = tagIDs
	return nil
}

func (f *importFakeRepo) FindTagsByNames(_ context.Context, _ uuid.UUID, _ []string) ([]domain.Tag, error) {
	return nil, nil // nothing pre-existing, so resolveTagIDs creates them
}

func (f *importFakeRepo) CreateTags(_ context.Context, tags []domain.Tag) error {
	// Must fill IDs in place: resolveTagIDs reads t.ID off this same backing array.
	for i := range tags {
		tags[i].ID = uuid.New()
	}
	return nil
}

// csvFile adapts a byte slice to multipart.File (Read/ReadAt/Seek/Close).
type csvFile struct{ *bytes.Reader }

func (csvFile) Close() error { return nil }

func newCSV(body string) csvFile { return csvFile{bytes.NewReader([]byte(body))} }

func strptr(s string) *string { return &s }

// seedTwins returns a repo holding the real contact and a NEWER near-miss whose
// address merely starts with the same text — the row a fuzzy lookup returns first.
func seedTwins(t *testing.T) (*importFakeRepo, uuid.UUID, uuid.UUID) {
	t.Helper()
	realID, imposterID := uuid.New(), uuid.New()
	return &importFakeRepo{
		contacts: []domain.Contact{
			{ID: realID, FirstName: "Bob", LastName: "Smith", Email: strptr("bob@example.com")},
			{ID: imposterID, FirstName: "Imposter", LastName: "Twin", Email: strptr("bob@example.com.au")},
		},
	}, realID, imposterID
}

// Overwrite mode must update the contact whose address matches EXACTLY. Under the
// old fuzzy lookup the near-miss came back first, the EqualFold guard rejected it,
// and the edit was then swallowed by ON CONFLICT DO NOTHING — the import reported
// success and changed nothing.
func TestBulkImport_OverwriteTargetsTheExactEmail(t *testing.T) {
	repo, realID, imposterID := seedTwins(t)
	uc := NewContactUseCase(repo, nil)

	csv := "first_name,last_name,email\nRobert,Smithers,bob@example.com\n"
	res, err := uc.BulkImport(context.Background(), uuid.New(), newCSV(csv), "contacts.csv", "overwrite")
	require.NoError(t, err)

	require.Len(t, repo.contacts, 2, "the address already exists, so no row may be added")

	var real, imposter domain.Contact
	for _, c := range repo.contacts {
		switch c.ID {
		case realID:
			real = c
		case imposterID:
			imposter = c
		}
	}

	assert.Equal(t, "Robert", real.FirstName, "the exactly-matching contact must be the one overwritten")
	assert.Equal(t, "Smithers", real.LastName)
	// The near-miss must be untouched.
	assert.Equal(t, "Imposter", imposter.FirstName, "the near-miss contact must not be modified")
	assert.Equal(t, "bob@example.com.au", *imposter.Email)
	assert.Equal(t, 1, res.Created)
}

// The tag pass is the dangerous one: it replaces the matched contact's tags with no
// exactness check afterwards, so targeting the wrong row destroys data rather than
// merely duplicating it.
func TestBulkImport_TagsLandOnTheExactEmail(t *testing.T) {
	repo, realID, imposterID := seedTwins(t)
	// The near-miss already has tags. Losing these is the data loss.
	existingTag := uuid.New()
	repo.tagsByContact = map[uuid.UUID][]uuid.UUID{imposterID: {existingTag}}
	uc := NewContactUseCase(repo, nil)

	csv := "first_name,last_name,email,tags\nRobert,Smithers,bob@example.com,vip\n"
	_, err := uc.BulkImport(context.Background(), uuid.New(), newCSV(csv), "contacts.csv", "overwrite")
	require.NoError(t, err)

	assert.Len(t, repo.tagsByContact[realID], 1, "the exactly-matching contact must receive the tag")
	assert.Equal(t, []uuid.UUID{existingTag}, repo.tagsByContact[imposterID],
		"the near-miss contact's existing tags must be left alone")
}

// The insert path, for completeness of coverage rather than as a regression guard:
// this one passes on the OLD fuzzy code too. A freshly imported contact is the
// NEWEST row, so created_at DESC happened to return it ahead of any near-miss and
// the fuzzy lookup got the right answer by luck. Kept because the property still
// has to hold — a future change to ordering or Limit could break it — but it is
// the two tests above, not this one, that demonstrate the bug.
func TestBulkImport_TagsOnNewContactSkipTheNearMiss(t *testing.T) {
	imposterID := uuid.New()
	repo := &importFakeRepo{
		contacts: []domain.Contact{
			{ID: imposterID, FirstName: "Imposter", LastName: "Twin", Email: strptr("carol@example.com.au")},
		},
		tagsByContact: map[uuid.UUID][]uuid.UUID{},
	}
	uc := NewContactUseCase(repo, nil)

	csv := "first_name,last_name,email,tags\nCarol,Jones,carol@example.com,vip\n"
	_, err := uc.BulkImport(context.Background(), uuid.New(), newCSV(csv), "contacts.csv", "skip")
	require.NoError(t, err)

	require.Len(t, repo.contacts, 2)
	newContact := repo.contacts[len(repo.contacts)-1]
	require.Equal(t, "carol@example.com", *newContact.Email)

	assert.Len(t, repo.tagsByContact[newContact.ID], 1, "the newly imported contact must get its tag")
	assert.Empty(t, repo.tagsByContact[imposterID], "the pre-existing near-miss must not be tagged")
}

// The clearest data loss, and the reason this is a bug rather than an annoyance.
// In the DEFAULT (non-overwrite) mode a row whose address already exists is dropped
// by ON CONFLICT, so no new contact is created — leaving the pre-existing exact
// contact OLDER than the near-miss. The tag pass then runs for that row anyway, and
// under the old fuzzy lookup it resolved to the near-miss and replaced ITS tags.
// Tags that were never part of the import are destroyed.
func TestBulkImport_TagPassDoesNotWipeANearMissTags(t *testing.T) {
	repo, realID, imposterID := seedTwins(t)
	keep := uuid.New()
	repo.tagsByContact = map[uuid.UUID][]uuid.UUID{imposterID: {keep}}
	uc := NewContactUseCase(repo, nil)

	csv := "first_name,last_name,email,tags\nBob,Smith,bob@example.com,newsletter\n"
	_, err := uc.BulkImport(context.Background(), uuid.New(), newCSV(csv), "contacts.csv", "skip")
	require.NoError(t, err)

	assert.Equal(t, []uuid.UUID{keep}, repo.tagsByContact[imposterID],
		"the near-miss contact's tags must survive an import that never mentioned it")
	assert.Len(t, repo.tagsByContact[realID], 1,
		"the tag belongs to the contact whose address actually matched")
}

// Case differences must still resolve to the same contact — the lookup is
// LOWER(email) = LOWER(?), not a raw equality.
func TestBulkImport_OverwriteIsCaseInsensitive(t *testing.T) {
	repo, realID, _ := seedTwins(t)
	uc := NewContactUseCase(repo, nil)

	csv := "first_name,last_name,email\nRobert,Smithers,BOB@Example.COM\n"
	_, err := uc.BulkImport(context.Background(), uuid.New(), newCSV(csv), "contacts.csv", "overwrite")
	require.NoError(t, err)

	require.Len(t, repo.contacts, 2, "a case variant is the same contact, not a new one")
	for _, c := range repo.contacts {
		if c.ID == realID {
			assert.Equal(t, "Robert", c.FirstName)
		}
	}
}
