package http

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// The reserved `filter` query param (filtering overhaul F3)
// ============================================================
//
// GET /api/registry/objects/:slug/records treats every UNRESERVED query param
// as a field filter, so reserving `filter` matters twice over: a malformed AST
// must be a 400 at the handler (parseRecordListQuery), and a well-formed one
// must ride RecordListInput.Filter — never leak into Filters, where it would
// become a `custom_fields ->> 'filter' = <json>` test that matches nothing
// (the exact sort_by failure mode R7.3 fixed).

type filterParamSpy struct {
	domain.RecordService
	called bool
	got    domain.RecordListInput
}

func (f *filterParamSpy) List(_ context.Context, _ uuid.UUID, _ string, in domain.RecordListInput) (*domain.RecordList, error) {
	f.called = true
	f.got = in
	return &domain.RecordList{Records: []domain.UniformRecord{}}, nil
}

// A filter param that is not valid JSON is rejected at parse time with the
// exact message the frontend keys off — and the service is never reached, so a
// typo can't burn a query.
func TestList_MalformedFilterParamIs400(t *testing.T) {
	spy := &filterParamSpy{}
	w := serveList(t, newSortListHandler(spy), "contact", "filter="+url.QueryEscape("{not json"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "filter is not valid JSON") {
		t.Errorf("body = %s, want the parse-error message", w.Body.String())
	}
	if spy.called {
		t.Error("a malformed filter must not reach RecordService.List")
	}
}

// A well-formed AST is parsed into RecordListInput.Filter, and — because
// `filter` is in reservedListParams — the raw JSON must NOT also appear in
// Filters. Un-reserved params keep becoming field filters alongside it, which
// is the property the reservation must not break.
func TestList_FilterParamIsReservedNotAFieldFilter(t *testing.T) {
	spy := &filterParamSpy{}
	ast := `{"op":"AND","rules":[{"field":"email","operator":"eq","value":"x@y.com"}]}`
	w := serveList(t, newSortListHandler(spy), "contact", "filter="+url.QueryEscape(ast)+"&industry=tech")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	if v, ok := spy.got.Filters["filter"]; ok {
		t.Errorf("filter leaked into Filters as %q — it would become a custom_fields ->> 'filter' test that matches nothing", v)
	}
	if spy.got.Filter == nil {
		t.Fatal("the AST did not reach RecordListInput.Filter")
	}
	if spy.got.Filter.Op != "AND" || len(spy.got.Filter.Rules) != 1 {
		t.Fatalf("parsed AST = %+v, want the one-rule AND group", spy.got.Filter)
	}
	if r := spy.got.Filter.Rules[0]; r.Field != "email" || r.Operator != "eq" || r.Value != "x@y.com" {
		t.Errorf("parsed rule = %+v", r)
	}
	// A genuinely unreserved param still becomes a field filter.
	if spy.got.Filters["industry"] != "tech" {
		t.Errorf("Filters[industry] = %q, want tech", spy.got.Filters["industry"])
	}
}
