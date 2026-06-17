# Object Model — "Salesforce, but Simple" Implementation Plan

> **Vision:** One object engine. Every object — Contact, Deal, Company, and anything a
> user invents — is a first-class citizen with the same fields, relationships, views,
> permissions, automation, and AI. Powerful like Salesforce, but with one-tenth the
> concepts a user has to learn.
>
> **Through-line of this whole plan:** *all objects are equal.* Every phase moves one more
> capability from "Contacts only" or "two separate stacks" to "every object, the same way."

**Status:** Proposal / ready to implement · **Sibling doc:** `workflow_builder_improvement_plan.md`

---

## Table of Contents

1. [Can it be like Salesforce but simpler?](#1-can-it-be-like-salesforce-but-simpler-yes)
2. [Where we are today (the "weird" part, diagnosed)](#2-where-we-are-today-the-weird-part-diagnosed)
3. [Target architecture — the Object Registry](#3-target-architecture--the-object-registry)
4. [Concrete data model (DDL)](#4-concrete-data-model-ddl)
5. [How a request flows (worked examples)](#5-how-a-request-flows-worked-examples)
6. [API surface](#6-api-surface)
7. [Security model — equal *and* safe](#7-security-model--equal-and-safe)
8. [Why users will love it](#8-why-users-will-love-it)
9. [Delivery plan (phased)](#9-delivery-plan-phased)
10. [Risk register & hard problems](#10-risk-register--hard-problems)
11. [Performance & indexing](#11-performance--indexing)
12. [Testing & rollout strategy](#12-testing--rollout-strategy)
13. [Migration & safety rules](#13-migration--safety-rules)
14. [Effort & timeline rollup](#14-effort--timeline-rollup)
15. [Non-goals — what we deliberately skip](#15-non-goals--what-we-deliberately-skip)
16. [Key decisions (resolved)](#16-key-decisions-resolved)
17. [Glossary](#17-glossary)

---

## 1. Can it be like Salesforce but simpler? Yes

Salesforce's superpower is that **standard objects and custom objects run on the same
platform**. An Account and a "Boat Rental" object share the same field types, the same
layout engine, the same permission model, the same API shape. Nothing is special-cased.

Their weakness is that learning it is a part-time job: record types, page layouts,
profiles + permission sets + sharing rules + OWD, validation rules vs. flows, etc.

**Our bet:** keep the "one engine, all objects equal" core, and cut the enterprise
ceremony. The user sees *one* way to define an object, *one* way to add a field, *one*
way to set who can see it. That's the "they'll love it" part.

### The key architectural decision (this is what makes it *simpler*)

We make objects equal at the **platform layer** (API, UI, permissions, AI, automation)
while staying pragmatic at the **storage layer**:

- **Contact / Deal / Company keep their real typed tables.** They have foreign keys,
  indexes, and a `vector(768)` embedding. Those are assets, not debt — we don't throw
  them away.
- **Custom objects keep the generic `data JSONB` store.** Infinitely flexible, zero
  migrations to add a field.
- **A new Object Registry sits on top and makes them indistinguishable to everything
  above storage.** The frontend, AI, automation, and the permission checker all talk to
  the registry — they never know (or care) whether an object is backed by a typed table
  or a JSONB blob.

```
                        ┌─────────────────────────────┐
   Frontend / AI /      │      OBJECT REGISTRY         │   ← everything is "equal" here
   Automation / Perms ─▶│  defs · fields · relations   │
                        │  views · permissions · search│
                        └──────────────┬──────────────┘
                          ┌────────────┴────────────┐
                  ┌───────▼────────┐       ┌─────────▼─────────┐
                  │  TYPED TABLES  │       │  GENERIC RECORDS  │
                  │ contacts/deals │       │ custom_object_*   │
                  │ companies      │       │ (data JSONB)      │
                  └────────────────┘       └───────────────────┘
```

This is the "simpler than Salesforce" sweet spot: **equal where users feel it, pragmatic
where the database feels it.** The purist "everything in one generic EAV table" approach
is rejected — see [§15 Non-Goals](#15-non-goals--what-we-deliberately-skip).

---

## 2. Where we are today (the "weird" part, diagnosed)

There are **two parallel object systems that don't share code**:

| | System objects (Contact/Deal/Company) | Custom objects |
|---|---|---|
| Defined in | Hardcoded Go structs, `models.go:108-186` | `custom_object_defs` table |
| Extra fields | `custom_fields JSONB` bag | `data JSONB` bag |
| Field schema | **One shared blob** in `org_settings.custom_field_defs`, keyed by `entity_type` | **Per-object** `custom_object_defs.fields` |
| Validation | Types + required checked (`org_settings_usecase.go:208`) | **None** — `data` stored as-is (`custom_object_usecase.go:185`) |
| Relationships | Real FKs (contact→company, deal→contact…) | Only `contact_id` + `deal_id`, hardcoded (`models.go:287`) |
| Tags | Contacts only (`contact_tags`) | Not taggable |
| Embeddings / search | Contacts have `vector(768)` + fulltext GIN | None |
| Managed in UI | `CustomFieldManager.tsx` | `ObjectDefManager.tsx` (different screen) |
| Tenant isolation | App-layer `WHERE org_id = ?` in each repo | Same — per-handler, easy to forget |

**Concrete symptoms:**

1. **Validation is backwards.** The flexible system (custom objects) is the *less* safe
   one — it stores any JSON with no type or required-field checking.
2. **Custom objects are second-class.** They can't relate to a Company or to each other,
   can't be tagged, and are invisible to AI/semantic search.
3. **Field defs are concurrency-unsafe.** All system-object fields live in one JSONB blob
   (`saveDefs` rewrites the whole array, `org_settings_usecase.go:340`); two admins
   editing fields race and lose updates.
4. **Stale denormalization.** `display_name` is "the first text field, captured at write
   time" (`custom_object_usecase.go:304`) — reorder fields and it rots.
5. **Two of everything to maintain.** Every cross-cutting feature (import, search,
   automation, AI context) has to be built twice or only works for one stack.
6. **Tenant isolation is a discipline, not a guarantee.** RLS is *enabled* but has **no
   policies** (migration `000008` — isolation is enforced by `WHERE org_id = ?` in every
   repo). One forgotten clause in a new handler = a cross-tenant leak. (This is the
   strongest argument for a single `RecordService`.)

---

## 3. Target architecture — the Object Registry

### 3.1 One descriptor for every object

Promote `custom_object_defs` into a general **`object_defs`** registry that also contains
seeded rows for the three system objects. System objects are *registered*, not rebuilt —
they keep their tables; the registry just describes them.

Key columns (full DDL in [§4](#4-concrete-data-model-ddl)):

| Column | Meaning |
|---|---|
| `slug` | `contact`, `deal`, `company`, or a custom slug |
| `is_system` | `true` for Contact/Deal/Company (cannot be deleted, slug locked) |
| `storage` | `'table'` (typed) or `'jsonb'` (generic) — **internal only**, never user-visible |
| `record_table` | `'contacts'` / `'deals'` / `'companies'`; `NULL` for custom |
| `display_field_id` | which field renders as the record title (replaces the fragile "first text field" heuristic) |

### 3.2 One field model for every object

A single `object_fields` table replaces **both** `org_settings.custom_field_defs` *and*
the per-def `fields` JSONB. Each field knows how it is physically stored:

- **System field** (e.g. `Deal.value`) → `storage_kind='column'`, `maps_to_column='value'`.
- **Custom field on a system object** (e.g. `Contact.shoe_size`) → `storage_kind='jsonb'`,
  stored under that row's existing `custom_fields` blob.
- **Custom object field** → `storage_kind='jsonb'`, stored under `data`.

One validator, one coercer, one field picker — used everywhere.

### 3.3 One relationship model (unlocks Salesforce-grade modeling)

A polymorphic **`object_links`** table lets *any* record relate to *any* record — mirrors
the shape your `record_shares` table already uses (`record_type` + `record_id`), so it'll
feel native. System-object FKs (deal→contact, contact→company) **stay** as real columns
for integrity and speed; `object_links` is *additive* for everything the rigid FKs can't
express (custom↔custom, custom↔company, many-to-many). **Tags become just another link
target**, so Deals/Companies/custom objects become taggable with zero new tables.

### 3.4 One read/write service

A `RecordService` with a uniform interface:

```
List(orgID, slug, filter)     Get(orgID, slug, id)
Create(orgID, slug, payload)  Update(orgID, slug, id, payload)  Delete(orgID, slug, id)
```

Internally it dispatches on `storage`: typed objects route to the existing
contact/deal/company repos; JSONB objects route to the generic record repo. Callers (HTTP
handlers, AI, automation) only ever see "objects." **Org-scoping, validation, FLS, and
audit all live in this one chokepoint** — so they can't be forgotten.

---

## 4. Concrete data model (DDL)

> All new migrations start at `000015`. Every table is org-scoped, soft-deletable where it
> holds user data, and ships with `ENABLE ROW LEVEL SECURITY` in the same migration (to
> match the existing external-access posture from migrations `000008`/`000013`). Tenant
> isolation remains app-enforced via `RecordService`.

### 4.1 `object_defs` (registry) — migration `000015`

```sql
CREATE TABLE object_defs (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug             VARCHAR(100) NOT NULL,
    label            VARCHAR(255) NOT NULL,
    label_plural     VARCHAR(255) NOT NULL,
    icon             VARCHAR(50)  DEFAULT '📦',
    color            VARCHAR(20)  DEFAULT '#6B7280',
    is_system        BOOLEAN NOT NULL DEFAULT FALSE,   -- contact/deal/company
    storage          VARCHAR(10) NOT NULL DEFAULT 'jsonb', -- 'table' | 'jsonb'
    record_table     VARCHAR(63),                      -- 'contacts'… for system, else NULL
    display_field_id UUID,                             -- FK added after object_fields exists
    searchable       BOOLEAN NOT NULL DEFAULT FALSE,   -- opt-in embeddings/fulltext (P6)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX uix_object_defs_org_slug
    ON object_defs(org_id, slug) WHERE deleted_at IS NULL;
ALTER TABLE object_defs ENABLE ROW LEVEL SECURITY;
```

Seed (per org, idempotent): three `is_system=true` rows for `contact` / `deal` /
`company` with `storage='table'` and the matching `record_table`.

### 4.2 `object_fields` — migration `000015`

```sql
CREATE TABLE object_fields (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_def_id  UUID NOT NULL REFERENCES object_defs(id) ON DELETE CASCADE,
    key            VARCHAR(100) NOT NULL,
    label          VARCHAR(255) NOT NULL,
    type           VARCHAR(30)  NOT NULL,   -- text|number|date|select|boolean|url|relation
    options        JSONB DEFAULT '[]',      -- for select
    target_slug    VARCHAR(100),            -- for type='relation' → object_defs.slug
    is_required    BOOLEAN NOT NULL DEFAULT FALSE,
    is_unique      BOOLEAN NOT NULL DEFAULT FALSE,
    is_system      BOOLEAN NOT NULL DEFAULT FALSE,  -- native column, label-editable only
    storage_kind   VARCHAR(10) NOT NULL DEFAULT 'jsonb', -- 'column' | 'jsonb'
    maps_to_column VARCHAR(63),             -- when storage_kind='column'
    position       INT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX uix_object_fields_def_key
    ON object_fields(object_def_id, key) WHERE deleted_at IS NULL;
ALTER TABLE object_fields ENABLE ROW LEVEL SECURITY;
-- deferred FK now that the table exists:
ALTER TABLE object_defs
    ADD CONSTRAINT fk_object_defs_display_field
    FOREIGN KEY (display_field_id) REFERENCES object_fields(id) ON DELETE SET NULL;
```

> **`is_unique` (D3):** enforced immediately for `storage_kind='column'` fields (the DB
> already does it); JSONB uniqueness is modeled now but deferred to an optional later
> slice. See [§10 R4](#10-risk-register--hard-problems).

### 4.3 `object_links` (universal relationships) — migration `000016`

```sql
CREATE TABLE object_links (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    from_slug    VARCHAR(100) NOT NULL,
    from_id      UUID NOT NULL,
    to_slug      VARCHAR(100) NOT NULL,
    to_id        UUID NOT NULL,
    relation_key VARCHAR(100) NOT NULL,    -- e.g. 'account', 'tags'
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX idx_object_links_from ON object_links(org_id, from_slug, from_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_object_links_to   ON object_links(org_id, to_slug, to_id)     WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX uix_object_links_unique
    ON object_links(org_id, from_slug, from_id, relation_key, to_slug, to_id)
    WHERE deleted_at IS NULL;
ALTER TABLE object_links ENABLE ROW LEVEL SECURITY;
```

> **No DB-level FK on `from_id`/`to_id`** (polymorphic). Referential integrity is
> app-enforced in `RecordService.Delete` (cascade-soft-delete links touching the record).
> See [§10 risk R3](#10-risk-register--hard-problems).

### 4.4 Permissions & audit — migrations `000017` (P5a) + `000017b` (P5b)

> `object_permissions` + `object_audit` ship in P5a; `field_permissions` in P5b (opt-in).

```sql
CREATE TABLE object_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    object_def_id UUID NOT NULL REFERENCES object_defs(id) ON DELETE CASCADE,
    can_read   BOOLEAN NOT NULL DEFAULT FALSE,
    can_create BOOLEAN NOT NULL DEFAULT FALSE,
    can_edit   BOOLEAN NOT NULL DEFAULT FALSE,
    can_delete BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (role_id, object_def_id)
);

CREATE TABLE field_permissions (
    role_id  UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    field_id UUID NOT NULL REFERENCES object_fields(id) ON DELETE CASCADE,
    level    VARCHAR(10) NOT NULL DEFAULT 'edit',   -- hidden | read | edit
    PRIMARY KEY (role_id, field_id)
);

CREATE TABLE object_audit (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    record_id   UUID NOT NULL,
    actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
    action      VARCHAR(20) NOT NULL,   -- create | update | delete
    changes     JSONB DEFAULT '{}',     -- { field: {old, new} }
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_object_audit_record ON object_audit(org_id, object_slug, record_id);
ALTER TABLE object_permissions ENABLE ROW LEVEL SECURITY;
ALTER TABLE field_permissions  ENABLE ROW LEVEL SECURITY;
ALTER TABLE object_audit       ENABLE ROW LEVEL SECURITY;
```

> **Default-deny:** absence of an `object_permissions` row = no access. A new object is
> invisible until a role is granted.

### 4.5 Generic search — migration `000018` (P6)

```sql
CREATE TABLE record_embeddings (
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    object_slug VARCHAR(100) NOT NULL,
    record_id   UUID NOT NULL,
    embedding   vector(768),
    content     TEXT,                   -- the text that was embedded (for fulltext too)
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (org_id, object_slug, record_id)
);
CREATE INDEX idx_record_embeddings_fts
    ON record_embeddings USING GIN (to_tsvector('simple', coalesce(content,'')));
-- ivfflat/hnsw index on embedding added once row counts justify it
ALTER TABLE record_embeddings ENABLE ROW LEVEL SECURITY;
```

---

## 5. How a request flows (worked examples)

### 5.1 Read: `GET /api/objects/deal/records/:id`

1. Handler → `RecordService.Get(orgID, "deal", id)`.
2. Service loads the `object_defs` descriptor (cached). Sees `storage='table'`,
   `record_table='deals'` → routes to the existing `deal_repository` (keeps preloads,
   FKs, fast indexes).
3. Service loads `object_fields` for the def, applies **FLS**: strips any field whose
   `field_permissions.level='hidden'` for the caller's role.
4. Returns a uniform `{ id, object: "deal", display, fields: {...}, links: [...] }` shape —
   identical to what a custom object returns.

### 5.2 Write: `PATCH /api/objects/project/records/:id` (a custom object)

1. Handler → `RecordService.Update(orgID, "project", id, payload)`.
2. Service loads descriptor: `storage='jsonb'`.
3. **Validate** every field in `payload` against `object_fields` (type, required, select
   options, uniqueness) using the shared validator. Reject on first error (same error
   shape as system objects).
4. **FLS write-guard:** reject any field the role only has `read`/`hidden` on.
5. Split the payload by `storage_kind`: `column` fields → typed columns;
   `jsonb` fields → merge into `data`. (For a custom object, all are `jsonb`.)
6. Recompute `display` from `display_field_id` (no more "first text field" guessing).
7. Persist, write an `object_audit` row with the field-level diff, sync `object_links`
   for any `relation` fields, enqueue re-embedding if `searchable`.

The caller wrote to a "project" exactly the way it would write to a "deal." That is *all
objects equal*.

---

## 6. API surface

Uniform, object-agnostic routes (existing per-object routes stay until P7 retires them):

| Method & path | Purpose |
|---|---|
| `GET /api/objects` | List all object defs (system + custom) with icons, counts, permissions |
| `POST /api/objects` | Create a custom object def |
| `GET /api/objects/:slug/schema` | Full descriptor: fields, types, relations, display field, the caller's effective permissions |
| `PATCH /api/objects/:slug` | Edit label/icon/fields (system objects: label/fields only, slug locked) |
| `DELETE /api/objects/:slug` | Soft-delete a custom object (403 for system) |
| `GET /api/objects/:slug/records` | List records (filter, sort, paginate) |
| `POST /api/objects/:slug/records` | Create record |
| `GET /api/objects/:slug/records/:id` | Get record (+ links) |
| `PATCH /api/objects/:slug/records/:id` | Update record |
| `DELETE /api/objects/:slug/records/:id` | Soft-delete record |
| `POST /api/objects/:slug/records/:id/links` | Relate to another record |
| `DELETE /api/links/:id` | Remove a relationship |

**Sample `GET /api/objects/deal/schema` response:**

```json
{
  "slug": "deal", "label": "Deal", "label_plural": "Deals", "icon": "💰",
  "is_system": true, "display_field": "title",
  "permissions": { "read": true, "create": true, "edit": true, "delete": false },
  "fields": [
    { "key": "title", "label": "Title", "type": "text", "is_system": true, "required": true },
    { "key": "value", "label": "Amount", "type": "number", "is_system": true },
    { "key": "stage", "label": "Stage", "type": "relation", "target_slug": "stage" },
    { "key": "renewal_risk", "label": "Renewal Risk", "type": "select",
      "options": ["low","med","high"], "is_system": false }
  ]
}
```

The frontend renders **any** object from this one shape: one `ObjectListView`, one
`ObjectDetailView`, one `ObjectForm`.

---

## 7. Security model — equal *and* safe

> Security is a first-class requirement, not an afterthought. Making objects equal must
> not widen the attack surface. Rule: **every object goes through the same guard rails,
> and the guard rails default to closed.**

### 7.1 The honest current state

- **Tenant isolation is application-enforced**, not RLS-enforced. RLS is *on* but has **no
  policies** (migration `000008` explicitly notes the backend uses a trusted service-role
  connection; isolation is `WHERE org_id = ?` in each repo). RLS only closes direct
  external (PostgREST/Supabase) access.
- **Implication:** isolation today depends on every developer remembering to scope every
  query. That's the #1 multi-tenant risk in the codebase.

### 7.2 The biggest security win is structural

Funnelling all reads/writes through `RecordService` makes org-scoping **impossible to
forget** — it's applied once, centrally, instead of in N hand-written handlers. This
single change does more for tenant safety than any new table.

### 7.3 What we add (deliberately minimal — Salesforce minus the matrix sprawl)

| Layer | Mechanism | Enforced where |
|---|---|---|
| **Tenant** | `org_id` scoping (existing) + RLS-on (existing) | `RecordService`, centrally |
| **Object-Level Security (OLS)** | `object_permissions(role × object)` — read/create/edit/delete | `RecordService` entry |
| **Field-Level Security (FLS)** *(opt-in, P5b)* | `field_permissions(role × field)` — hidden/read/edit | `RecordService` (strip on read, reject on write) |
| **Record sharing** | existing polymorphic `record_shares` — now used for *all* objects | read/write checks |
| **Validation** | shared server-side validator on *every* write | `RecordService` before persist |
| **Audit** | append-only `object_audit` with field-level diffs | `RecordService` after persist |

### 7.4 Non-negotiables baked into every phase

- **Default-deny.** New object/field invisible until explicitly granted.
- **Server-side only.** The client is never trusted; FLS strips fields from the JSON
  *response*, not just the UI. This closes the classic "sensitive field leaks via the raw
  API" hole.
- **Validate before persist, strip before serialize.** Both run inside `RecordService`.
- **No raw user input in SQL.** Parameterized queries / GORM throughout; `slug` and field
  `key` validated against `^[a-z][a-z0-9_]{0,49}$` (already enforced for custom objects,
  `custom_object_usecase.go:28`).
- **System objects are protected.** `is_system` defs can't be deleted and their slug/native
  fields can't be removed — only relabeled and extended.

---

## 8. Why users will love it

- **One mental model.** "Everything is an object. Objects have fields. Fields have types.
  Objects relate to objects." That's the whole learning curve.
- **Custom objects stop feeling like a downgrade.** Your "Properties" / "Vehicles" /
  "Contracts" get the *same* polished list view, detail page, kanban, tagging, search, and
  AI as Contacts.
- **One consistent UI everywhere.** A single set of components renders any object from its
  descriptor. Build the experience once; every object — including ones invented next year —
  inherits it.
- **Relate anything to anything.** "This Contract belongs to this Company and covers these
  three Assets" becomes expressible. Today it isn't.
- **The AI gets smarter for free.** Once custom objects have embeddings + fulltext (P6),
  "find contracts expiring next month near the Dallas accounts" works across custom data.
- **No surprises.** Same validation messages, permissions, and flows on every object.
  Consistency *is* the delight.

---

## 9. Delivery plan (phased)

> Format per item: **What's broken → What we want → Fix → Files → Definition of Done →
> Effort.** Same shape as `workflow_builder_improvement_plan.md`. Each phase ships and is
> useful on its own.
>
> **Committed scope (D1):** full convergence P1–P7, with a hard **MVP cut line after P3** —
> the "Objects Are Equal" release, where every object already shares one engine, one API,
> and one UI. P4–P7 deepen it.

### P1 — Close the validation gap *(safety first, no schema change)*

- **What's broken:** Custom-object records store arbitrary JSON with no validation
  (`custom_object_usecase.go:185`). The flexible system is the unsafe one.
- **What we want:** Every record write — system or custom — validated against its field
  definitions, server-side.
- **Fix:** Extract `validateFieldValue` + required-field logic from
  `org_settings_usecase.go:208-320` into a shared `internal/fieldvalidate` package; call it
  from `custom_object_usecase.CreateRecord`/`UpdateRecord`.
- **Files:** new `internal/fieldvalidate/*.go`; edit `custom_object_usecase.go`,
  `org_settings_usecase.go`.
- **Checklist:**
  - [x] Create `internal/fieldvalidate` package; move `validateFieldValue` + required-field logic out of `org_settings_usecase.go`
  - [x] Repoint `ValidateCustomFields` at the new package (no behavior change for system objects)
  - [x] Call the validator in `custom_object_usecase.CreateRecord` / `UpdateRecord` against `def.Fields`
  - [x] Unit tests: text/url/number/boolean/date/select, required, unknown-key passthrough
  - [x] `go build ./... && go vet ./...` clean
- **Definition of Done:** a custom-object record with a wrong-typed / missing-required /
  invalid-select value is rejected with a 400 matching the system-object error shape; unit
  tests cover each field type; `go build ./... && go vet ./...` clean.
- **Effort:** Small (1 day). **Security value: high.**

### P2 — The Object Registry (read-only first)

- **What's broken:** Two stacks, two descriptions; nothing enumerates "all objects."
- **What we want:** One registry listing Contact/Deal/Company *and* custom objects with
  identical descriptors.
- **Fix:** Migration `000015` (`object_defs` + `object_fields` + seed system rows).
  `GET /api/objects` and `GET /api/objects/:slug/schema`. System objects map their
  descriptor onto existing columns via `maps_to_column`. **No data migration of records yet.**
- **Files:** migration `000015_object_registry.{up,down}.sql`; new
  `internal/repository/object_registry_repository.go`,
  `internal/usecase/object_registry_usecase.go`,
  `internal/delivery/http/object_handler.go`; seed in `scripts/`.
- **Checklist:**
  - [x] Migration `000015`: `object_defs` + `object_fields` (+ deferred display-field FK) with RLS enabled, plus `.down`
  - [x] Idempotent seed: 3 system defs (`storage='table'`) + their `object_fields` mapped to real columns (`EnsureSystemObjects`, ensure-on-read; covers existing + future orgs; concurrency-safe via a per-org `pg_advisory_xact_lock` + re-check)
  - [x] `object_registry_repository.go` — list defs, load schema
  - [x] `object_registry_usecase.go` — assemble descriptor (system columns + custom fields merged; custom objects read live from `custom_object_defs`, no duplication)
  - [x] `object_handler.go` + routes — mounted at `GET /api/registry/objects`, `GET /api/registry/objects/:slug/schema` (see note)
  - [x] Tests: schema for a deal and a custom object (5 usecase unit tests, no Docker); `.down` drops cleanly + up/down/up round-trip, re-run seed is a no-op, concurrent first-reads seed once (3 repository integration tests, Docker-gated)
  - > **Route note:** the plan's literal `GET /api/objects` is already occupied by the live custom-object handler (the sidebar's `listCustomObjects`). To stay strictly additive in a backend-only P2 (frontend convergence is P3), the registry mounts under `/api/registry/objects`; it is promoted to `/api/objects` in P7 when the old paths retire.
  - > **Verification note:** build + vet + the 5 usecase unit tests are green. The 3 integration tests were executed against a real Postgres 16 (the up→down→up round-trip, seed idempotency, and the 8-goroutine concurrency path all pass). On hosts where testcontainers can't run (Docker Desktop on Windows), set `TEST_DATABASE_URL` to a Postgres DSN and the tests use it directly; otherwise they fall back to testcontainers and skip when Docker is unavailable.
- **Definition of Done:** `GET /api/objects` returns 3 system + N custom defs; schema
  endpoint returns correct field lists for a deal and a custom object; idempotent seed;
  `.down` drops cleanly.
- **Effort:** Medium (2–3 days).

### P3 — Unified RecordService + one frontend renderer

- **What's broken:** Separate handlers/UI per object; custom objects get a worse UX.
- **What we want:** One service and one set of React views rendering *any* object from its
  descriptor.
- **Fix:** `internal/usecase/record_service.go` dispatching on `storage`. New
  `features/objects/ObjectListView|ObjectDetailView|ObjectForm` driven by the P2 schema.
  Migrate custom-object pages first (lowest risk), then point Contacts/Deals/Companies at
  the shared components **behind a feature flag**, incrementally.
- **Files:** `record_service.go`; frontend `features/objects/*`; retire duplication in
  `CustomObjectPage.tsx`, `ObjectDefManager.tsx`, `CustomFieldManager.tsx`.
- **Checklist:**
  - [x] `record_service.go` — List/Get/Create/Update/Delete dispatching on `storage` (system precedence over a custom slug, matching `GetSchema`)
  - [x] Route typed objects to existing contact/deal/company usecases via per-slug adapters (`record_service_system.go`); JSONB to the custom-object usecase. **No typed-repo changes** (R1)
  - [x] Wire P1 validation into the write path: custom objects via `customObjUC`; system objects' non-native (`custom_fields`) subset via `orgSettingsUC.ValidateCustomFields`; native columns via adapter coercion. Display recomputed on write — system objects use each object's natural label, custom objects recompute `display_name`; the registry `display_field_id` is **not** yet resolved inside `RecordService` (that part of R8 is deferred — see the deferred note)
  - [x] Frontend `features/objects/`: `ObjectListView`, `ObjectDetailView`, `ObjectForm`, all driven by `GET /api/registry/objects/:slug/schema`
  - [x] Migrate custom-object page to the shared components first (`CustomObjectPage` is now a thin wrapper)
  - [x] Put Contacts/Deals behind `objects.unified_read` flag (`src/lib/flags.ts`) with fallback to the legacy pages (default OFF). Companies have no standalone page; covered as a relation target
  - [x] Tests: deal + custom object render through the same `ObjectListView` (+ a flag-fallback test pinning the legacy↔unified branch on both Contacts and Deals); 17 backend unit/route tests; `npx vitest run` (577) + `npx tsc -b` clean; `go build`/`go vet` clean
  - > **Pagination note:** the uniform list is cursor-based and opaque — system objects pass through the typed repos' keyset cursor; custom objects encode the offset in the cursor (`off:N`). One API shape, **zero typed-repo changes**.
  - > **Routes note:** uniform records mount at `/api/registry/objects/:slug/records` (PATCH for update), additive alongside the legacy `/api/objects` + `/api/contacts|deals|companies`; promoted to `/api/objects` in P7. A gin route-registration test guards the tree shape.
  - > **Automation note:** the uniform write path fires `slug_created`/`slug_updated` for **custom** objects (payload identical to the legacy `customObjHandler`), so existing custom-object workflows keep firing after the UI move. System-object automation (`contact_created`, deal stage-change side-effects + triggers) stays on the legacy pages — the default — until the workflow engine cuts over in **P7**.
  - > **Deferred to later phases:** relation values render as raw UUIDs in list/detail (resolved labels = P4 `object_links`); custom-object contact/deal *linking UI* is dropped (the columns/data are untouched; relationships return as first-class in P4); system-object relation *clear*-to-null isn't expressible via the partial-update path; record **display titles aren't yet resolved from `display_field_id`** inside `RecordService` — system objects use a hardcoded per-object label and custom objects still use the first-text-field `display_name` heuristic, so the full R8 fix is deferred.
  - > **Verification note:** build + vet + all backend tests green; `tsc -b` + 577 frontend tests green. No live browser walkthrough yet — the unified renderer only shows real data behind the full local stack (Postgres + backend + seeded login).
  - > **Post-verification hardening (re-verify pass):** two risks surfaced while re-verifying P3 were closed: (1) `SetEventEmitter` is now part of the `domain.RecordService` interface — it was previously reached via a type assertion in `main.go` that would silently no-op on a signature drift, disabling custom-object automation with no build/runtime error; (2) the deal/contact adapters now thread `owner_user_id` into the typed create/update input — it was a declared-native key that was silently dropped, so an owner sent through the uniform endpoint vanished. Added a custom-object `*_updated` event test and a deal owner-mapping test. (The value/probability "default-to-0" concern was checked and is already identical to the legacy handler — no change made.)
- **Definition of Done:** a custom object and a Deal both render through the same
  components; flag flip falls back to old pages with no data change; vitest + `tsc -b`
  clean. ✅
- **Effort:** Large (1–1.5 weeks).

### P4 — Universal relationships + tags

- **What's broken:** Custom records link only to contact/deal; only contacts are taggable.
- **What we want:** Any object relates to any object; everything is taggable.
- **Fix:** Migration `000016` (`object_links`). Relation modeled as a field `type:'relation'`
  with `target_slug` + cardinality. **Tags (D4):** non-contact objects tag via `object_links`;
  contacts keep `contact_tags`; `RecordService` exposes tags **uniformly via one adapter** so
  the API is identical for every object (workflow engine cut over in P7).
- **Files:** migration `000016`; `record_service.go` (link CRUD + cascade soft-delete);
  frontend relation picker (reuse the workflow `FieldPicker` pattern).
- **Checklist:**
  - [ ] Migration `000016`: `object_links` (both-direction indexes + unique edge) with RLS, plus `.down`
  - [ ] `record_service.go` — link create/delete + cascade-soft-delete links on record delete
  - [ ] `relation` field type with `target_slug` + cardinality
  - [ ] Tag adapter: non-contact via `object_links`, contacts via `contact_tags`, uniform API shape
  - [ ] Frontend relation/tag picker (reuse `FieldPicker`)
  - [ ] Tests: custom↔company link, custom↔custom link, tag a deal, delete cascades links
- **Definition of Done:** a custom "Project" links to a Company and to another custom
  object; a Deal can be tagged; deleting a record soft-deletes its links; round-trips via
  the API.
- **Effort:** Large (1 week).

### P5a — Object-Level Security + audit *(mandatory)*

- **What's broken:** Permissions are coarse; no audit trail.
- **What we want:** Per-role read/create/edit/delete per object, plus a uniform audit log.
- **Fix:** Migration `000017` (`object_permissions`, `object_audit`). Enforce OLS in
  `RecordService` (default-deny on entry). Cache the role→object map per org; bust via the
  existing `bustSchemaCache`. Simple admin toggle grid (role × object).
- **Files:** migration `000017`; `record_service.go` enforcement; `internal/auth/*`;
  frontend permissions grid.
- **Checklist:**
  - [ ] Migration `000017`: `object_permissions` + `object_audit` with RLS, plus `.down`
  - [ ] `record_service.go` — default-deny OLS check on every entry
  - [ ] Write an `object_audit` row (field-level diff) on every create/update/delete
  - [ ] Permission-set cache per org; invalidate via `bustSchemaCache`
  - [ ] Admin role×object toggle grid (frontend)
  - [ ] Tests: role without `deal:read` blocked; audit row per write; live permission change applies without restart
- **Definition of Done:** a role without `deal:read` gets 403/empty on deals; every write
  produces an audit row with a field-level diff; permission changes take effect without
  restart.
- **Effort:** Medium–Large (4–5 days). **Security value: high.**

### P5b — Field-Level Security *(opt-in, simplified)*

- **What's broken:** Sensitive fields (e.g. salary, SSN) can leak via the raw API.
- **What we want:** Mark a field "sensitive" and control which roles can see/edit it —
  enforced server-side, not just hidden in the UI.
- **Fix:** Migration `000017b` (`field_permissions`). In `RecordService`, strip
  hidden/`read`-only fields from responses and reject writes to them. A lightweight
  per-field control ("who can see this field?"), **not** a full role×field matrix.
- **Files:** migration `000017b`; `record_service.go` strip/guard; small UI on the field
  editor.
- **Checklist:**
  - [ ] Migration `000017b`: `field_permissions` with RLS, plus `.down`
  - [ ] `record_service.go` — strip hidden/`read`-only fields from responses; reject writes to them
  - [ ] "Mark field sensitive → who can see it" control on the field editor
  - [ ] Tests: viewer can't see or write a hidden field; zero overhead when unused
- **Definition of Done:** a viewer cannot see a `hidden` field in the API response and gets
  403 writing it; the control is off by default and adds no overhead when unused.
- **Effort:** Medium (3 days). **Security value: very high.**

### P6 — Search & AI for every object

- **What's broken:** Only contacts have embeddings + fulltext; custom objects invisible to
  AI/search.
- **What we want:** Optional embedding + fulltext for any object so AI and global search
  span all data.
- **Fix:** Migration `000018` (`record_embeddings` + GIN). Extend
  `internal/worker/embedding_worker.go` to enqueue any `searchable` object. Feed the
  registry into `internal/ai/knowledge_builder.go` so AI context includes custom objects.
- **Files:** migration `000018`; `embedding_worker.go`; `knowledge_builder.go`; global
  search endpoint.
- **Checklist:**
  - [ ] Migration `000018`: `record_embeddings` + GIN fulltext with RLS, plus `.down`
  - [ ] Extend `embedding_worker.go` to enqueue any object flagged `searchable`
  - [ ] Feed the registry into `knowledge_builder.go` so AI context includes custom objects
  - [ ] Global search endpoint across objects
  - [ ] Tests: marking a custom object `searchable` populates embeddings; semantic + fulltext hit custom records
- **Definition of Done:** marking a custom object `searchable` populates embeddings;
  semantic + fulltext search returns custom records; AI context lists custom objects.
- **Effort:** Medium–Large (1 week).

### P7 — Retire the old paths (cleanup)

- **What's broken:** Dual code paths linger after migration.
- **What we want:** One stack.
- **Fix:** Move system-object field defs out of `org_settings.custom_field_defs` into
  `object_fields` (fixes the concurrency/lost-update risk). Drop hardcoded
  `contact_id`/`deal_id` on custom records once `object_links` is source of truth. Delete
  the duplicate managers.
- **Files:** data-migration script; deletions across backend + frontend.
- **Checklist:**
  - [ ] Backfill `object_fields` from `org_settings.custom_field_defs`; stop writing the blob
  - [ ] Backfill `object_links` from hardcoded `contact_id`/`deal_id`; drop those columns
  - [ ] Cut the workflow engine over to the tag adapter; `contact_tags` becomes optional
  - [ ] Delete duplicate managers (`ObjectDefManager` vs `CustomFieldManager`) and old per-object pages
  - [ ] Remove feature flags
  - [ ] Verify backfill row counts; full build / vet / test green
- **Definition of Done:** `org_settings.custom_field_defs` no longer written; old
  per-object custom-field UI removed; backfill verified; flags removed.
- **Effort:** Medium (3–4 days).

---

## 10. Risk register & hard problems

| # | Risk | Mitigation |
|---|---|---|
| **R1** | **Big-bang regression** on Contacts/Deals/Companies when they move onto the shared renderer/service. | Feature-flag P3; keep old handlers live; migrate custom objects first; never touch record storage until P7. |
| **R2** | **Tag migration breaks automation.** The workflow engine references `contact.tags` and `contact_tags` exists. | **Committed (D4):** non-contact objects tag via `object_links`; contacts keep `contact_tags`; `RecordService` exposes tags uniformly via an adapter; workflow engine cut over in P7, after which `contact_tags` is optional. No automation breakage at any step. |
| **R3** | **`object_links` has no DB foreign keys** (polymorphic) → orphaned links after a record delete. | App-enforced cascade: `RecordService.Delete` soft-deletes links where the record is `from` or `to`. Nightly sweep for hard-deleted orphans. Covered by integration test. |
| **R4** | **`is_unique` on JSONB fields** is not a simple column constraint. | **Committed (D3):** model `is_unique` from P2; enforce it for column-backed fields immediately (free — DB already does it); defer JSONB uniqueness (partial expression index per key) to a later optional slice, on demand. |
| **R5** | **FLS adds per-request permission lookups** → latency. | Cache the role→object/field permission map per org; invalidate via existing `bustSchemaCache`. O(1) after warm. |
| **R6** | **Lost-update on the shared field-def blob** during the transition. | P2 reads from the new tables; writes dual-write until P7 cutover; the blob is retired, not edited concurrently. |
| **R7** | **JSONB query performance** for filters/sorts on custom fields. | GIN index on `data`; push hot/sortable custom fields toward typed columns only if a real bottleneck appears (measure first). |
| **R8** | **Display-name drift.** | Replace the "first text field" heuristic with `display_field_id`; recompute on every write inside `RecordService`. |

---

## 11. Performance & indexing

- **Typed objects stay fast** — we deliberately keep `contacts`/`deals`/`companies` columns
  and their existing indexes (`idx_deals_stage_id`, `idx_contacts_fulltext`, etc.).
- **Registry is cached.** `object_defs` + `object_fields` per org are read on almost every
  request — cache in-process, invalidate via `bustSchemaCache` (already wired for AI schema).
- **`object_links` is double-indexed** (`from` and `to`) so traversal is fast in both
  directions; unique index prevents duplicate edges.
- **JSONB:** GIN on `data` for containment filters; partial expression indexes only for
  fields proven hot.
- **Embeddings (P6):** start without an ANN index; add `ivfflat`/`hnsw` once row counts make
  a sequential scan slow — avoids premature index-build cost.
- **N+1 guard:** record list endpoints batch-load links and relation targets (one query per
  relation_key), never per-row.

---

## 12. Testing & rollout strategy

- **Unit tests** for the shared validator (every field type, required, select options,
  uniqueness) and the storage-dispatch logic.
- **Integration tests** (the automation/DB suite needs Docker Desktop — it silently skips
  without it; a real prod bug once hid behind that skip). Cover: cross-object links, cascade
  soft-delete, FLS strip-on-read, default-deny.
- **Gates:** `go build ./... && go vet ./...` are the real backend gates — **do not run
  `gofmt -w`** (it churns unrelated files). Frontend: `npx vitest run` (note: the `rtk`
  wrapper breaks vitest) + `npx tsc -b`.
- **Feature flags** per phase: `objects.unified_read` (P2/P3), `objects.fls` (P5b),
  `objects.search` (P6). Default off; enable per-org for dogfooding.
- **Migration discipline:** every migration reversible + idempotent (`IF NOT EXISTS`,
  `ADD COLUMN IF NOT EXISTS`) — consistent with the lesson from `000014`. Test `.up` then
  `.down` then `.up` locally before merge.
- **Rollout order:** dogfood org → opt-in beta orgs → default-on → P7 removes old paths.

---

## 13. Migration & safety rules

- **Every migration is reversible** (`.up`/`.down`) and idempotent.
- **No big-bang.** System objects are *registered* in P2 and only physically migrated in P7,
  behind a flag, old path still working.
- **Backfill, then cut over.** `object_links` is populated from existing FKs before any
  hardcoded column is dropped.
- **RLS enabled on every new table** in its creating migration (matching `000008`/`000013`).
- **Local stack note:** when bringing the stack up locally, mind the `org_users` migration
  gotcha; seed via `scripts/seed_local_account.js` (`local_admin@20q.com` / `password123`).

---

## 14. Effort & timeline rollup

| Phase | Theme | Effort | Cumulative |
|---|---|---|---|
| P1 | Validation safety | 1 day | 1 day |
| P2 | Object registry (read) | 2–3 days | ~1 week |
| P3 | RecordService + one renderer | 1–1.5 weeks | ~2.5 weeks |
| P4 | Universal relationships + tags | 1 week | ~3.5 weeks |
| P5a | OLS + audit (mandatory) | 4–5 days | ~4.5 weeks |
| P5b | FLS (opt-in) | 3 days | ~5 weeks |
| P6 | Search & AI for all objects | 1 week | ~6 weeks |
| P7 | Retire old paths | 3–4 days | ~6.5 weeks |

**Dependency graph:**

```
P1 ─▶ P2 ─▶ P3 ║─▶ P4 ─▶ P5a ─▶ P5b ─▶ P6 ─▶ P7
safety registry one║ relate  OLS    FLS    search cleanup
       (read) engine║       +audit (opt-in)
              MVP ──╜ "Objects Are Equal" (one engine / API / UI)
```

- **Earliest value:** P1 alone closes a real security gap in a day.
- **MVP milestone (D1):** after **P3**, every object shares one engine, one API, and one UI
  — the shippable "Objects Are Equal" release; P4–P7 deepen it.
- **De-risked:** nothing destructive until P7; the new path is proven additively first.
- **Fallback only:** if timelines force a cut, **P1 + P4** still kill the two worst symptoms
  (unsafe validation + second-class custom objects) for ~30% of effort — but two stacks
  remain, so this is no longer the target.

---

## 15. Non-Goals — what we deliberately skip

To stay *simpler* than Salesforce, we are **not** building:

- A fully generic EAV store for Contacts/Deals (keep the typed tables — see §1).
- Salesforce's profiles + permission sets + sharing rules + OWD matrix. We ship **two**
  layers (role→object, role→field) plus per-record `record_shares`. That's it.
- Record types / page layouts per record type. One layout per object, driven by field order.
- A formula/rollup field language. Revisit after the core lands.
- Apex-style user code. Automation stays in the existing workflow builder.

**Discipline:** every time we're tempted to add a concept, ask "does a small team *need*
this, or does it just match Salesforce?" If the latter, it goes here in §15.

---

## 16. Key decisions (resolved)

These were the five open questions. Here is the committed direction and the reasoning —
now baked into every section above.

### D1 — Scope: **full convergence (P1–P7), with a hard MVP cut line after P3.**
"All objects equal" is the entire point, and the lite P1+P4 path leaves two stacks — so it
can't be the destination. But we don't wait six weeks for value: **P1–P3 is the MVP
milestone** ("Objects Are Equal"), at which point every object already shares one engine,
one API, and one UI. P4–P7 then layer on relationships, security depth, search, and
cleanup. P1 still ships day one as a standalone security fix.
*Why:* commits to the vision without a big-bang; every phase is usable alone.

### D2 — Naming: **keep `object` in code / API / admin; never show the word "object" to end users.**
The schema already speaks "object" (`custom_object_defs`, `ObjectDefManager`,
`CustomObjectPage`), so renaming would churn for nothing. End users only ever see the
object's own label — "Contacts", "Deals", "Projects" — in navigation and on records. The
word "Object" appears only in the *customization / admin* area, exactly like Salesforce and
HubSpot.
*Why:* consistent internally, jargon-free externally, zero rename risk.

### D3 — Uniqueness (R4): **model `is_unique` from P2; enforce column-backed fields now; defer JSONB uniqueness.**
System fields that need it already have real DB unique constraints (e.g. contact email per
org). For custom JSONB fields, true uniqueness needs a partial expression index per key —
real cost for a rare need. We model it now, enforce the free case now, and revisit JSONB
enforcement only on demand.
*Why:* no wasted schema, no premature complexity.

### D4 — Tags (R2): **unify onto `object_links` behind one API adapter; keep `contact_tags` synced until the workflow engine migrates in P7.**
Equality demands every object be taggable, but the automation engine reads `contact.tags`
today and must not break. So: non-contact objects tag via `object_links` immediately (P4);
contacts keep `contact_tags`; `RecordService` exposes tags identically for all objects via
an adapter; the workflow engine is cut over to read through the adapter in P7, after which
`contact_tags` becomes optional.
*Why:* uniform to every caller now, zero automation breakage, clean end state.

### D5 — Permissions (P5): **OLS + audit mandatory (P5a); FLS opt-in & simplified (P5b).**
Small teams need object-level control first; a full role×field matrix is the kind of
Salesforce sprawl we're avoiding. P5a delivers per-object read/create/edit/delete + audit.
P5b adds field protection as a lightweight "mark a field sensitive → who can see it"
control, not an exhaustive grid — and only where a team turns it on.
*Why:* covers the common case immediately; keeps the rare case simple and optional.

> **Remaining judgment calls** (safe to make during implementation, no blocker): relation
> cardinality UI (one-to-many vs many-to-many default), and whether global search (P6)
> ranks across objects or groups by object. Both are reversible UI choices.

---

## 17. Glossary

| Term | Meaning |
|---|---|
| **Object** | Any entity type — Contact, Deal, Company, or custom. All equal. |
| **System object** | Built-in object backed by a typed table (`is_system=true`). |
| **Custom object** | User-defined object backed by JSONB. |
| **Object Registry** | The metadata layer (`object_defs` + `object_fields`) that makes all objects look identical above storage. |
| **RecordService** | Single read/write service; dispatches table-vs-JSONB internally; the chokepoint for scoping, validation, FLS, audit. |
| **OLS / FLS** | Object-Level / Field-Level Security (role → object, role → field). |
| **object_links** | Polymorphic relationship table connecting any record to any record. |
| **storage / storage_kind** | Internal flag: is this object/field backed by a real column or JSONB. Never user-visible. |

---

*Plan authored for the 20q CRM object-model convergence.*
