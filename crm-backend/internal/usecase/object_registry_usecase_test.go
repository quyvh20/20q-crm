package usecase

import (
	"context"
	"encoding/json"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes (the usecase depends only on interfaces, so no DB is needed)
// ============================================================

type fakeRegistryRepo struct {
	defs        []domain.ObjectDef
	fields      map[uuid.UUID][]domain.ObjectField // by object_def_id
	ensureCalls int
}

func (f *fakeRegistryRepo) EnsureSystemObjects(_ context.Context, _ uuid.UUID) error {
	f.ensureCalls++
	return nil
}

func (f *fakeRegistryRepo) ListDefs(_ context.Context, _ uuid.UUID) ([]domain.ObjectDef, error) {
	return f.defs, nil
}

func (f *fakeRegistryRepo) GetDefBySlug(_ context.Context, _ uuid.UUID, slug string) (*domain.ObjectDef, error) {
	for i := range f.defs {
		if f.defs[i].Slug == slug {
			return &f.defs[i], nil
		}
	}
	return nil, nil
}

func (f *fakeRegistryRepo) ListFields(_ context.Context, defID uuid.UUID) ([]domain.ObjectField, error) {
	return f.fields[defID], nil
}

func (f *fakeRegistryRepo) FieldCounts(_ context.Context, _ uuid.UUID) (map[uuid.UUID]int, error) {
	counts := make(map[uuid.UUID]int)
	for id, fs := range f.fields {
		counts[id] = len(fs)
	}
	return counts, nil
}

type fakeCustomRepo struct {
	defs []domain.CustomObjectDef
}

func (f *fakeCustomRepo) ListDefs(_ context.Context, _ uuid.UUID) ([]domain.CustomObjectDef, error) {
	return f.defs, nil
}
func (f *fakeCustomRepo) GetDefBySlug(_ context.Context, _ uuid.UUID, slug string) (*domain.CustomObjectDef, error) {
	for i := range f.defs {
		if f.defs[i].Slug == slug {
			return &f.defs[i], nil
		}
	}
	return nil, nil
}
func (f *fakeCustomRepo) GetDefByID(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.CustomObjectDef, error) {
	return nil, nil
}
func (f *fakeCustomRepo) CreateDef(_ context.Context, _ *domain.CustomObjectDef) error { return nil }
func (f *fakeCustomRepo) UpdateDef(_ context.Context, _ *domain.CustomObjectDef) error { return nil }
func (f *fakeCustomRepo) SoftDeleteDef(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}
func (f *fakeCustomRepo) ListRecords(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ domain.RecordFilter) ([]domain.CustomObjectRecord, int64, error) {
	return nil, 0, nil
}
func (f *fakeCustomRepo) GetRecord(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*domain.CustomObjectRecord, error) {
	return nil, nil
}
func (f *fakeCustomRepo) CreateRecord(_ context.Context, _ *domain.CustomObjectRecord) error {
	return nil
}
func (f *fakeCustomRepo) UpdateRecord(_ context.Context, _ *domain.CustomObjectRecord) error {
	return nil
}
func (f *fakeCustomRepo) SoftDeleteRecord(_ context.Context, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}

type fakeOrgSettingsUC struct {
	byEntity map[string][]domain.CustomFieldDef
}

func (f *fakeOrgSettingsUC) GetFieldDefs(_ context.Context, _ uuid.UUID, entityType string) ([]domain.CustomFieldDef, error) {
	// Mirror the real contract: an empty entityType returns every field def.
	if entityType == "" {
		var all []domain.CustomFieldDef
		for _, defs := range f.byEntity {
			all = append(all, defs...)
		}
		return all, nil
	}
	return f.byEntity[entityType], nil
}
func (f *fakeOrgSettingsUC) CreateFieldDef(_ context.Context, _ uuid.UUID, _ domain.CreateFieldDefInput) (*domain.CustomFieldDef, error) {
	return nil, nil
}
func (f *fakeOrgSettingsUC) UpdateFieldDef(_ context.Context, _ uuid.UUID, _ string, _ domain.UpdateFieldDefInput) (*domain.CustomFieldDef, error) {
	return nil, nil
}
func (f *fakeOrgSettingsUC) DeleteFieldDef(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakeOrgSettingsUC) ValidateCustomFields(_ context.Context, _ uuid.UUID, _ string, _ domain.JSON) error {
	return nil
}

// ============================================================
// Test fixtures
// ============================================================

// newDealRepo builds a registry repo seeded with a "deal" system object: a
// title (display) and a value field.
func newDealRepo() (*fakeRegistryRepo, uuid.UUID) {
	dealID := uuid.New()
	titleID := uuid.New()
	titleCol := "title"
	valueCol := "value"
	repo := &fakeRegistryRepo{
		defs: []domain.ObjectDef{{
			ID:             dealID,
			Slug:           "deal",
			Label:          "Deal",
			LabelPlural:    "Deals",
			Icon:           "💰",
			Color:          "#10B981",
			IsSystem:       true,
			Storage:        "table",
			DisplayFieldID: &titleID,
		}},
		fields: map[uuid.UUID][]domain.ObjectField{
			dealID: {
				{ID: titleID, ObjectDefID: dealID, Key: "title", Label: "Title", Type: "text", Options: domain.JSON("[]"), IsSystem: true, IsRequired: true, StorageKind: "column", MapsToColumn: &titleCol, Position: 0},
				{ID: uuid.New(), ObjectDefID: dealID, Key: "value", Label: "Value", Type: "number", Options: domain.JSON("[]"), IsSystem: true, StorageKind: "column", MapsToColumn: &valueCol, Position: 1},
			},
		},
	}
	return repo, dealID
}

func projectCustomDef() domain.CustomObjectDef {
	fields, _ := json.Marshal([]domain.CustomFieldDef{
		{Key: "name", Label: "Name", Type: "text"},
		{Key: "budget", Label: "Budget", Type: "number"},
	})
	return domain.CustomObjectDef{
		ID:          uuid.New(),
		Slug:        "project",
		Label:       "Project",
		LabelPlural: "Projects",
		Icon:        "📁",
		Fields:      domain.JSON(fields),
	}
}

// ============================================================
// Tests
// ============================================================

func TestListObjects_MergesSystemAndCustom(t *testing.T) {
	repo, _ := newDealRepo()
	customRepo := &fakeCustomRepo{defs: []domain.CustomObjectDef{projectCustomDef()}}
	orgUC := &fakeOrgSettingsUC{byEntity: map[string][]domain.CustomFieldDef{
		"deal": {{Key: "renewal_risk", Label: "Renewal Risk", Type: "select", Options: []string{"low", "med", "high"}, EntityType: "deal"}},
	}}
	uc := NewObjectRegistryUseCase(repo, customRepo, orgUC)

	got, err := uc.ListObjects(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListObjects error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(got))
	}

	deal := got[0]
	if deal.Slug != "deal" || !deal.IsSystem {
		t.Fatalf("expected system deal first, got %+v", deal)
	}
	// 2 native fields + 1 admin custom field.
	if deal.FieldCount != 3 {
		t.Errorf("deal field_count: want 3, got %d", deal.FieldCount)
	}

	project := got[1]
	if project.Slug != "project" || project.IsSystem {
		t.Fatalf("expected custom project second, got %+v", project)
	}
	if project.FieldCount != 2 {
		t.Errorf("project field_count: want 2, got %d", project.FieldCount)
	}

	if repo.ensureCalls != 1 {
		t.Errorf("expected EnsureSystemObjects called once, got %d", repo.ensureCalls)
	}
}

