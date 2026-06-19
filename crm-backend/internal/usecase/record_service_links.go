package usecase

import (
	"context"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// Universal relationships + tags (plan §3.3, P4).
//
// object_links is the single store for "any record relates to any record." Tags
// are modelled as the reserved edge relation_key='tags' → to_slug='tag', except
// for contacts, which keep their legacy contact_tags store until P7. RecordService
// hides that split so every caller sees one tag API (D4).
//
// All link/tag operations validate the record(s) through Get first, which already
// resolves system-vs-custom storage and enforces org scoping — so a forged slug
// or a cross-tenant id is rejected before any edge is written.

const (
	tagSlug        = "tag"  // reserved to_slug for tag edges
	tagRelationKey = "tags" // reserved relation_key for tag edges
)

// ============================================================
// Relationships
// ============================================================

// AddLink relates a record to another record. Idempotent: an existing edge is
// returned rather than duplicated. Tag edges must go through AddTag (which keeps
// contacts on their legacy store), so they are rejected here.
func (s *recordService) AddLink(ctx context.Context, orgID, userID uuid.UUID, slug string, id uuid.UUID, in domain.LinkInput) (*domain.LinkView, error) {
	if err := s.requireLinks(); err != nil {
		return nil, err
	}

	relationKey := strings.TrimSpace(in.RelationKey)
	if !slugRegex.MatchString(relationKey) {
		return nil, domain.NewAppError(http.StatusBadRequest, "relation_key must be lowercase alphanumeric with underscores, 1-50 chars, starting with a letter")
	}
	if relationKey == tagRelationKey || in.ToSlug == tagSlug {
		return nil, domain.NewAppError(http.StatusBadRequest, "use the tags endpoint to tag a record")
	}
	if slug == in.ToSlug && id == in.ToID {
		return nil, domain.NewAppError(http.StatusBadRequest, "a record cannot be linked to itself")
	}

	// Both endpoints must exist within the org. Get enforces org scope, resolves
	// storage, and 404s an unknown slug or id.
	if _, err := s.Get(ctx, orgID, slug, id); err != nil {
		return nil, err
	}
	target, err := s.Get(ctx, orgID, in.ToSlug, in.ToID)
	if err != nil {
		return nil, err
	}

	link, err := s.upsertEdge(ctx, orgID, userID, slug, id, relationKey, in.ToSlug, in.ToID)
	if err != nil {
		return nil, err
	}
	return &domain.LinkView{
		ID:          link.ID,
		RelationKey: link.RelationKey,
		ToSlug:      link.ToSlug,
		ToID:        link.ToID,
		ToDisplay:   target.Display,
	}, nil
}

// ListLinks returns a record's outgoing relationships (tag edges excluded), each
// resolved to the target's current display title. Targets are resolved one Get
// per link — acceptable for a single record's relationship panel; the plan's
// batch N+1 guard (§11) targets record *list* endpoints, not this one.
func (s *recordService) ListLinks(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]domain.LinkView, error) {
	if err := s.requireLinks(); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, orgID, slug, id); err != nil {
		return nil, err
	}

	links, err := s.linkRepo.ListFrom(ctx, orgID, slug, id)
	if err != nil {
		return nil, err
	}

	views := make([]domain.LinkView, 0, len(links))
	for i := range links {
		l := links[i]
		if l.ToSlug == tagSlug || l.RelationKey == tagRelationKey {
			continue // tags surface through ListTags
		}
		views = append(views, domain.LinkView{
			ID:          l.ID,
			RelationKey: l.RelationKey,
			ToSlug:      l.ToSlug,
			ToID:        l.ToID,
			ToDisplay:   s.resolveDisplay(ctx, orgID, l.ToSlug, l.ToID),
		})
	}
	return views, nil
}

// RemoveLink soft-deletes one relationship edge by id.
func (s *recordService) RemoveLink(ctx context.Context, orgID, linkID uuid.UUID) error {
	if err := s.requireLinks(); err != nil {
		return err
	}
	link, err := s.linkRepo.GetByID(ctx, orgID, linkID)
	if err != nil {
		return err
	}
	if link == nil {
		return domain.NewAppError(http.StatusNotFound, "link not found")
	}
	ok, err := s.linkRepo.SoftDelete(ctx, orgID, linkID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewAppError(http.StatusNotFound, "link not found")
	}
	return nil
}

// ============================================================
// Tags (uniform across every object)
// ============================================================

// ListTags returns a record's tags. Contacts read from contact_tags; every other
// object reads its 'tags' edges from object_links. The shape is identical.
func (s *recordService) ListTags(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]domain.Tag, error) {
	if err := s.requireTags(); err != nil {
		return nil, err
	}
	if _, err := s.Get(ctx, orgID, slug, id); err != nil {
		return nil, err
	}

	tagIDs, err := s.tagIDsFor(ctx, orgID, slug, id)
	if err != nil {
		return nil, err
	}
	return s.resolveTags(ctx, orgID, tagIDs)
}

