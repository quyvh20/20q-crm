package usecase

import (
	"context"
	"encoding/json"
	"errors"
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

func (f *fakeRegistryRepo) SetNumberPrefix(_ context.Context, _ uuid.UUID, slug, prefix string) error {
	for i := range f.defs {
		if f.defs[i].Slug == slug {
			if prefix == "" {
				f.defs[i].NumberPrefix = nil
			} else {
				p := prefix
				f.defs[i].NumberPrefix = &p
			}
			return nil
		}
	}
	return errors.New("not found")
}

func (f *fakeRegistryRepo) ListCustomFields(_ context.Context, defID uuid.UUID) ([]domain.ObjectField, error) {
	var out []domain.ObjectField
	for _, fld := range f.fields[defID] {
		if !fld.IsSystem {
			out = append(out, fld)
		}
	}
	return out, nil
}

func (f *fakeRegistryRepo) GetFieldByDefKey(_ context.Context, defID uuid.UUID, key string) (*domain.ObjectField, error) {
	for i := range f.fields[defID] {
		if f.fields[defID][i].Key == key {
			return &f.fields[defID][i], nil
		}
	}
	return nil, nil
}

func (f *fakeRegistryRepo) FindCustomFieldByKey(_ context.Context, _ uuid.UUID, key string) (*domain.ObjectField, string, error) {
	for di := range f.defs {
		defID := f.defs[di].ID
		for i := range f.fields[defID] {
			if f.fields[defID][i].Key == key && !f.fields[defID][i].IsSystem {
				return &f.fields[defID][i], f.defs[di].Slug, nil
			}
		}
	}
	return nil, "", nil
}

func (f *fakeRegistryRepo) CreateField(_ context.Context, fld *domain.ObjectField) error {
	f.fields[fld.ObjectDefID] = append(f.fields[fld.ObjectDefID], *fld)
	return nil
}

func (f *fakeRegistryRepo) SaveField(_ context.Context, fld *domain.ObjectField) error {
	for i := range f.fields[fld.ObjectDefID] {
		if f.fields[fld.ObjectDefID][i].ID == fld.ID {
			f.fields[fld.ObjectDefID][i] = *fld
			return nil
		}
	}
	return nil
}

func (f *fakeRegistryRepo) SoftDeleteFieldByID(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	for defID := range f.fields {
		kept := f.fields[defID][:0]
		for _, fld := range f.fields[defID] {
			if fld.ID != id {
				kept = append(kept, fld)
			}
		}
		f.fields[defID] = kept
	}
	return nil
}

// addCustomObject registers a custom object (is_system=false) in the fake registry —
// the post-P7 storage, where custom objects live in object_defs/object_fields just
// like system objects. Returns its def id so fields can be attached.
func addCustomObject(repo *fakeRegistryRepo, slug, label string) uuid.UUID {
	id := uuid.New()
	repo.defs = append(repo.defs, domain.ObjectDef{
		ID: id, Slug: slug, Label: label, LabelPlural: label + "s",
		Icon: "📁", Color: "#6B7280", IsSystem: false, Storage: "jsonb",
	})
	repo.fields[id] = nil
	return id
}

// addCustomField appends an admin-defined (is_system=false) field to a def's field
// list — the post-P7 storage for system-object custom fields (object_fields), which
// the registry now reads directly instead of merging the legacy org_settings blob.
func addCustomField(repo *fakeRegistryRepo, defID uuid.UUID, key, label, fieldType string, options []string) {
	opts := domain.JSON("[]")
	if len(options) > 0 {
		raw, _ := json.Marshal(options)
		opts = domain.JSON(raw)
	}
	repo.fields[defID] = append(repo.fields[defID], domain.ObjectField{
		ID:          uuid.New(),
		ObjectDefID: defID,
		Key:         key,
		Label:       label,
		Type:        fieldType,
		Options:     opts,
		IsSystem:    false,
		StorageKind: "jsonb",
		Position:    len(repo.fields[defID]),
	})
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

// newProject registers a custom "project" object with name + budget fields.
func newProject(repo *fakeRegistryRepo) uuid.UUID {
	pid := addCustomObject(repo, "project", "Project")
	addCustomField(repo, pid, "name", "Name", "text", nil)
	addCustomField(repo, pid, "budget", "Budget", "number", nil)
	return pid
}

// ============================================================
// Tests
// ============================================================

func TestListObjects_MergesSystemAndCustom(t *testing.T) {
	repo, dealID := newDealRepo()
	addCustomField(repo, dealID, "renewal_risk", "Renewal Risk", "select", []string{"low", "med", "high"})
	newProject(repo)
	uc := NewObjectRegistryUseCase(repo)

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

func TestGetSchema_NumberPrefixDefaultsToUpperSlug(t *testing.T) {
	repo, _ := newDealRepo()
	uc := NewObjectRegistryUseCase(repo)
	org := uuid.New()
	ctx := context.Background()

	// With no prefix configured, the descriptor falls back to the uppercased slug.
	got, err := uc.GetSchema(ctx, org, "deal")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if got.NumberPrefix != "DEAL" {
		t.Fatalf("default number_prefix: want DEAL, got %q", got.NumberPrefix)
	}

	// After SetNumberPrefix, the configured value wins.
	if err := uc.SetNumberPrefix(ctx, org, "deal", "OPP"); err != nil {
		t.Fatalf("SetNumberPrefix: %v", err)
	}
	got2, err := uc.GetSchema(ctx, org, "deal")
	if err != nil {
		t.Fatalf("GetSchema after set: %v", err)
	}
	if got2.NumberPrefix != "OPP" {
		t.Fatalf("configured number_prefix: want OPP, got %q", got2.NumberPrefix)
	}

	// Clearing it resets to the slug default.
	if err := uc.SetNumberPrefix(ctx, org, "deal", ""); err != nil {
		t.Fatalf("SetNumberPrefix clear: %v", err)
	}
	got3, _ := uc.GetSchema(ctx, org, "deal")
	if got3.NumberPrefix != "DEAL" {
		t.Fatalf("cleared number_prefix: want DEAL, got %q", got3.NumberPrefix)
	}
}

func TestGetSchema_SystemDealMergesCustomFields(t *testing.T) {
	repo, dealID := newDealRepo()
	addCustomField(repo, dealID, "renewal_risk", "Renewal Risk", "select", []string{"low", "med", "high"})
	uc := NewObjectRegistryUseCase(repo)

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
	newProject(repo)
	uc := NewObjectRegistryUseCase(repo)

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
	uc := NewObjectRegistryUseCase(repo)

	_, err := uc.GetSchema(context.Background(), uuid.New(), "nope")
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected *domain.AppError, got %T (%v)", err, err)
	}
	if appErr.Code != 404 {
		t.Errorf("expected 404, got %d", appErr.Code)
	}
}
