package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// P5 identity re-key — session-cache v2 codec
// ============================================================

func TestSessionCacheValue_RoundTrip(t *testing.T) {
	rid := uuid.New()
	val := EncodeSessionCacheValue("active", 7, domain.DataScopeOwn, rid, true, "Support: Tier 2")

	status, tv, ds, gotRID, isOwner, roleName, ok := ParseSessionCacheValue(val)
	if !ok {
		t.Fatalf("round-trip failed to parse: %q", val)
	}
	if status != "active" || tv != 7 || ds != domain.DataScopeOwn || gotRID != rid || !isOwner {
		t.Fatalf("round-trip mismatch: %s %d %s %s %v", status, tv, ds, gotRID, isOwner)
	}
	// The role name goes LAST in the encoding precisely so a tenant-chosen name
	// containing colons survives intact.
	if roleName != "Support: Tier 2" {
		t.Fatalf("role name with colons must survive: got %q", roleName)
	}
}

func TestSessionCacheValue_MalformedRejected(t *testing.T) {
	cases := []string{
		"",
		"active:owner:3:all",                    // v1 format — must NOT parse as v2
		"active:x:all:" + uuid.NewString() + ":true:owner", // bad token version
		"active:3:all:not-a-uuid:true:owner",    // bad rid
		"active:3:all:" + uuid.NewString() + ":maybe:owner", // bad bool
	}
	for _, val := range cases {
		if _, _, _, _, _, _, ok := ParseSessionCacheValue(val); ok {
			t.Errorf("malformed value %q must be rejected (treated as a cache miss)", val)
		}
	}
}

func TestSessionCacheKey_IsV2(t *testing.T) {
	// The key is bumped so v2 code can never read a v1 value: the old parser would
	// have misread the appended fields as dataScope and widened own-scope users.
	key := SessionCacheKey(uuid.Nil, uuid.Nil)
	if want := "session:v2:" + uuid.Nil.String() + ":org:" + uuid.Nil.String(); key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

// ============================================================
// P5 identity re-key — grants keyed by role id
// ============================================================

// TestAuthorize_SurvivesRoleRename is the R1 keystone: authorization keys off
// the role's ID, so a rename (same id, new name) changes nothing about what the
// role can do — even when the caller's cached name is stale.
func TestAuthorize_SurvivesRoleRename(t *testing.T) {
	rid := uuid.New()
	repo := &fakePermRepo{access: map[uuid.UUID]map[string]domain.ObjectAccess{
		rid: {"deal": {Read: true}},
	}}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	// The caller's NAME is stale ("Old Name" was renamed server-side); the id is
	// what matters.
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{
		Role: "Old Name", UserID: uuid.New(), RoleID: rid,
	})
	if err := uc.Authorize(ctx, uuid.New(), "deal", domain.ActionRead); err != nil {
		t.Fatalf("grants must follow the role id across a rename, got %v", err)
	}
}

// TestAuthorize_NoRoleID_DefaultDenies: since the P9 bridge removal a caller
// carrying only a name (no rid) resolves to no grants and is default-denied —
// there is no name→id fallback anymore.
func TestAuthorize_NoRoleID_DefaultDenies(t *testing.T) {
	rid := uuid.New()
	repo := &fakePermRepo{
		access: map[uuid.UUID]map[string]domain.ObjectAccess{
			rid: {"deal": {Read: true}},
		},
		roles: []domain.Role{{ID: rid, Name: "Support Agent"}},
	}
	uc := NewPermissionUseCase(repo, &fakeRegistryUC{})

	// A caller whose name matches a granted role but who carries NO rid is denied:
	// names grant nothing after P9.
	nameOnly := domain.WithCallerIdentity(context.Background(), domain.Caller{Role: "Support Agent", UserID: uuid.New()})
	err := uc.Authorize(nameOnly, uuid.New(), "deal", domain.ActionRead)
	assertForbidden(t, err, "a name-only caller no longer resolves via any bridge")
}

// TestOwnerBypass_FlagNotName: the IsOwner flag is the sole owner check. A caller
// whose role is merely NAMED owner but carries a non-owner identity (IsOwner
// false) must NOT bypass — names grant nothing (the P5 name bridge is gone).
func TestOwnerBypass_FlagNotName(t *testing.T) {
	uc := NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
	org := uuid.New()

	// 1. Flag path (what the middleware sets after resolving roles.is_owner).
	flagCtx := domain.WithCallerIdentity(context.Background(), domain.Caller{
		Role: "owner", UserID: uuid.New(), RoleID: uuid.New(), IsOwner: true,
	})
	if err := uc.Authorize(flagCtx, org, "deal", domain.ActionDelete); err != nil {
		t.Fatalf("IsOwner flag must bypass OLS, got %v", err)
	}

	// 2. A resolved identity that is NOT the owner does not bypass, whatever the
	// name says — this is what makes the owner check rename/shadow-proof.
	imposterCtx := domain.WithCallerIdentity(context.Background(), domain.Caller{
		Role: domain.RoleOwner, UserID: uuid.New(), RoleID: uuid.New(), IsOwner: false,
	})
	err := uc.Authorize(imposterCtx, org, "deal", domain.ActionDelete)
	assertForbidden(t, err, "a non-owner identity merely NAMED 'owner'")

	// 3. A name-only 'owner' caller (no rid, no flag) does NOT bypass — the P9
	// bridge removal means the name alone grants nothing.
	nameOnlyOwner := domain.WithCallerIdentity(context.Background(), domain.Caller{Role: domain.RoleOwner, UserID: uuid.New()})
	err = uc.Authorize(nameOnlyOwner, org, "deal", domain.ActionDelete)
	assertForbidden(t, err, "a name-only 'owner' no longer bypasses via any bridge")
}