func TestGetSchema_SystemDealMergesCustomFields(t *testing.T) {
	repo, _ := newDealRepo()
	orgUC := &fakeOrgSettingsUC{byEntity: map[string][]domain.CustomFieldDef{
		"deal": {{Key: "renewal_risk", Label: "Renewal Risk", Type: "select", Options: []string{"low", "med", "high"}, EntityType: "deal"}},
	}}
	uc := NewObjectRegistryUseCase(repo, &fakeCustomRepo{}, orgUC)

	got, err := uc.GetSchema(context.Background(), uuid.New(), "deal")
	if err != nil {
		t.Fatalf("GetSchema error: %v", err)
	}
	if !got.IsSystem {
		t.Error("expected IsSystem=true for deal")
	}
	if got.DisplayField != "title" {
		t.Errorf("display_field: want title, got %q", got.DisplayField)
	}
	if len(got.Fields) != 3 {
		t.Fatalf("expected 3 fields (2 native + 1 custom), got %d", len(got.Fields))
	}

	// Native field flagged system + required.
	if got.Fields[0].Key != "title" || !got.Fields[0].IsSystem || !got.Fields[0].Required {
		t.Errorf("title field wrong: %+v", got.Fields[0])
	}
	// Custom field appended last, not system, with its select options.
	last := got.Fields[2]
	if last.Key != "renewal_risk" || last.IsSystem {
		t.Errorf("expected non-system renewal_risk last, got %+v", last)
	}
	if len(last.Options) != 3 || last.Options[0] != "low" {
		t.Errorf("renewal_risk options wrong: %+v", last.Options)
	}
}

func TestGetSchema_CustomObject(t *testing.T) {
	repo, _ := newDealRepo()
	customRepo := &fakeCustomRepo{defs: []domain.CustomObjectDef{projectCustomDef()}}
	uc := NewObjectRegistryUseCase(repo, customRepo, &fakeOrgSettingsUC{})

	got, err := uc.GetSchema(context.Background(), uuid.New(), "project")
	if err != nil {
		t.Fatalf("GetSchema error: %v", err)
	}
	if got.IsSystem {
		t.Error("expected IsSystem=false for custom object")
	}
	if got.DisplayField != "name" {
		t.Errorf("display_field: want name (first text field), got %q", got.DisplayField)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(got.Fields))
	}
	for _, f := range got.Fields {
		if f.IsSystem {
			t.Errorf("custom object field should not be system: %+v", f)
		}
	}
}

func TestGetSchema_NotFound(t *testing.T) {
	repo, _ := newDealRepo()
	uc := NewObjectRegistryUseCase(repo, &fakeCustomRepo{}, &fakeOrgSettingsUC{})

	_, err := uc.GetSchema(context.Background(), uuid.New(), "nope")
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected *domain.AppError, got %T (%v)", err, err)
	}
	if appErr.Code != 404 {
		t.Errorf("expected 404, got %d", appErr.Code)
	}
}

func TestGetSchema_SystemTakesPrecedenceOverCustomSlug(t *testing.T) {
	// A custom object that reuses a system slug must not shadow the system one.
	repo, _ := newDealRepo()
	shadow := projectCustomDef()
	shadow.Slug = "deal"
	customRepo := &fakeCustomRepo{defs: []domain.CustomObjectDef{shadow}}
	uc := NewObjectRegistryUseCase(repo, customRepo, &fakeOrgSettingsUC{})

	got, err := uc.GetSchema(context.Background(), uuid.New(), "deal")
	if err != nil {
		t.Fatalf("GetSchema error: %v", err)
	}
	if !got.IsSystem {
		t.Error("expected the system deal to win over a custom object reusing the slug")
	}
}
