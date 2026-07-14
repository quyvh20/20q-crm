package usecase

import (
	"context"
	"net/http"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// api_token_scope_test.go pins the single most dangerous property of personal
// access tokens (U6.5): a token can never do more than the scopes it was granted —
// not even when its owner holds the owner role, which bypasses every capability
// check in the system.
//
// The bug this guards against is an ORDERING bug, and it is silent. Put the scope
// intersection after the owner bypass and everything still appears to work, because
// the tokens you try belong to people who would pass anyway. A leaked owner token
// would then be god-mode while its scope list read reassuringly narrow.
//
// It also guards the second half of the same rule: record routes are gated by OLS,
// not by a capability, so without an explicit records.read / records.write scope a
// token would reach every record its owner can see no matter how it was scoped.

func tokenTestUC() domain.PermissionUseCase {
	return NewPermissionUseCase(&fakePermRepo{}, &fakeRegistryUC{})
}

func withCaller(c domain.Caller) context.Context {
	return domain.WithCallerIdentity(context.Background(), c)
}

func isForbidden(err error) bool {
	appErr, ok := err.(*domain.AppError)
	return ok && appErr.Code == http.StatusForbidden
}

func TestHasCapability_TokenScopeBindsBeforeOwnerBypass(t *testing.T) {
	uc := tokenTestUC()
	orgID := uuid.New()
	userID := uuid.New()
	roleID := uuid.New()

	// An owner's SESSION passes on the owner bypass, as always.
	if err := uc.HasCapability(withCaller(domain.Caller{UserID: userID, RoleID: roleID, IsOwner: true}), orgID, domain.CapMembersManage); err != nil {
		t.Fatalf("an owner's session should pass, got %v", err)
	}

	// An owner's TOKEN, scoped only to reports, must NOT manage members — even though
	// the owner role bypasses every capability check. This is the assertion that
	// catches the ordering bug.
	ownerToken := domain.Caller{
		UserID: userID, RoleID: roleID, IsOwner: true,
		IsAPIToken: true, TokenScopes: []string{domain.CapReportsManage},
	}
	if err := uc.HasCapability(withCaller(ownerToken), orgID, domain.CapMembersManage); !isForbidden(err) {
		t.Errorf("an owner's TOKEN must be limited to its scopes, got %v — the scope intersection is running AFTER the owner bypass", err)
	}
	// …but it may do what it WAS scoped for.
	if err := uc.HasCapability(withCaller(ownerToken), orgID, domain.CapReportsManage); err != nil {
		t.Errorf("a token must be allowed the scope it holds, got %v", err)
	}

	// A scope-less token can do nothing.
	empty := domain.Caller{UserID: userID, RoleID: roleID, IsOwner: true, IsAPIToken: true}
	if err := uc.HasCapability(withCaller(empty), orgID, domain.CapReportsManage); !isForbidden(err) {
		t.Errorf("a scope-less token must be denied everything, got %v", err)
	}
}

func TestAuthorize_TokenNeedsRecordScope(t *testing.T) {
	uc := tokenTestUC()
	orgID := uuid.New()
	base := domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), IsOwner: true} // owner: OLS would pass

	// A token scoped only to analytics must not be able to read records — the record
	// routes have no capability gate, so this is the ONLY thing standing in its way.
	analytics := base
	analytics.IsAPIToken = true
	analytics.TokenScopes = []string{domain.CapAnalyticsView}
	if err := uc.Authorize(withCaller(analytics), orgID, "contact", domain.ActionRead); !isForbidden(err) {
		t.Errorf("a token without records.read must not read records, got %v", err)
	}

	reader := base
	reader.IsAPIToken = true
	reader.TokenScopes = []string{domain.ScopeRecordsRead}
	if err := uc.Authorize(withCaller(reader), orgID, "contact", domain.ActionRead); err != nil {
		t.Errorf("a token WITH records.read must read records, got %v", err)
	}
	// Reading is not writing.
	if err := uc.Authorize(withCaller(reader), orgID, "contact", domain.ActionEdit); !isForbidden(err) {
		t.Errorf("records.read must not grant writes, got %v", err)
	}

	writer := base
	writer.IsAPIToken = true
	writer.TokenScopes = []string{domain.CapRecordsWrite}
	if err := uc.Authorize(withCaller(writer), orgID, "contact", domain.ActionEdit); err != nil {
		t.Errorf("a token WITH records.write must write records, got %v", err)
	}

	// A normal session needs no token scope at all.
	if err := uc.Authorize(withCaller(base), orgID, "contact", domain.ActionEdit); err != nil {
		t.Errorf("a normal session must not need a token scope, got %v", err)
	}
}
