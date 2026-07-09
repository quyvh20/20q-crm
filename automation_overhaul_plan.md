# Automation System Overhaul — Phased Plan (A1–A8)

> Status tracker. Each phase is independently deployable (Railway backend first, Pages frontend second), never changes the meaning of existing rows, and never breaks in-flight runs. Automation tables change via engine GORM AutoMigrate (works on prod); any **non-automation** table needs a `main.go` boot guard + matching `migrations/*.up.sql`.
>
> Supersedes `workflow_builder_improvement_plan.md` for net-new work (that doc's P0–P3 shipped; its remaining items are folded into A3 here).

## Why

The current system's engine core is sound (durable DB-backed runs with `FOR UPDATE SKIP LOCKED` + idempotency, version pinning, crash recovery, retry sweeper, P8 author-as-actor authorization) but everything around it is weak:

- **Delays block workers and lose progress.** `DelayExecutor` is a `time.NewTimer` sleep inside one of 5 worker goroutines (up to 30 days); a restart re-runs the full duration from scratch.
- **No time-based triggers.** Only `no_activity_days` (hardcoded contact/deal). No cron schedules, no "3 days before close_date".
- **Two parallel workflow formats.** Flat `Actions` (deprecated, removal scheduled 2026-09-01) and `Steps` tree, dual-written by the frontend and synced by a fragile zustand `subscribe` block.
- **Authorization is inconsistent.** `update_record`/`assign_user` enforce full OLS/FLS/own-scope + audit; `create_task`/`log_activity` do raw INSERTs with no authorizer.
- **Hardcoded objects.** Schema endpoints list contact/deal only (company absent); Run Now, webhook inbound, update-record entity resolution all assume contact/deal.
- **The builder UI is the worst part.** 320px sidebar that hides the palette on selection, emoji icons, native `alert()`/`confirm()`, mobile blocked, dead test-run API, design-system island (`bg-gray-950` hardcodes), the email variable picker offers every entity's tokens regardless of trigger.
- **Missing table stakes:** no email templates, no in-app notifications, no AI assistance.

## Phase overview

| Phase | Name | Layer | Status |
|---|---|---|---|
| A1 | Engine correctness: durable waits, steps-only format, executor authz | BE + small FE | **in progress** |
| A2 | Object generalization: registry-driven schema, uniform events, any-object triggers | BE + light FE | **DONE** (core); a few generalizations folded into A4/later |
| A3 | New React Flow builder + workflows frontend re-platform | FE + small BE | **DONE** (A3.1–A3.6: canvas, config panel, React Query, dry-run overlay, route swap + RunHistory restyle/deep-link). Uncommitted. |
| A4 | Time-based triggers: cron schedules, date-field triggers, wait-until | BE + builder UI | **DONE** (A4.1 schedule backend + A4.2 schedule form + A4.3 date_field + A4.4 wait-until). Uncommitted. |
| A5 | Email templates library + TipTap editor + scoped merge tags | BE + FE | planned |
| A6 | New actions: in-app notifications + inbox, create_record, find/enroll | BE + FE | planned |
| A7 | AI copilot (NL → draft on canvas) + `ai_generate` step action | BE + FE | planned |
| A8 | Cutover & cleanup: drop legacy builder + flat Actions column, mobile pass | Both | planned |

---

## A1 — Engine correctness *(in progress)*

**Problem:** blocking delays, dual format, authz gaps (see Why).

**Fix:**
1. **Durable delays.** Add `wake_at timestamptz` + run status `waiting` to `automation_workflow_runs`. The delay step becomes a first-class case in `executeStepsRecursive` (not an executor — executors can't park a run): persist the absolute deadline in a `waiting` action log + run row atomically via `commitActionAndRun`, return up the stack; the worker is freed in milliseconds. The 30s retry sweeper additionally flips due waiting runs (`UPDATE ... SET status='pending', next_retry_at=now(), wake_at=NULL WHERE status='waiting' AND wake_at <= now() RETURNING id`) — atomic, multi-instance safe; `next_retry_at=now()` makes the existing `SweepRetries` the fallback if the channel push drops. On resume the delay's waiting log completes and execution continues — elapsed time is never lost. Branch pinning (`hasAnyStepExecuted`) switches to a "started" set (success ∪ waiting logs, matched by step ID **and** structural path) so a parked branch can't flip on resume — this also fixes a latent resume bug where path-keyed success logs didn't pin ID-checked branches.
2. **Steps-only canonical writes.** Backend derives the deprecated flat `Actions` from `Steps` (`FlattenStepsToActions`, a Go port of store.ts `flattenSteps`); frontend stops dual-writing and deletes the subscribe sync. Column drop stays scheduled for A8 (2026-09-01).
3. **Executor authz.** `create_task`/`log_activity` executors gain the `domain.RecordAuthorizer`: OLS read check + own-scope on linked contact/deal before INSERT, audit after, denials permanent (non-retryable).

**Files:** `internal/automation/models.go`, `repository.go`, `scheduler.go`, `engine.go`, `executor_task.go`, `executor_log_activity.go`, `handlers.go`; frontend `store.ts`, `types.ts`, `schemas.ts`, `RunHistory.tsx`.

**Done when:** a 7-day-delay workflow parks with a visible "resumes in…" in Run History; backend restart mid-wait doesn't reset the clock; N parked runs leave all workers idle; new workflows save without `actions` in the request; a restricted author's create_task run fails permanently with an audit trail.

---

## A2 — Object generalization

**Problem:** schema endpoints hardcode contact/deal (`handlers.go` `GetSchemaObjects`/`GetSchemaObjectFields`); company is absent; `RecordService.fireEvent` emits only on the custom-object path (system objects emit from legacy handlers; DELETE emits nothing); Run Now, update-record entity resolution, `no_activity_days`, and webhook inbound assume contact/deal; deal→contact hydration is hand-rolled (`loadContactForTrigger`).

**Fix:**
- Inject `domain.ObjectRegistryUseCase` (narrowed `SchemaProvider`: `ListObjects` + `GetSchema`) into `automation.NewHandler` from `main.go` — the domain-level interface respects the automation package's no-usecase-import layering. Rebuild the three schema endpoints on it (same DTOs, contract-compatible; now includes company + relation fields).
- Make `RecordService.fireEvent` emit `{slug}_created/_updated/_deleted` for **all** objects incl. DELETE; remove legacy handler emitters in the same deploy (the minute-bucket idempotency key absorbs the overlap window).
- Generalize `entityKindForTrigger` (run_now.go) and `resolveEntity` (executor_update_record.go) to registry slugs; `no_activity_days` accepts any slug (activity linkage, else updated_at recency); inbound webhook gets configurable target object + field mapping (contact default preserved).
- Replace `loadContactForTrigger` with generic one-hop relation hydration: walk the trigger object's descriptor, follow `relation` fields (`TargetSlug`) + `object_links` (`RecordService.ListLinks` via a narrow injected port), lazily populate `evalCtx.Extra[<slug>]`.

**Done when:** a `company_updated`-triggered workflow with a condition on a related record's field runs green in prod; no double runs for contact/deal writes.

**Progress:**
- **Step 1 (registry-driven schema + company first-class) — DONE.** `company` is now a first-class built-in object in all three schema endpoints (`GetWorkflowSchema`, `GetSchemaObjects`, `GetSchemaObjectFields`) — selectable as a trigger and pickable for conditions/tokens. Introduced a single source of truth (`builtinObjectFieldDefs`/`builtinSchemaEntities` in handlers.go) so the picker and token list can't drift, and both endpoints share it. **Fixed broken paths** whose offered value never matched the event payload key: `contact.owner_id`→`contact.owner_user_id`, `deal.owner_id`→`deal.owner_user_id`, `deal.stage`→`deal.stage_id`; removed never-resolving `contact.created_at`/`contact.company.name`/`deal.created_at`; added `contact.company_id`/`deal.contact_id`/`deal.company_id`. Frontend: the create_task "Contact Owner" assignee default now emits `contact.owner_user_id` (was `contact.owner_id`, which silently produced no assignee), with backward-compat for saved actions. Custom objects/fields continue to enumerate from the registry tables (`object_defs`/`object_fields`). *Deferred:* routing custom enumeration through `ObjectRegistryUseCase.GetSchema` (would add per-viewer FLS filtering) — a behavior-changing refactor left for later.
- **Step 2 (uniform events) — DONE.** Every system object now fires its own `_created`/`_updated`/`_deleted` from the uniform write path via the adapter-emit pattern (`contactAutomationMap`/`companyAutomationMap` mirror the delivery `contactToMap`/`dealToMap` automation shape; `dealAutomationMap` reused). Company had **no** automation wiring before — it's now fully triggerable. Deletes fire for custom objects too. A pure deal stage move fires only `deal_stage_changed` (not also `deal_updated`), matching legacy.
  - **Correction to the original plan:** legacy handler emitters are **kept**, not removed. Legacy-UI writes route through their own usecases (not RecordService), so removing those emitters would break legacy-path automation. The adapters cover the uniform/AI path; the per-minute idempotency key absorbs any single-write overlap. Removing legacy emitters would only be safe once *all* writes route through RecordService (not the case today).
  - Files: `internal/usecase/record_service_system.go` (adapter emit fields + automation maps + fire calls), `record_service.go` (`SetEventEmitter` wiring + custom-delete event). Tests: `record_service_events_test.go` + updated `record_service_test.go`.
- **Step 3 (de-hardcode contact/deal) — core DONE.** `resolveEntity` already resolved `company_*`/custom triggers to their slug, but all non-contact/deal slugs fell through to `executeCustomObject` (JSONB `custom_object_records` path) — so a `company_updated` workflow's `update_record` hit the wrong table. Added a real `executeCompany` (typed `companies` table: native `name`/`industry`/`website` via the shared `handleGenericColumn` + `custom_fields` via `handleCustomField`; no tags, no own-scope since companies have no owner). Wired `case "company"` in the executor switch. Files: `executor_update_record.go`. Tests: `executor_update_company_test.go`.
  - *Scoped deferrals* (enhancements, not correctness — the contact default keeps working): `no_activity_days` custom-object support → folded into **A4** (the scheduler/timer subsystem is rebuilt there); configurable inbound-webhook target object + field mapping → a dedicated later effort (needs config surface + UI); Run Now against company/custom sample records → later (needs `RunNowRequest`/EntityPicker generalization; automatic triggering already works for all objects).
- **Step 4 (generic relation hydration) — system-relations DONE.** `buildEvalContext` now hydrates `{{company.*}}` from a deal's/contact's `company_id` (new `loadCompanyForTrigger`), the sibling of the existing deal→contact hydration — so cross-object company tokens/conditions resolve on deal/contact-triggered runs. *Deferred:* the fully generic registry-walking version (follow `ObjectField` relation `TargetSlug` + `object_links` for arbitrary custom-object relations) — worth doing when custom-object relations need hydration; the system relations (the common case) are covered now. Files: `engine.go`.

---

## A3 — New builder (the flagship UI phase)

**Problem:** everything in "Why" about the builder UI.

**Fix:**
- New `src/features/workflows/builder/` on `@xyflow/react` + `@dagrejs/dagre` (top-down auto-layout). Node types: trigger / action / condition (Yes/No edges whose tails rejoin the next sibling — the engine already executes siblings after branches; the old UI just couldn't author it) / delay. `nodesDraggable=false`, `nodesConnectable=false` — structured editing via "+" buttons on edges opening a searchable command popover (replaces the palette; nothing fights for sidebar space). MiniMap, zoom controls, dots background, `fitView`.
- Persistent ~400px right config panel on react-hook-form + zod, porting the 7 action forms and reusing all 10 `panels/inputs/*` typed inputs (the salvageable assets). Design tokens + lucide icons + Radix dialogs/toasts replace the dark island, emoji, and `alert()`/`confirm()`.
- Re-platform data layer on React Query (mirror `src/features/reports/` architecture).
- Rewrite backend `TestRun` as a steps-tree walker (per-step would-run/skip + interpolation previews) and wire it to a dry-run canvas overlay — the API exists today but nothing calls it.
- Route strategy: develop at `/workflows/:id/edit-next`, then swap `/workflows/:id` to the new builder with the old one at `/workflows/:id/legacy` until A8. Safe because after A1 both read/write identical steps JSON.
- RunHistory keeps its UX (the good part) — restyle only, plus deep-link from a run's step log to the canvas node.

**Done when:** `/workflows/:id` serves the new builder; a branching workflow can be built end-to-end without typing a field path; dry-run overlay works.

**Progress** (all uncommitted on `main`):
- **A3.1 / A3.2 — canvas DONE.** `src/features/workflows/builder/`: `graph.ts` (pure steps→React Flow transform on dagre TB auto-layout — condition Yes/No branches rejoin the next sibling; ghost `end` nodes carry insert slots so the first-step add is the same edge-"+" gesture everywhere), `WorkflowCanvas.tsx` (`nodesDraggable`/`nodesConnectable` off, MiniMap/Controls/Dots/fitView), token-styled `nodes.tsx` + `nodeMeta.tsx` (lucide icons, per-object accents), `InsertEdge`/`InsertMenu` (edge "+" opens a searchable command popover — replaces the palette), `BuilderContext`. Store gained the step-tree ops (`addStep`/`updateStep`/`removeStep`/`reorderSteps`/`findStep` + path helpers). Route `/workflows/:id/edit-next` → `NextBuilder.tsx`; public `/builder-demo` harness (temp). Tests: `builder/__tests__/{graph,editing}.test.ts`.
- **A3.3 — config panel DONE.** Because the app is light-only and the legacy dnd-kit builder is a hardcoded-dark island reusing the shared `panels/`+`inputs/` (which must stay intact until A8), the new builder gets its **own** token-styled config layer at `builder/config/`: presentation-only ports (classes→design tokens, chrome SVGs→lucide, logic byte-identical) of all reused inputs (`inputs/` = 11 typed inputs + shared/barrel) + `FieldPicker` + `SmartValueInput` + `TemplateInput`, plus `TriggerConfig`/`ConditionConfig`/`ActionConfig` (6 actions + delay) and the `ConfigPanel` shell (header + delete, routes by `selectedNodeId`), wired into `NextBuilder`. Built + parity-reviewed via multi-agent workflows; `tsc -b` clean, 592 FE tests green. *Note:* the new canvas has no workflow-level global `conditions` node (conditions are authored as If/Else steps); a loaded workflow's legacy global `conditions` round-trips untouched on save but isn't editable here.
- **A3.4 — React Query data layer DONE.** New `features/workflows/queries.ts` (RQ hooks + `workflowKeys`) mirrors the reports feature's use of `@tanstack/react-query`: `useWorkflowsList`/`useWorkflow` queries + `useSaveWorkflow`/`useToggleWorkflow`/`useDeleteWorkflow` mutations (optimistic cache updates + rollback + invalidation). `WorkflowList` (data-only; styling untouched) and `NextBuilder` (load→hydrate store id-gated; save→mutation) migrated. Pure `applyLoadedWorkflow`/`buildSavePayload`/`detachAsDuplicate` extracted from the store so the untouched legacy builder still shares the logic via `store.save`/`loadWorkflow`/`duplicateFrom`. *Schema + object-fields deliberately stay on the store* (org config it already caches + shares with both builders' config panels). List uses `refetchOnMount:'always'` so returning from the still-legacy create/edit path shows fresh data; NextBuilder navigates to an addressable URL after create. `RunNowModal`/`RunHistory` deferred to A3.6.
- **A3.5 — dry-run overlay DONE.** Backend: `engine_dryrun.go` rewrites `TestRun` as a side-effect-free steps-tree walker (`dryRunWorkflow`/`evaluateDryRun`/`dryWalkSteps`) mirroring `executeStepsWithState` — per-step run/skip, the branch a condition takes, and interpolated (incl. nested) param previews; the handler resolves a sample contact/deal server-side (reusing the Run Now loaders) or accepts a raw context. Crucially, the top-level `wf.Conditions` gate is applied ONLY on the legacy flat-actions path — the real engine ignores it for steps workflows (`processRun` returns after the steps block, before the condition check), so gating steps on it would invert the preview. Sample-entity path is gated like Run Now (creator/run_any) since it echoes loaded field values. Frontend: `TestRunResponse` is a per-step tree; `useTestRun`; a token-styled `DryRunDialog` sample picker; `nodes.tsx` tints run/skip + shows the taken branch; `NextBuilder` has a Test button (saved + not-dirty + supported trigger), a dry-run banner, and clears the overlay on structural edits; `ConfigPanel` shows a resolved-values preview.
- **A3.6 — route swap + RunHistory DONE (A3 complete).** `/workflows/:id` now renders the new builder (`NextBuilder`); the legacy dnd-kit builder moved to `/workflows/:id/legacy` (kept until A8, still self-consistent — its post-create nav stays in `/legacy`; NextBuilder has a "Classic editor" escape link). Removed `/edit-next`; NextBuilder gained a `<768px` mobile guard (parity with legacy) + create-nav to `/workflows/:id`. `RunHistory` restyled dark→tokens (presentation-only; JSON syntax + error colors made theme-aware after review flagged washed-out contrast on the now-light code blocks). Deep-link: `store.parseStepPath` (backend `BuildStepPath` format `idx(|branch|idx)*`, decimal-guarded) + NextBuilder `?node=<action_path>` → `getStepAtPath` → select; RunHistory step-log rows get an "Open in builder" button. **Accepted gaps** (new builder vs legacy): no drag-reorder (structured +/delete by design), no in-builder Run Now (available from the list), no workflow-level global-conditions editing (round-trips untouched); deep-link resolves against the current saved version (silent no-op / possible wrong node on structural drift). Verified: tsc clean, 673 FE tests, `/builder-demo` renders the canvas+config panel token-styled with no console errors.

---

## A4 — Time-based triggers

**Fix:**
- New `automation_timers` table (engine AutoMigrate): `kind` `schedule`|`date_field`, `fire_at`, `payload`, unique `(workflow_id, dedupe_key)`, partial index on due pending timers. Scanner cron every 60s under `pg_try_advisory_lock` claims due timers `FOR UPDATE SKIP LOCKED LIMIT 200`, marks fired, calls `TriggerEvent` (run idempotency = second dedupe layer).
- `schedule` trigger (`{cron, timezone}`): on save/toggle upsert next pending timer (robfig cron parser `.Next` in tz); scanner re-arms the next occurrence after each fire; reconciliation pass self-heals missing timers.
- `date_field` trigger (`{object, field, offset_days, at_time, timezone}`): timers materialized event-driven at the A2 emission chokepoint (dedupe_key embeds the date value, so moved dates re-arm and stale timers cancel) + nightly advisory-locked reconciliation scan. Firing scans indexed `fire_at` — O(due), not O(records).
- Wait-until step: `DelayParams` gains `{until_field, offset_days, at_time}` — resolves to an absolute `wake_at` on the A1 machinery (30-day cap kept for fixed delays, lifted for field-based waits).
- Builder UI: schedule trigger form (cron presets + human preview + timezone), date-field trigger form, wait-until variant of the delay form.

**Done when:** "every Monday 9am" and "3 days before deal.close_date" each fire exactly once and survive a redeploy mid-schedule.

**Progress** (uncommitted on `main`):
- **A4.1 — `schedule` trigger backend DONE.** New `automation_timers` table (engine AutoMigrate: unique `(workflow_id, dedupe_key)` + partial index on due pending). Scheduler Job C `scanTimers` every 60s under a **pinned-connection** `pg_try_advisory_lock` (acquire+release on the same `*sql.Conn` so the cluster-global lock can't leak), plus Job D hourly prune of old fired timers. Firing is **create-run-THEN-mark-fired** (`DueTimers` selects without consuming; the scanner fires then `MarkTimerFired`), so a crash in the fire→mark window is retried next scan and the run's occurrence-derived idempotency key makes the retry a no-op — at-least-once fire + idempotent run = exactly-once run, no lost occurrence across a redeploy. `ArmScheduleTimer` (robfig/cron `.Next` in the trigger's tz) is called on create/update/toggle and re-arm/reconcile; it **always deletes the current pending timer first** then upserts the next occurrence, so a cron/timezone edit drops the stale fire and a toggle off→on re-arms cleanly (cancel = DELETE, not tombstone, so `OnConflict DoNothing` can't block re-insert). `reconcileScheduleTimers` self-heals a missing next-occurrence row. `schedule` added to `ValidTriggerTypes` + validator (cron required/parseable, tz loadable). Go: build/vet clean; 7 pure cron tests + 5 Docker-gated timer tests (fire-exactly-once, toggle re-arm, cron-edit-drops-stale) + full automation package green. Multi-agent review found + fixed 3 HIGH (missed-fire, toggle-drop, cron-edit double-fire) + advisory-lock leak + unbounded rows.
- **A4.2 — builder schedule-trigger form DONE.** `Schedule` is now a selectable trigger in the new builder (alongside the object triggers + Webhook in the source dropdown). New pure `cron.ts` (`buildCron`/`parseCron`/`describeCron`/`isValidCron` + tz helpers) is the frontend mirror of the backend's 5-field robfig model — round-trip safe, 32 unit tests. New `builder/config/ScheduleConfig.tsx` renders a friendly **frequency (hourly/daily/weekly/monthly/custom) + time/day + timezone** editor over that model, with a live human preview ("Every Monday at 9:00 AM (America/New_York)") and inline cron validation; monthly day is capped at 28 so every month fires; timezone list from `Intl.supportedValuesOf` (feature-detected via cast, curated fallback), defaulting to the viewer's zone. Wired into `TriggerConfig` (schedule parse/build/hide-fires-on/own-preview); store `validate()` gained a schedule branch (cron required + `isValidCron`, tz string) and `setTrigger` now clears object-scoped conditions when entering/leaving a non-object trigger; `nodeMeta` shows a CalendarClock icon + the cron cadence as the trigger-node label; `TRIGGER_LABELS.schedule = 'Schedule'`. Dry-run/Run-Now stay correctly gated off (schedule has no sample entity → `entityKindForTrigger` null). Verified: `tsc -b` clean, 637 workflows tests green, `/builder-demo` exercises all 5 frequencies + invalid-cron error with no console errors. Frontend-only (backend shipped in A4.1). Uncommitted.
- **A4.3 — `date_field` trigger DONE (backend + builder form).** "Fire N days before/after `<record>.<date field>` at `<time>`", materialized event-driven from record writes (O(due), not O(records)).
  - *Backend* (`internal/automation`): `TriggerDateField`/`TimerKindDateField` consts + `ValidTriggerTypes` + validator (object/field required, offset_days numeric, at_time HH:MM, tz loadable). New `datefield_timers.go`: `computeDateFieldFireAt` (field's calendar date + at_time-in-tz, shifted by offset_days → UTC — the date's day is tz-agnostic, only the time-of-day uses the tz), dedupe key `df:<recordID>:<fireUnix>` (embeds record + fire moment), repo `MaterializeDateFieldTimer` (tx: delete stale-different-key pending for the record, then OnConflict-DoNothing upsert), `CancelDateFieldTimersForRecord`, `ActiveDateFieldWorkflowsForObject`. `Engine.materializeDateFieldTimers` runs on **every** record write (hooked into `TriggerEvent`, independent of the event fan-out since date_field doesn't subscribe to `{slug}_updated`): create/update arm/re-arm, a moved date cancels the stale timer + arms the new one, delete/past/empty cancels. Firing reuses `fireTimerRun` (payload snapshots the record for the run's eval context). Handler cancels date_field timers on deactivate/trigger-change/delete. Fires reuse the existing scanner (Job C) — no new scan loop.
  - *Scoped deferrals (documented):* **pre-existing-record backfill** — activating a date_field workflow only arms records written *after* activation (a full per-object record scan is a follow-up); **config-change re-arm** — already-materialized timers fire with the config captured at materialization (new/edited records use the new config). Both mirror the phase-scoping pattern of A2/A4.1.
  - *Frontend*: "Date reached" is a selectable source trigger. New pure `dateField.ts` (`describeDateField`, offset↔direction split, `fieldPathLabel`) + `builder/config/DateFieldConfig.tsx` — object + date-field pickers (date-type fields read straight from the loaded schema; auto-selects the first object-with-a-date-field + its first date field), a **N days before/on/after** offset control, at_time, timezone, live preview ("3 days before Expected Close at 9:00 AM (tz)"). Wired into `TriggerConfig`; store `validate()` date_field branch (object+field required); `nodeMeta` CalendarDays icon + `describeDateField` node label; `TRIGGER_LABELS.date_field`.
  - Verified: Go build/vet clean, full automation package green incl. Docker-gated date_field tests (materialize-on-event, moved-date re-arm, delete/past cancel, inactive-not-armed, due-fires-exactly-one-run) + 15 pure tests; `tsc -b` clean, 652 workflows FE tests, `/builder-demo` drives the full "3 days before deal.expected_close_at" flow with no console errors. Uncommitted.
- **A4.4 — wait-until delay step DONE (backend + builder form).** A delay step resolves its deadline from a record date field on the run's eval context instead of a fixed duration, parked on the same A1 durable-wait machinery.
  - *Backend* (`internal/automation`): `DelayParams` gains `{until_field, offset_days, at_time, timezone}` + `IsWaitUntil()` (until_field is the discriminator; when set, duration_sec is ignored and the 30-day cap does not apply). New `engine_wait.go` `resolveDelayWakeAt` — for wait-until, `resolvePath(until_field)` off `evalCtx` → `computeDateFieldFireAt` (reuses the A4.3 date/offset/tz math; the field's calendar day is tz-agnostic, only at_time uses the tz); a field resolving empty/unparseable/past yields ok=false → the delay proceeds immediately (a wait "until" a passed moment is trivially satisfied). Both delay shapes persist an absolute `wake_at`, so resume/crash-recovery reads the deadline back from the parked log and never recomputes. `validateDelayParams` (steps path) branches on IsWaitUntil (at_time HH:MM + tz loadable; skip duration checks). `FlattenStepsToActions`/`delayParamsFromMap` carry the wait-until fields.
  - *Frontend*: `ActionConfig.tsx` `DelayParams` gets a "For a duration / Until a date" mode toggle; `WaitUntilFields` = date-field picker + N days before/on/after + at_time + timezone + a live "Wait until …" preview (`describeWaitUntil` in `dateField.ts`). `store.ts` validate/flatten/updateStep carry wait-until; `nodeMeta.delayLabel` renders the wait-until cadence.
  - *Adversarial multi-agent review (4 dimensions) + browser verification on `/builder-demo` found & fixed 3 issues:* **(HIGH, browser-only)** the zod `delayParamsSchema` (`schemas.ts`) required `duration_sec.positive()`, so `validate()` rejected every wait-until delay (duration_sec 0) → **no wait-until workflow could be saved via the steps path**; now branches on until_field (mirrors backend). **(MEDIUM)** the wait-until date-field picker offered date fields from ALL objects, letting a user pick one the trigger's eval context can't resolve → a silent no-op wait; now scoped to trigger-resolvable objects via `resolvableObjectsForTrigger` (mirrors backend `buildEvalContext` hydration: contact→+company, deal→+contact+company, custom→slug, schedule→none) + a `validate()` guard rejecting an unresolvable `until_field`. **(LOW)** the deprecated flat-actions validator (`validateActionParams` case delay) rejected a wait-until actions-only body; now consistent with the steps path.
  - *Scoped deferral (documented):* the A3.5 dry-run overlay shows a wait-until delay's `delay_sec` as 0 (the resolved deadline isn't previewed) — cosmetic only; the `delay_sec` field isn't rendered in the builder (the node label uses `delayLabel`), so no misleading UI. A resolved-deadline dry-run preview is a later polish.
  - Verified: Go build/vet/`-short` tests green (pure `resolveDelayWakeAt`/validate/flatten tests + Docker-gated park/resume/unresolvable-proceeds); `tsc -b` clean, 672 workflows FE tests; `/builder-demo` drives the full flow (deal-trigger offers deal date fields; contact-trigger excludes them; resolvable wait-until validates, unresolvable is flagged, fixed delay unregressed) with no console errors. Uncommitted.

---

## A5 — Email templates

**Fix:**
- `email_templates` table (engine AutoMigrate; org-scoped, soft-delete, unique lower(name) per org): `subject`, `body_html` (canonical send source with `{{merge.tags}}`), `body_json` (TipTap doc for lossless re-edit), optional `object_slug` merge scope, created/updated_by.
- Rendering reuses `template.go InterpolateTemplate` over subject + body_html, wrapped in the `pkg/mailer` branded shell — one interpolation engine everywhere, missing-tag → empty + warning preserved.
- CRUD under `/api/workflows/email-templates` (cap `workflows.manage`) + `POST /:id/test-send` (renders with a sample record, sends to the caller). `send_email` executor gains `template_id` (inline subject/body keeps working); soft-deleted template → permanent failure with a clear error.
- Frontend: templates library tab on `/workflows`; TipTap editor (`@tiptap/react` + starter-kit + custom inline MergeTag node serializing to `{{path}}`); template dropdown in the send_email form; trigger-scoped variable picker replaces the over-offering `TemplateInput` VariablePicker (fixes the contact.*-on-deal-trigger bug structurally).

**Done when:** an email action can pick a library template, test-send, and a live run delivers correctly merged HTML.

---

## A6 — New actions + notifications

**Fix:**
- `notifications` table — **platform table**: main.go boot guard + `migrations/*.up.sql`. No soft-delete (90-day hard-delete sweep). Indexes: inbox `(user_id, org_id, created_at DESC)`, partial unread.
- `NotificationService.Create` inserts + `PUBLISH sse:<orgID>:<userID>` (per-user channel — org-wide would leak payloads to every member; `events.go Stream` subscribes both org and user channels).
- REST: list (cursor, unread filter), mark read, read-all, unread-count. UI: bell + unread badge + Radix popover inbox in `AppLayout` header.
- New executors: `notify_user` (recipient specific|owner_field, per-run cap ~50), `create_record` (through `RecordService.Create` via a narrow port — creation has no multi-op atomicity needs, so uniform validation/authz/events are pure win; P8 actor context applies), `find_records` (registry-validated filters, ≤100 into action output), `enroll_records` (creates runs in a target workflow per record; idempotency includes source run id; enroll depth counter in trigger context, reject >2).

**Done when:** a workflow notifies a record's owner in-app in <2s via SSE, creates a company record, and enrolls matching contacts into another workflow — all authz-enforced.

---

## A7 — AI copilot + AI step

**Fix:**
- `POST /api/workflows/ai/draft` (cap `workflows.manage`): command-center-style tool loop (reuses `gateway.go` + `budget_guard.go`) with workflow-scoped tools `get_workflow_schema` (registry objects/fields/stages/users) and `draft_workflow(name, trigger, conditions, steps)` — handler validates via the existing workflow validator and returns normalized draft JSON. **Never saves**; the client applies through the same zod validation as manual edits.
- Builder UX: "Copilot" tab in the right panel; the draft applies to the canvas immediately with an "AI draft — Keep / Undo" banner (store snapshot undo). The canvas is the preview.
- Command Center gets `create_workflow`/`update_workflow` tools in `tools.go`, gated by `workflows.manage` via `AllowedToolsWithSchema`.
- `ai_generate` executor: `{prompt, output_var, max_tokens≤1024}` → interpolated prompt → `AIGateway` under budget guard → output in `evalCtx.Actions[step.id]` (`{{actions.<id>.text}}`); 429/5xx retryable, 4xx permanent.

**Done when:** "when a deal moves to Negotiation, wait 2 days, then email the owner and create a follow-up task" produces a valid, editable draft on the canvas.

---

## A8 — Cutover & cleanup

- Remove `/legacy` builder + dnd-kit builder code + old panels.
- Execute the scheduled flat-Actions removal: verify no steps-less workflows remain, drop the column, delete `MigrateFlatActionsToSteps`, the legacy flat execution path in `processRun`, and `DelayExecutor`.
- Remove hardcoded builtin field maps in `handlers.go` (old builder gone).
- Mobile pass: canvas read-only + list/history usable below 768px instead of blocked.
- Docs.
