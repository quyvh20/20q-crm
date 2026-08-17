# Filtering Overhaul — one filter engine for every list

Date: 2026-08-15. Grounded in an 8-surface audit of every list/filter path in the repo.

## The finding

The repo already contains a hardened filter engine — the P9 report compiler
(`report_sql.go`: per-type operator whitelist, typed JSONB casts, bind-arg
discipline, `escapeReportLike`) plus the M5 segment AST compiler
(`segment_sql.go`: AND/OR/NOT nesting, depth/leaf caps) — but it is wired only
to reports and marketing segments. Every actual list runs on an equality-only
`map[string]string` with four defects:

1. **Silent-empty trap** (shipped 3×: sort_by, pipeline_id, lead_score): any
   filter key the adapter doesn't explicitly bind falls through to
   `custom_fields ->> key = value` against a key no row has → empty list, no
   error. Filtering contacts by `email` — a real native column — *lies*.
2. **FLS oracle**: filter keys bypass the field mask; `?hidden_field=v` filters
   and leaks by membership.
3. **Tag filter is a lie off contacts**: UI renders tag chips for every object;
   only `ContactFilter` carries `TagIDs`.
4. **Equality only**: no operators, no OR, no ranges, no relative dates, no
   is_empty — while the full matrix sits unreachable in `reportOperatorsByType`.

Plus: the AI-semantic toggle is dead end-to-end on the unified path; the kanban
board silently ignores every active filter; only relation fields get any filter
UI; there are no saved views.

## The design

**F1 — one operator matrix in `domain`.** Move `reportOperatorsByType` to
`domain.FilterOperatorsByType`; repository keeps an alias. Add new operators:
`between` (number/date, value `[a,b]`), `last_n_days` / `next_n_days` (date,
value N, clamped 1..3650), `on` (date, day-granular equality — kills the
midnight-eq footgun). Served to the FE on the schema endpoint as
`filter_operators`, so the three hand-mirrored operator maps collapse to one.
Reports and segments gain the new operators automatically (their compiler is
the same leaf builder).

**F2 — filter AST on the record list chokepoint.** `RecordListInput.Filter
*ReportFilterGroup` (same JSON shape as reports/automation conditions — one
filter language everywhere). New reserved list param `filter` = URL-encoded
JSON. Flow:

- `record_handler.List` parses JSON → 400 on malformed.
- `RecordService.List` builds the catalog via `reportCatalogForDef` (same
  registry+virtual-fields catalog reports use), then validates: depth ≤ 5,
  leaves ≤ 50, unknown field → 400, operator-per-type via the domain matrix →
  400, FLS-hidden field → 403. Validation lives in usecase; the repository
  compile can then only fail on programmer error.
- Adapters thread `Filter` + `FilterCatalog` through the typed filter structs
  (`ContactFilter`/`DealFilter`/`CompanyFilter`/`RecordFilter`).
- Repos compile via new `RecordFilterExpr` (delegates to
  `buildReportFilterGroup`) and append ONE `.Where(sql, args...)` to the
  existing chain — scope predicates, keyset cursors, q-search all unchanged.
  No row predicate in the fragment (repos already apply scope).

**F3 — legacy params become honest.** Adapter typed bindings for today's exact
keys (company/owner_user_id/stage/contact/pipeline_id) stay untouched (kanban +
automation compatibility). The *fall-through* changes: leftover keys resolve
against the catalog → converted to `eq` leaves ANDed into the AST (native
fields start working; custom JSONB fields keep exact-match semantics); a key
that resolves nowhere → 400 (`unknown filter field`). FLS applies to these too.
Consequence: automation `find_records` with a stale field key now fails loudly
in the run log instead of silently acting on zero records — the repo's own
"rejected rather than ignored" doctrine.

**F4 — tags for every object.** `TagIDs` added to Deal/Company/Record filters;
contacts keep `contact_tags` (source of truth per M5); others compile an EXISTS
over `object_links` tag edges. The chips the UI already renders everywhere stop
lying.

**F5 — saved views.** New `list_views` table (org, owner, object_slug, name,
definition jsonb {filter, q, tags, sort_by, sort_order}, shared, timestamps).
Boot guard in main.go (golang-migrate is dead on prod) + numbered SQL for CI
fixtures. CRUD under `/api/registry/objects/:slug/views`; save-time validation
runs the same F2 validator (segment doctrine: FLS airtight at save because
saved definitions are trusted later). Personal by default; `shared` makes a
view org-visible read-only. Mutations owner-only.

**F6 — frontend.** New `features/objects/filters/` family composing
`@/components/ui` (+ minimal Popover/Combobox primitives, token-styled, no new
deps):

- FilterBar on ObjectListView: "Add filter" → searchable field picker over ALL
  filterable fields (not just relations) → per-type operator menu (from the
  server-served matrix) → per-type value editor (RelationPicker for relations,
  date/number/select/boolean editors, two-input between, N-days input).
- Active filters render as pills with inline edit + remove; any/all (AND/OR)
  toggle; Clear all.
- URL persistence via one `flt` param (compact JSON); legacy `f.*` params are
  read once and migrated; reserved-key collisions dissolve.
- Saved-views dropdown: apply/save/update/delete, with a "modified" indicator.
- Kanban receives the same q/tags/filter payload it currently drops.
- The dead AI-semantic toggle is removed (it changes placeholder text only;
  `ContactFilter.Semantic` has zero consumers on this path).
- Relation dropdowns in the filter bar are replaced by the server-searching
  RelationPicker (no more 200-option cap).

**Bonus fixes found by the audit** (small, surgical):
- Workflows list "Inactive" pill is a silent no-op (`activeOnly :=
  c.Query("active") == "true"`) → proper tri-state.
- TasksPage overdue tab mints a fresh `due_before` every render, churning the
  react-query key → memoized.

## Non-goals (documented follow-ups)

- Semantic search composed with filters (needs vector-arm-in-WHERE design).
- Sort expansion (keyset whitelist is compile-time by design; company/custom
  sort needs its own pass), column chooser, result totals for keyset lists.
- Nested group UI on lists (backend accepts depth 5; UI ships flat rows +
  any/all — segments keep the deep builder).
- Reports/segments FE upgrades beyond inheriting new operators; behavioral
  (campaign-event) segment leaves; object_links "linked to X" leaves.
- Tasks/activities lists (not registry objects; R9.5 keeps them report-only).

## Traps honored

- gorm `db.Raw`/`.Where` treats every literal `?` in SQL text as a bind
  placeholder — new SQL uses `{0,1}`-style constructs, never literal `?`.
- Company List has no scope predicate on purpose (ownerless) — untouched.
- Boot guards, not migrations, for prod DDL; numbered SQL only for CI.
- Never `gofmt -w`; build + vet are the gates. FE: `npx vitest run`, `npx tsc -b`.
- Modal primitive: no autoFocus. New UI composes `@/components/ui` tokens.