// AddTag tags a record. Idempotent. Contacts write to contact_tags; everyone else
// writes a 'tags' edge.
func (s *recordService) AddTag(ctx context.Context, orgID, userID uuid.UUID, slug string, id, tagID uuid.UUID) error {
	if err := s.requireTags(); err != nil {
		return err
	}
	if _, err := s.Get(ctx, orgID, slug, id); err != nil {
		return err
	}
	if err := s.requireTagInOrg(ctx, orgID, tagID); err != nil {
		return err
	}

	if slug == "contact" {
		return s.linkRepo.AddContactTag(ctx, id, tagID)
	}
	_, err := s.upsertEdge(ctx, orgID, userID, slug, id, tagRelationKey, tagSlug, tagID)
	return err
}

// RemoveTag untags a record. Idempotent: removing an absent tag is a no-op.
func (s *recordService) RemoveTag(ctx context.Context, orgID uuid.UUID, slug string, id, tagID uuid.UUID) error {
	if err := s.requireTags(); err != nil {
		return err
	}
	if _, err := s.Get(ctx, orgID, slug, id); err != nil {
		return err
	}

	if slug == "contact" {
		_, err := s.linkRepo.RemoveContactTag(ctx, id, tagID)
		return err
	}
	edge, err := s.linkRepo.FindEdge(ctx, orgID, slug, id, tagRelationKey, tagSlug, tagID)
	if err != nil {
		return err
	}
	if edge == nil {
		return nil // already untagged
	}
	_, err = s.linkRepo.SoftDelete(ctx, orgID, edge.ID)
	return err
}

// ============================================================
// Helpers
// ============================================================

// upsertEdge creates an edge idempotently: an existing active edge is returned
// as-is, and a unique-index race (two concurrent identical adds) is recovered by
// re-finding the winner's row.
func (s *recordService) upsertEdge(ctx context.Context, orgID, userID uuid.UUID, fromSlug string, fromID uuid.UUID, relationKey, toSlug string, toID uuid.UUID) (*domain.ObjectLink, error) {
	if existing, err := s.linkRepo.FindEdge(ctx, orgID, fromSlug, fromID, relationKey, toSlug, toID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	link := &domain.ObjectLink{
		OrgID:       orgID,
		FromSlug:    fromSlug,
		FromID:      fromID,
		ToSlug:      toSlug,
		ToID:        toID,
		RelationKey: relationKey,
	}
	if userID != uuid.Nil {
		link.CreatedBy = &userID
	}
	if err := s.linkRepo.Create(ctx, link); err != nil {
		// Lost a race on the unique edge — the winner's row is now committed.
		if existing, ferr := s.linkRepo.FindEdge(ctx, orgID, fromSlug, fromID, relationKey, toSlug, toID); ferr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return link, nil
}

// tagIDsFor returns the tag ids attached to a record, from whichever store backs
// that object's tags.
func (s *recordService) tagIDsFor(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]uuid.UUID, error) {
	if slug == "contact" {
		return s.linkRepo.ListContactTagIDs(ctx, id)
	}
	links, err := s.linkRepo.ListFrom(ctx, orgID, slug, id)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0)
	for i := range links {
		if links[i].ToSlug == tagSlug && links[i].RelationKey == tagRelationKey {
			ids = append(ids, links[i].ToID)
		}
	}
	return ids, nil
}

// resolveTags maps tag ids to Tag rows, dropping ids that no longer exist in the
// org (a defensive guard against stale edges). Order follows tagRepo.List (name).
func (s *recordService) resolveTags(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]domain.Tag, error) {
	out := make([]domain.Tag, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	all, err := s.tagRepo.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	want := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	for _, t := range all {
		if want[t.ID] {
			out = append(out, t)
		}
	}
	return out, nil
}

// resolveDisplay returns a target record's current display title, or "" if it
// can't be loaded (e.g. concurrently deleted) — link listing degrades gracefully
// rather than failing wholesale.
func (s *recordService) resolveDisplay(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) string {
	rec, err := s.Get(ctx, orgID, slug, id)
	if err != nil || rec == nil {
		return ""
	}
	return rec.Display
}

func (s *recordService) requireTagInOrg(ctx context.Context, orgID, tagID uuid.UUID) error {
	tag, err := s.tagRepo.GetByID(ctx, orgID, tagID)
	if err != nil {
		return err
	}
	if tag == nil {
		return domain.NewAppError(http.StatusNotFound, "tag not found")
	}
	return nil
}

func (s *recordService) requireLinks() error {
	if s.linkRepo == nil {
		return domain.NewAppError(http.StatusInternalServerError, "relationships are not configured")
	}
	return nil
}

func (s *recordService) requireTags() error {
	if s.linkRepo == nil || s.tagRepo == nil {
		return domain.NewAppError(http.StatusInternalServerError, "tags are not configured")
	}
	return nil
}
