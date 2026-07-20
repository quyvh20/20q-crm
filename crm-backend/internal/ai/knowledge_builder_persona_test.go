package ai

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// The per-org AI persona an industry template installs. Before this existed the
// prompt opened with a hardcoded identity line and org_settings.ai_context_override
// had no reader at all — every template's persona was written and then ignored.

type stubOrgSettingsRepo struct {
	settings *domain.OrgSettings
	err      error
}

func (s *stubOrgSettingsRepo) GetByOrgID(ctx context.Context, orgID uuid.UUID) (*domain.OrgSettings, error) {
	return s.settings, s.err
}
func (s *stubOrgSettingsRepo) Upsert(ctx context.Context, settings *domain.OrgSettings) error {
	return nil
}

func personaOf(t *testing.T, repo domain.OrgSettingsRepository) string {
	t.Helper()
	b := NewKnowledgeBuilder(&mockKBRepo{}, &mockOrgSettingsUC{}, nil, nil)
	if repo != nil {
		b.SetOrgSettingsRepo(repo)
	}
	return b.persona(context.Background(), uuid.New())
}

func ptr(s string) *string { return &s }

func TestPersonaDefaultsWhenUnset(t *testing.T) {
	// No repo wired at all — the state every existing NewKnowledgeBuilder caller is
	// in until main.go injects one.
	if got := personaOf(t, nil); got != defaultPersona {
		t.Errorf("expected default persona, got %q", got)
	}

	// Row absent, override nil, override blank: all must fall back rather than
	// producing an empty first line in the prompt.
	for name, repo := range map[string]domain.OrgSettingsRepository{
		"no row":         &stubOrgSettingsRepo{settings: nil},
		"nil override":   &stubOrgSettingsRepo{settings: &domain.OrgSettings{}},
		"blank override": &stubOrgSettingsRepo{settings: &domain.OrgSettings{AIContextOverride: ptr("   ")}},
	} {
		if got := personaOf(t, repo); got != defaultPersona {
			t.Errorf("%s: expected default persona, got %q", name, got)
		}
	}
}

func TestPersonaUsesOverride(t *testing.T) {
	const p = "You are a CRM assistant for a real estate agency."
	got := personaOf(t, &stubOrgSettingsRepo{settings: &domain.OrgSettings{AIContextOverride: ptr(p)}})
	if got != p {
		t.Errorf("expected the org persona, got %q", got)
	}
}

// A blip reading one optional row must not take the whole assistant down.
func TestPersonaFallsBackOnRepoError(t *testing.T) {
	repo := &stubOrgSettingsRepo{err: context.DeadlineExceeded}
	if got := personaOf(t, repo); got != defaultPersona {
		t.Errorf("a repo error must degrade to the default persona, got %q", got)
	}
}

func TestBuildSystemPromptOpensWithPersona(t *testing.T) {
	const p = "You are a CRM assistant for a dental practice."
	b := NewKnowledgeBuilder(&mockKBRepo{}, &mockOrgSettingsUC{}, nil, nil)
	b.SetOrgSettingsRepo(&stubOrgSettingsRepo{settings: &domain.OrgSettings{AIContextOverride: ptr(p)}})

	prompt, err := b.BuildSystemPrompt(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if !strings.HasPrefix(prompt, p) {
		t.Errorf("prompt must OPEN with the persona; got first line %q", strings.SplitN(prompt, "\n", 2)[0])
	}
	// Appending instead of replacing would leave two competing identity statements.
	if strings.Contains(prompt, defaultPersona) {
		t.Error("the default identity line must be REPLACED by the persona, not kept alongside it")
	}
	// The rest of the prompt must survive intact.
	for _, section := range []string{"COMPANY:", "PRODUCTS & SERVICES:", "CRM SCHEMA", "CRITICAL INSTRUCTIONS:"} {
		if !strings.Contains(prompt, section) {
			t.Errorf("prompt lost section %q", section)
		}
	}
}

// An org with no persona must get exactly the prompt it got before this feature.
func TestBuildSystemPromptUnchangedWithoutPersona(t *testing.T) {
	b := NewKnowledgeBuilder(&mockKBRepo{}, &mockOrgSettingsUC{}, nil, nil)
	prompt, err := b.BuildSystemPrompt(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if !strings.HasPrefix(prompt, "You are an AI sales assistant.\n") {
		t.Errorf("prompt for a persona-less org changed; first line %q", strings.SplitN(prompt, "\n", 2)[0])
	}
}
