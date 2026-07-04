package usecase

import (
	"context"
	"net/http"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fake comment repo (in-memory; author names not resolved — the usecase sets
// CanDelete, which is what these tests exercise)
// ============================================================

type fakeCommentRepo struct {
	comments []*domain.ReportComment
	deleted  map[uuid.UUID]bool
}

func newFakeCommentRepo() *fakeCommentRepo {
	return &fakeCommentRepo{deleted: map[uuid.UUID]bool{}}
}

func (f *fakeCommentRepo) Create(_ context.Context, c *domain.ReportComment) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	f.comments = append(f.comments, c)
	return nil
}

func (f *fakeCommentRepo) ListByReport(_ context.Context, _ uuid.UUID, reportID uuid.UUID) ([]domain.ReportCommentView, error) {
	var out []domain.ReportCommentView
	for _, c := range f.comments {
		if c.ReportID != reportID || f.deleted[c.ID] {
			continue
		}
		out = append(out, domain.ReportCommentView{ID: c.ID, AuthorID: c.AuthorID, Body: c.Body, CreatedAt: c.CreatedAt})
	}
	return out, nil
}

func (f *fakeCommentRepo) GetByID(_ context.Context, _ uuid.UUID, id uuid.UUID) (*domain.ReportComment, error) {
	for _, c := range f.comments {
		if c.ID == id && !f.deleted[c.ID] {
			return c, nil
		}
	}
	return nil, nil
}

func (f *fakeCommentRepo) SoftDelete(_ context.Context, _ uuid.UUID, id uuid.UUID) (int64, error) {
	for _, c := range f.comments {
		if c.ID == id && !f.deleted[c.ID] {
			f.deleted[c.ID] = true
			return 1, nil
		}
	}
	return 0, nil
}

func newCommentUC(env *reportEnv) (domain.ReportCommentUseCase, *fakeCommentRepo) {
	repo := newFakeCommentRepo()
	return NewReportCommentUseCase(env.uc, repo), repo
}

// ============================================================
// Access matrix (reuses the report env's real ReportUseCase gate)
// ============================================================

func TestReportComment_ViewerCanListButNotAdd(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, _ := newCommentUC(env)
	me := uuid.New()
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	env.shares.byRept[rep.ID] = []domain.ReportShare{shareRow(rep.ID, domain.ShareTargetUser, me, domain.ShareLevelView)}

	if _, err := uc.List(context.Background(), env.orgID, me, rep.ID); err != nil {
		t.Errorf("view sharee List = %v, want nil (any level can read)", err)
	}
	if _, err := uc.Add(context.Background(), env.orgID, me, rep.ID, domain.AddReportCommentInput{Body: "hi"}); err != domain.ErrForbidden {
		t.Errorf("view sharee Add = %v, want forbidden (needs >= comment)", err)
	}
}

func TestReportComment_CommenterCanAdd(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, repo := newCommentUC(env)
	me := uuid.New()
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	env.shares.byRept[rep.ID] = []domain.ReportShare{shareRow(rep.ID, domain.ShareTargetUser, me, domain.ShareLevelComment)}

	c, err := uc.Add(context.Background(), env.orgID, me, rep.ID, domain.AddReportCommentInput{Body: "  looks good  "})
	if err != nil {
		t.Fatalf("comment sharee Add = %v, want nil", err)
	}
	if c.Body != "looks good" {
		t.Errorf("body not trimmed: %q", c.Body)
	}
	if c.AuthorID == nil || *c.AuthorID != me {
		t.Errorf("author not set to the caller: %+v", c.AuthorID)
	}
	if len(repo.comments) != 1 {
		t.Errorf("comment not persisted: %d", len(repo.comments))
	}
}

func TestReportComment_BlankBodyRejected(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, _ := newCommentUC(env)
	me := uuid.New()
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	env.shares.byRept[rep.ID] = []domain.ReportShare{shareRow(rep.ID, domain.ShareTargetUser, me, domain.ShareLevelComment)}

	_, err := uc.Add(context.Background(), env.orgID, me, rep.ID, domain.AddReportCommentInput{Body: "   "})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Errorf("blank body code = %d, want 400", code)
	}
}

