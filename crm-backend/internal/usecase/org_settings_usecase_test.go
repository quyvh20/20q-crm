package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// newSystemRegistry seeds a fake registry repo with the three system objects, each
// carrying one native field — the post-P7 substrate OrgSettingsUseCase now manages
// custom fields on (object_fields), instead of the org_settings blob.
func newSystemRegistry() *fakeRegistryRepo {
	repo := &fakeRegistryRepo{fields: map[uuid.UUID][]domain.ObjectField{}}
	natives := map[string]string{"contact": "first_name", "company": "name", "deal": "title"}
	for _, slug := range []string{"contact", "company", "deal"} {
		defID := uuid.New()
		repo.defs = append(repo.defs, domain.ObjectDef{ID: defID, Slug: slug, Label: slug, IsSystem: true, Storage: "table"})
		col := natives[slug]
		repo.fields[defID] = []domain.ObjectField{{
			ID: uuid.New(), ObjectDefID: defID, Key: natives[slug], Label: natives[slug],
			Type: "text", Options: domain.JSON("[]"), IsSystem: true, StorageKind: "column", MapsToColumn: &col, Position: 0,
		}}
	}
	return repo
}

func TestOrgSettings_CreateGetField_BacksOntoObjectFields(t *testing.T) {
	repo := newSystemRegistry()
	uc := NewOrgSettingsUseCase(repo)
	org := uuid.New()
	ctx := context.Background()

	created, err := uc.CreateFieldDef(ctx, org, domain.CreateFieldDefInput{
		Key: "shoe_size", Label: "Shoe Size", Type: "number", EntityType: "contact",
	})
	if err != nil {
		t.Fatalf("CreateFieldDef: %v", err)
	}
	if created.Key != "shoe_size" || created.EntityType != "contact" {
		t.Fatalf("unexpected created def: %+v", created)
	}

	defs, err := uc.GetFieldDefs(ctx, org, "contact")
	if err != nil {
		t.Fatalf("GetFieldDefs: %v", err)
	}
	if len(defs) != 1 || defs[0].Key != "shoe_size" {
		t.Fatalf("expected one custom field shoe_size, got %+v", defs)
	}
	// Native fields are not returned as "custom" defs.
	for _, d := range defs {
		if d.Key == "first_name" {
			t.Fatalf("native field leaked into custom defs: %+v", defs)
		}
	}
}

func TestOrgSettings_DuplicateKeyRejected(t *testing.T) {
	repo := newSystemRegistry()
	uc := NewOrgSettingsUseCase(repo)
	org := uuid.New()
	ctx := context.Background()

	if _, err := uc.CreateFieldDef(ctx, org, domain.CreateFieldDefInput{Key: "tier", Label: "Tier", Type: "text", EntityType: "contact"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := uc.CreateFieldDef(ctx, org, domain.CreateFieldDefInput{Key: "tier", Label: "Tier 2", Type: "text", EntityType: "contact"})
	assertAppCode(t, err, 409)

	// Collision with a native column key is also rejected.
	_, err = uc.CreateFieldDef(ctx, org, domain.CreateFieldDefInput{Key: "first_name", Label: "First", Type: "text", EntityType: "contact"})
	assertAppCode(t, err, 409)
}

func TestOrgSettings_CreateValidation(t *testing.T) {
	repo := newSystemRegistry()
	uc := NewOrgSettingsUseCase(repo)
	org := uuid.New()
	ctx := context.Background()

	cases := []domain.CreateFieldDefInput{
		{Key: "x", Label: "X", Type: "bogus", EntityType: "contact"},   // bad type
		{Key: "x", Label: "X", Type: "text", EntityType: "alien"},      // bad entity
		{Key: "Bad Key", Label: "X", Type: "text", EntityType: "deal"}, // bad key format
		{Key: "x", Label: "X", Type: "select", EntityType: "deal"},     // select w/o options
	}
	for i, in := range cases {
		if _, err := uc.CreateFieldDef(ctx, org, in); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestOrgSettings_UpdateAndDelete(t *testing.T) {
	repo := newSystemRegistry()
	uc := NewOrgSettingsUseCase(repo)
	org := uuid.New()
	ctx := context.Background()

	if _, err := uc.CreateFieldDef(ctx, org, domain.CreateFieldDefInput{Key: "stage_note", Label: "Note", Type: "text", EntityType: "deal"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	newLabel := "Stage Note"
	updated, err := uc.UpdateFieldDef(ctx, org, "stage_note", domain.UpdateFieldDefInput{Label: &newLabel})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Label != "Stage Note" || updated.EntityType != "deal" {
		t.Fatalf("unexpected updated def: %+v", updated)
	}

	// Updating an unknown key is a 404.
	_, err = uc.UpdateFieldDef(ctx, org, "ghost", domain.UpdateFieldDefInput{Label: &newLabel})
	assertAppCode(t, err, 404)

	if err := uc.DeleteFieldDef(ctx, org, "stage_note"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	defs, _ := uc.GetFieldDefs(ctx, org, "deal")
	if len(defs) != 0 {
		t.Fatalf("expected no custom defs after delete, got %+v", defs)
	}

	// Deleting an unknown key is a 404.
	assertAppCode(t, uc.DeleteFieldDef(ctx, org, "ghost"), 404)
}

func TestOrgSettings_ValidateCustomFields(t *testing.T) {
	repo := newSystemRegistry()
	uc := NewOrgSettingsUseCase(repo)
	org := uuid.New()
	ctx := context.Background()

	if _, err := uc.CreateFieldDef(ctx, org, domain.CreateFieldDefInput{Key: "score", Label: "Score", Type: "number", EntityType: "contact"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Wrong type is rejected.
	if err := uc.ValidateCustomFields(ctx, org, "contact", domain.JSON(`{"score":"not-a-number"}`)); err == nil {
		t.Error("expected validation error for non-numeric score")
	}
	// Correct type passes.
	if err := uc.ValidateCustomFields(ctx, org, "contact", domain.JSON(`{"score":42}`)); err != nil {
		t.Errorf("expected valid score to pass, got %v", err)
	}
	// Empty payload is a no-op.
	if err := uc.ValidateCustomFields(ctx, org, "contact", domain.JSON(`{}`)); err != nil {
		t.Errorf("empty payload should pass, got %v", err)
	}
}

func assertAppCode(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected *domain.AppError with code %d, got nil", code)
	}
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected *domain.AppError, got %T (%v)", err, err)
	}
	if appErr.Code != code {
		t.Fatalf("expected code %d, got %d (%s)", code, appErr.Code, appErr.Message)
	}
}
