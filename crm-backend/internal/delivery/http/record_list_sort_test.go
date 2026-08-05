package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================
// R7.3 — the registry list route's query-param contract
// ============================================================
//
// GET /api/registry/objects/:slug/records treats every UNRESERVED query param as
// a field filter. That is what makes one handler serve every object's filters
// without per-object code, and it is also why an un-reserved param fails in the
// worst possible way: `?sort_by=value` did not error and did not sort — it asked
// for the records whose custom field "sort_by" equals "value", and returned an
// empty page. A status-code test would have called that a pass.

type sortSpyRecords struct {
	domain.RecordService
	got  domain.RecordListInput
	page *domain.RecordList
}

func (f *sortSpyRecords) List(_ context.Context, _ uuid.UUID, _ string, in domain.RecordListInput) (*domain.RecordList, error) {
	f.got = in
	if f.page != nil {
		return f.page, nil
	}
	return &domain.RecordList{Records: []domain.UniformRecord{}}, nil
}

func serveList(t *testing.T, h *RecordHandler, slug, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("org_id", uuid.New())
	c.Params = gin.Params{{Key: "slug", Value: slug}}
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
	h.List(c)
	return w
}

func newSortListHandler(svc domain.RecordService) *RecordHandler {
	return NewRecordHandler(svc, nil, nil, nil, nil, nil, nil)
}

func TestList_SortParamsReachTheServiceAndNotTheFilters(t *testing.T) {
	spy := &sortSpyRecords{}
	w := serveList(t, newSortListHandler(spy), "deal", "sort_by=value&sort_order=asc&company=not-a-uuid")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if spy.got.SortBy != "value" || spy.got.SortOrder != "asc" {
		t.Errorf("service saw SortBy=%q SortOrder=%q, want value/asc", spy.got.SortBy, spy.got.SortOrder)
	}
	// The regression this route actually had: sort_by swallowed as a jsonb filter.
	for _, k := range []string{"sort_by", "sort_order"} {
		if v, ok := spy.got.Filters[k]; ok {
			t.Errorf("%q leaked into Filters as %q — it would become a custom_fields ->> %q = %q test that matches nothing", k, v, k, v)
		}
	}
	// …while a genuinely unreserved param still becomes a filter, which is the
	// property the reservation must not break.
	if spy.got.Filters["company"] != "not-a-uuid" {
		t.Errorf("Filters[company] = %q, want the raw value passed through", spy.got.Filters["company"])
	}
}

// The applied ordering is echoed to the client, because after the whitelist
// fallback "what you asked for" and "what you got" can differ, and a UI that
// draws its sort arrow from the request rather than the response would point at
// a column the query did not order by.
func TestList_EchoesTheAppliedSortToTheClient(t *testing.T) {
	spy := &sortSpyRecords{page: &domain.RecordList{
		Records: []domain.UniformRecord{},
		Sort:    &domain.RecordSort{By: "created_at", Order: "desc", Sortable: []string{"created_at", "value"}},
	}}
	w := serveList(t, newSortListHandler(spy), "deal", "sort_by=nonsense")

	var body struct {
		Data struct {
			Sort *domain.RecordSort `json:"sort"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body.String())
	}
	if body.Data.Sort == nil {
		t.Fatal("response carried no sort block")
	}
	if body.Data.Sort.By != "created_at" || len(body.Data.Sort.Sortable) != 2 {
		t.Errorf("sort = %+v", body.Data.Sort)
	}
}

// An object with no sort at all must serve a response with NO sort key, not one
// with an empty object: the frontend keys "should I render sort controls?" off
// its absence.
func TestList_OmitsSortBlockEntirelyWhenUnsortable(t *testing.T) {
	spy := &sortSpyRecords{page: &domain.RecordList{Records: []domain.UniformRecord{}}}
	w := serveList(t, newSortListHandler(spy), "project", "")

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw["data"], &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if _, ok := data["sort"]; ok {
		t.Errorf("unsortable object served a sort key: %s", w.Body.String())
	}
}