func TestReportComment_AuthorCanDeleteOwn(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, _ := newCommentUC(env)
	me := uuid.New()
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	env.shares.byRept[rep.ID] = []domain.ReportShare{shareRow(rep.ID, domain.ShareTargetUser, me, domain.ShareLevelComment)}

	c, err := uc.Add(context.Background(), env.orgID, me, rep.ID, domain.AddReportCommentInput{Body: "mine"})
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// The author is offered delete on their own comment.
	views, err := uc.List(context.Background(), env.orgID, me, rep.ID)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(views) != 1 || !views[0].CanDelete {
		t.Errorf("author should be able to delete own comment: %+v", views)
	}

	if err := uc.Delete(context.Background(), env.orgID, me, rep.ID, c.ID); err != nil {
		t.Errorf("author Delete = %v, want nil", err)
	}
}

func TestReportComment_NonAuthorNonManagerCannotDelete(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, _ := newCommentUC(env)
	author, other := uuid.New(), uuid.New()
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	env.shares.byRept[rep.ID] = []domain.ReportShare{
		shareRow(rep.ID, domain.ShareTargetUser, author, domain.ShareLevelComment),
		shareRow(rep.ID, domain.ShareTargetUser, other, domain.ShareLevelComment),
	}
	c, err := uc.Add(context.Background(), env.orgID, author, rep.ID, domain.AddReportCommentInput{Body: "theirs"})
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	if err := uc.Delete(context.Background(), env.orgID, other, rep.ID, c.ID); err != domain.ErrForbidden {
		t.Errorf("non-author non-manager Delete = %v, want forbidden", err)
	}
	// …and is not offered the delete affordance in the list.
	views, err := uc.List(context.Background(), env.orgID, other, rep.ID)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(views) != 1 || views[0].CanDelete {
		t.Errorf("non-author must not be offered delete: %+v", views)
	}
}

func TestReportComment_ManagerCanDeleteAny(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, _ := newCommentUC(env)
	author := uuid.New()
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	env.shares.byRept[rep.ID] = []domain.ReportShare{shareRow(rep.ID, domain.ShareTargetUser, author, domain.ShareLevelComment)}
	c, err := uc.Add(context.Background(), env.orgID, author, rep.ID, domain.AddReportCommentInput{Body: "theirs"})
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}

	// A caller with reports.manage (here: everyone, once caps allow) may delete
	// anyone's comment.
	env.caps.allow = true
	manager := uuid.New()
	if err := uc.Delete(context.Background(), env.orgID, manager, rep.ID, c.ID); err != nil {
		t.Errorf("manager Delete = %v, want nil", err)
	}
}

func TestReportComment_NoAccessIs404(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = false
	uc, _ := newCommentUC(env)
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	stranger := uuid.New()

	if _, err := uc.List(context.Background(), env.orgID, stranger, rep.ID); err != domain.ErrReportNotFound {
		t.Errorf("no-access List = %v, want 404", err)
	}
	if _, err := uc.Add(context.Background(), env.orgID, stranger, rep.ID, domain.AddReportCommentInput{Body: "hi"}); err != domain.ErrReportNotFound {
		t.Errorf("no-access Add = %v, want 404", err)
	}
}

func TestReportComment_DeleteUnknownCommentIs404(t *testing.T) {
	env := newReportEnv()
	env.caps.allow = true // caller is a manager, so access isn't the blocker
	uc, _ := newCommentUC(env)
	rep := seedForeignReport(env, domain.ReportVisibilityPrivate)
	if err := uc.Delete(context.Background(), env.orgID, uuid.New(), rep.ID, uuid.New()); err == nil {
		t.Error("deleting an unknown comment should 404")
	} else if code := appErrCode(t, err); code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", code)
	}
}
