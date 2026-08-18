# Engagement Trigger + Percentage Split — Implementation Plan

*2026-08-18. Planned against a 4-agent code recon of the working tree (post Brevo-redesign,
commit `8f4ec65`). Every file:line below was verified in-repo; re-verify offsets before
patching (main.go especially — it is 3,800 lines and hot).*

Two features, two independent arcs:

- **G (enGagement): `email_opened` trigger** — email opens recorded by the M4 Resend/Svix
  pipeline start automations.
- **S (Split): `split` step kind** — a percentage A/B fork on the canvas.

They share no code. Recommended order: **S first** (pure code, fully testable locally),
then **G** (its true E2E needs the real Resend account). Each arc is two deploys:
backend first, frontend second — both features have a backend that *silently misbehaves*
if the frontend ships first (details in each arc).

---

## Arc G — `email_opened` trigger

### What exists today (recon facts)

- The M4 pipeline is: Svix-signed POST `/api/marketing/webhooks/resend` → **pure enqueue**
  into `marketing_email_events` (org resolved from the From-domain, campaign attributed from
  the echoed `campaign_id` send tag) → a 5s background worker (`ResendProcessor`, launched
  with `context.Background()` at `main.go:3286-3288`) applies side-effects.
  **Opens and clicks currently do NOTHING** — they fall into the `default:` branch of
  `ResendProcessor.apply` (`webhook_processor.go:152-154`), ledger-only.
- `package marketing` already imports `package automation` (8 non-test files); the reverse
  import does not exist and must never (cycle — `marketing_gate.go:16-36` documents this).
  So the processor may call `Engine.TriggerEvent` **directly**; the precedent is
  `sequenceEnroller` (`sequence_feeder.go:39-43`, wired `main.go:3405`). The engine is
  constructed (`main.go:2959`) long before the processor (`:3286`) — wiring is a signature
  change on `NewResendProcessor`.
- Workflow matching is pure data (`trigger->>'type' = ?`, `repository.go:426-433`) — no
  dispatch registry to extend. But `IsValidTriggerType`'s dynamic wildcard accepts only
  `_created/_updated/_deleted/_any` suffixes: **`email_opened` needs an explicit const or
  every save 400s** (`models.go:463-479`, `validator.go:250-256`).
  Do NOT name it `*_updated` — that suffix drags it into the `watch_field`/`changed_fields`
  filter branch (`engine.go:467`).
- Dedupe reality: ledger ingestion is idempotent **per Svix delivery only**
  (UNIQUE `(org_id, svix_id)`). Every pixel load / Apple-MPP prefetch is a *new* event with
  a new svix-id. The engine's default run idempotency key is per-entity-per-**minute**
  (`engine.go:525-533`) — useless against repeated opens across minutes.
- Attribution: sends are tagged `{campaign_id, contact_id}` (`campaign_sender.go:352-355`).
  **For M8 sequence sends the `campaign_id` tag holds the WORKFLOW UUID**, not a campaigns
  row. At ingest only `campaign_id` is parsed into a column; `contact_id` lives only in
  `raw_payload` (readable via the `tag()` helper, `events_models.go:97-135`).
- Coverage: plain transactional `send_email` automation steps are **untagged and sent from
  the global MAIL_FROM** — their webhook events are *dropped at org resolution*
  (`webhook_handlers.go:121-129`). The trigger can only ever see campaign (M7), sequence
  (M8), and 1:1 verified-domain (R9) sends. This is fine for v1 (see scope).
- Analytics already discounts Apple-MPP machine opens with a **10s-after-delivery window**
  (`analytics_repository.go:87-107`); the `(org_id, email_normalized, event_type,
  occurred_at)` index (`main.go:2251-2259`) covers that lookup.
- No existing guard covers the new loop class *trigger → send → recipient opens → trigger*:
  `_internal_update` covers engine record-writes, `_enroll_depth` only bounds
  `enroll_records`, the minute-key only absorbs bursts (`engine.go:387-455, 530-533`).

### Design decisions (locked)

1. **v1 fires for CAMPAIGN opens only.** At emit time, the event's `campaign_id` must
   resolve to a row in `campaigns`; if it's absent (sequence sends carry a workflow UUID)
   or NULL (1:1 sends), skip. This *structurally* kills the send→open→send loop: an
   `email_opened` workflow can send whatever it wants — its own sends are never
   campaign-attributed, so they can't re-trigger. Sequence-open triggering is an explicit
   v2 (needs `_enroll_depth` threading).
2. **Machine-open filter always on (v1):** skip opens arriving ≤10s after the same
   recipient's `email.delivered` for the same campaign — the exact analytics heuristic,
   same index. No param; document it in the trigger's config panel copy.
3. **Idempotency: once per (workflow, contact, message).** Bespoke branch in
   `triggerEventInternal` key-derivation: when the payload carries `email_id`, the run key
   is `sha256(workflowID : eventType : entityID : email_id)` instead of the minute-truncated
   form. Re-opens of the same email are absorbed forever (within the 90-day run-prune
   horizon — acceptable: Resend opens are near-real-time; no durable-claims table needed).
4. **Contact resolution:** prefer the `contact_id` tag from `raw_payload`; fall back to a
   lookup by `email_normalized` within the org; if both fail, skip with a warn log
   (parity with the org-resolution drop policy). Payload contract per `buildEvalContext`
   (`engine.go:1014-1098`): `{contact: <field map>, trigger: {type, campaign_id, email_id},
   entity_id: <contactID>}` — contact under its slug key, `entity_id` mandatory.
5. **Trigger params:** optional `campaign_id` filter ("opened campaign X"; empty = any
   campaign). Filtered in a small bespoke branch next to the `deal_stage_changed` stage
   filter (`engine.go:496-523`), validated in `validateTrigger`'s switch
   (`validator.go:230-432`).
6. **Emission is best-effort from the worker's `apply`**, keyed idempotently (decision 3)
   so at-least-once redelivery is safe. Never return an error from the emit (that would
   newly repend ledger-only events); `TriggerEvent` is fire-and-forget anyway. The
   processor's ctx is already Background-derived — no detach needed, but pass
   `context.Background()` to keep the house idiom uniform.
7. **Wiring:** `NewResendProcessor` gains an emitter seam (func value or 2-method
   interface: `TriggerEvent(...)` + a contact loader), satisfied by `autoEngine` /existing
   loaders, wired at `main.go:3286`. Nil seam ⇒ no emission (house fail-closed style —
   no env flag).
8. **No schema changes.** No new tables, columns, or indexes: idempotency rides the run
   key; the MPP lookup and campaign lookup use existing indexes/PKs. (If measurement later
   wants an "opens that triggered" marker, that's a v2 ledger column via the
   `marketingGuards` boot-guard list, `main.go:1663-1985` — never GORM tags alone.)

### G1 — backend (one deploy)

| # | Change | Where |
|---|--------|-------|
| 1 | `TriggerEmailOpened = "email_opened"` const + `ValidTriggerTypes` entry | `automation/models.go:336-347, 450-458` |
| 2 | `validateTrigger` case: optional `campaign_id` (uuid-or-empty) | `automation/validator.go:230-432` |
| 3 | Idempotency-key branch: `email_id`-keyed run key | `automation/engine.go:525-533` |
| 4 | `campaign_id` trigger-filter branch | `automation/engine.go` next to `:496-523` |
| 5 | `entityKindForTrigger` → `"contact"` (Run Now + Test Run support; otherwise 400 INCOMPATIBLE_ENTITY) | `automation/run_now.go:42-51` |
| 6 | Emitter in `ResendProcessor.apply`: `case ResendTypeOpened:` → campaign-resolve gate → MPP window check → contact resolve → `TriggerEvent` | `marketing/webhook_processor.go:152-154` |
| 7 | Seam + wiring | `marketing/webhook_processor.go` ctor, `cmd/server/main.go:3286` |
| 8 | AI-draft trigger vocabulary mention | `automation/ai_draft.go:364` |

Tests (pure, no Docker needed): key derivation (same email_id absorbed, different email_id
fires), campaign-gate (workflow-UUID and NULL campaign_ids skipped), MPP window, contact
fallback order, emitter-nil safety. Backend gates: `go build ./...` + `go vet` (never
gofmt). One Docker-gated test for the processor→engine integration runs in CI.

**Deploy note:** after G1 alone, the trigger exists but nothing can create it (frontend
can't build the type). That's the intended safe state.

### G2 — frontend (one deploy, after G1 is live)

The recon's exact touch list — the store's validate() branch is the one that *blocks save*
until written (`store.ts:867-894` demands an object slug + fires-on event today):

- `types.ts:151-159` TRIGGER_LABELS; `catalog.ts:97-122` palette item (new **Email**
  category, `MailOpen` icon, rose accent — lucide only, no emoji) + `CATEGORY_ICONS`.
- `TriggerConfig.tsx` pseudo-object, cloned from the schedule/date_field pattern:
  `buildEntityList:37-59`, `parseTrigger:62-85`, `buildTriggerSpec:88-118`,
  `showFiresOn:174-176` exclusion, a small `EmailOpenedConfig` sub-form (campaign dropdown
  from the marketing campaigns list + the machine-open-filter explainer) at `:282-285`.
- `store.ts`: `extractObjectSlug:624-632` → map to `'contact'` (the `no_activity_days`
  precedent) **and** `validate()`'s trigger branch `:827-894` gets an `email_opened` case
  (else "Select a source object"/"fires-on missing" block every save).
- `dateField.ts` — the silent-failure trio: `resolvableObjectsForTrigger:119-159` →
  `{contact, company}` (else every wait-until is rejected and merge-tag pickers go empty),
  `triggerPrimaryObject:167-185` and `triggerOwnerObject:193-196` → `'contact'` (else
  notify_user "record owner" mode hard-fails at save, `store.ts:1114`).
- `RunNowModal.tsx:35-46` `entityKindForTrigger` → `'contact'` (gates BOTH Run Now and the
  builder Test button — mirrors the backend function, must change in lockstep).
- `nodeMeta.tsx` triggerMeta/Label/Title/Description branches (`:129-248`) — today all
  fall through to the raw string; `ConditionConfig.tsx:18-40` deriveFiresOn/deriveObjectSlug;
  `validationMessages.ts:28-63` TERMS.
- Tests: TriggerConfig round-trip, store validate case, dateField trio, RunNowModal.
  (`npx tsc -b` + `npx vitest run`, never rtk-wrapped vitest.)

### G3 — verification

Local: `RESEND_WEBHOOK_SKIP_SIGNATURE=true` (dev/test only) + curl synthetic
`email.opened` payloads through the real handler → processor → engine; assert run
creation, dedupe, campaign gating. Live: the real-Resend constraint from the B3 spike
applies — a true pixel-open E2E needs the production account; Run Now / dry-run cover the
workflow side without it.

### Explicit non-goals (v2 backlog)

**Update 2026-08-18: `email_clicked` SHIPPED** — see the click section below; the
remaining non-goals stand.

Sequence-open/click triggering (+`_enroll_depth` threading); **wait-until-EVENT**
(different machinery: resumable waits keyed on engagement, not a trigger);
tagging transactional automation sends (conflicts with the Guardrail-9
byte-identical-payload rule, `executor_email.go:58-69`); backfill (impossible
anyway — unattributable events were dropped, not stored).

### `email_clicked` as shipped (2026-08-18)

Built after the open trigger, reusing its campaign gate, contact resolution and
per-message run key. Three decisions differ from what this plan originally
assumed:

- **The grace period was KEPT, not dropped.** The plan argued the 75s wait only
  served the open-side machine filter. That holds only if clicks get no machine
  filter — but corporate link scanners (Safe Links, Proofpoint) fetch every URL
  moments after delivery, the click-side twin of Apple MPP. Filtering them needs
  the delivered row, which needs the wait.
- **No URL filter in v1**, so the unverified payload shape blocks nothing. The
  clicked URL still rides in the payload as `{{trigger.link}}`. Dedupe therefore
  stays per-message, which is the right grain without a per-link filter.
- **Unsubscribe exclusion matches by PATH, not origin** (`/u/…`,
  `/api/marketing/u/…`): the link is baked in at send time and mail sits in
  inboxes for weeks, so comparing against the current `FRONTEND_URL` would
  un-recognise every link still in flight after an origin change. When the URL
  cannot be extracted at all, the whole raw payload is scanned for the same
  markers.

Two defects the review caught and the implementation fixed: typing the click
fields would have made webhook ingest strict, silently dropping EVERY click
event (ledger, analytics and the A/B decider included) on any unexpected shape —
they are `json.RawMessage` with tolerance in `clickedLink()`; and the grace
repend consumed a retry attempt, so a waiting event could be reaped as
permanently failed — deferral now gives the attempt back and stops the drain
loop re-claiming parked rows.

## Arc S — `split` percentage step

### What exists today (recon facts)

- **Determinism is not optional.** After any durable delay/retry/crash, `processRun`
  re-walks the whole steps tree from the root and re-derives every branch decision from
  scratch; only success/waiting action logs pin a branch (`engine.go:757-789, 947-973`;
  `engine_wait.go:85-100`). A random-at-execution split would *flip sides mid-run* after a
  branch already produced side effects (a retrying send may have actually sent).
- Unknown step kinds are **silently skipped at runtime** — `executeStepsWithState`
  (`engine.go:826`) and `dryWalkSteps` (`engine_dryrun.go:78`) have no default case. An old
  binary handed a saved split drops the node and its whole subtree without error.
- `FlattenStepsToActions` (`models.go:267-322`) is **security-load-bearing**: its two live
  consumers are `shouldActivate` (refuses auto-activating starter templates that send
  email/webhooks — `system_template_usecase.go:756`) and `workflowHasMarketingSend`
  (`sequence_usecase.go:254`). Missing split-branch recursion = fail-open auto-activation.
- `ParseStepPath` tokenizes only `yes`/`no` as branch labels (`step_path.go:44-78`), and
  the FE mirrors this (`store.ts:222`, `issueMap.ts:94`).
- FE fork machinery: ~10 tree helpers are generic over the `yes_steps`/`no_steps` arrays;
  ~14 sites are hardcoded on `type === 'condition'`; `enforceYesLeft` additionally matches
  the literal edge labels `'Yes'`/`'No'` (full inventory below).

### Design decisions (locked)

1. **Shape:** `{ type: "split", split: { percent_a: 1..99 }, yes_steps: [...A...],
   no_steps: [...B...] }`. **Reuse `yes_steps`/`no_steps` as the A/B branches.** This keeps
   all generic tree helpers, the `yes|no` path tokens (zero `ParseStepPath` change, FE and
   BE), `issueMap`, and backend `BuildStepPath` parity for free. Display is A/B; the wire
   format stays yes/no. (The alternative — new branch arrays — touches all 74
   `yes_steps/no_steps` occurrences across 7 FE files plus the path token format. Not worth it.)
2. **Branch choice:** `A ⇔ hash(run.ID, step.ID) mod 100 < percent_a` (sha256-derived,
   uniform). Pure function of persisted, resume-stable inputs: run IDs are refetched by ID,
   step IDs come from the run's *pinned* WorkflowVersion. Same run always re-derives the
   same side; retries can never flip. Per-run (not per-contact) randomness: a contact
   re-enrolled in a new run may land differently — correct for percentage rollouts.
3. **Terminal fork, no-merge, FE-enforced** — identical doctrine to If/Else (backend stays
   permissive; the FE gets a shared `isFork(step)` predicate instead of a third copy of
   the condition checks).
4. **Two deploys, backend first** — because of the silent-skip behavior above. FE must not
   be able to save a split until the engine that executes it is live.

### S1 — backend (one deploy)

The recon's 11-site touch list, in dependency order:

| # | Site | Change |
|---|------|--------|
| 1 | `models.go:255-264` StepSpec | `Split *SplitParams` (`percent_a int`), Type value |
| 2 | `validator.go:119-185` | `case "split"`: percent 1-99, branch recursion at depth+1 (mirror condition `:165-166`); keep the default-reject |
| 3 | `engine.go:819-977` walk | `case "split"`: pin-then-decide — reuse the condition case's `hasAnyStepStarted` pinning, decision = hash, recurse taken branch |
| 4 | `engine_wait.go:62-79` `stepPathIndex` | recurse split branches (else `{{actions.<id>}}` outputs vanish after resume) |
| 5 | `engine_wait.go:85-100` `hasAnyStepStarted` | recurse split branches (else pinning never sees progress → decision could theoretically drift if the hash inputs ever changed — belt and braces) |
| 6 | `engine_dryrun.go:76-129` | `case "split"`: surface the deterministic assignment (`Branch`, reached/unreached) |
| 7 | `models.go:267-322` `FlattenStepsToActions` | recurse split branches — **security: do this in the same commit as #1** |
| 8 | `dto.go:348-359` `countStepsList` | count split branch contents |
| 9 | `step_path.go` | no change (yes/no reused) — add a test asserting split paths round-trip |
| 10 | `ai_draft.go:288-299` `normalizeStepIDs` | recurse split branches (prompt vocabulary itself waits for S3) |
| 11 | run-now / triggers | no change (splits are steps, trigger-agnostic) |

Tests: determinism (same run/step → same branch across simulated re-walks; distribution
sanity over many runs); resume-with-parked-delay-inside-split (path aliasing + pinning);
flatten sees into branches (assert `shouldActivate` refuses a template with send_email
hidden in a split branch — the fail-open regression test); validator bounds; dry-run shape.

### S2 — frontend (one deploy, after S1 is live)

- **Types/schemas:** `types.ts:42-47` union + `split` payload + `TestRunStep.type`
  (`:128`); `schemas.ts:60` stepTypes + a `splitSchema` (percent int 1-99) in
  `stepSpecSchema:72-85`. (The A6 lesson generalized: TS union AND zod enum, same commit.)
- **Store — introduce `isFork(step)`** and convert the condition-hardcoded sites:
  `findStepLocation:196` (else drag/duplicate can't reach split children),
  `getStepDepth:267` / `getSubtreeDepth:286` (else the absorb-aware depth guard
  undercounts and the backend 400s), `conditionIsTerminal:429` → fork-terminal,
  `normalizeMergesInList:500`, `insertConditionAbsorbing:522` + `addStep` dispatch
  `:742-744` (a split insert absorbs trailing siblings into A, same UX as If/Else),
  `duplicateStep:803` exclusion, reorder guard `:816`, `validateStepConditions:981`
  (percent presence/bounds mirror), `assertTerminalConditions:1002`.
  `parseStepPath` unchanged (yes/no reuse).
- **Graph/canvas:** `NODE_HEIGHTS` key (missing key = NaN dagre layout), `BuilderNodeKind`,
  fan-out block `graph.ts:144-165` labeled `A · 60%` / `B · 40%`; **generalize
  `enforceYesLeft`** to match branch edges by `edge.data.insert.branch === 'yes'` instead
  of the literal label text (edges already carry it — this un-hardcodes the label strings
  for both kinds, one small refactor); `WorkflowCanvas.tsx:42` DRAGGABLE_KINDS;
  `nodes.tsx` SplitNode violet pill (Percent icon, RULE_ACCENT, kebab, dry-run chip —
  clone ConditionNode `:284-305`); `nodeMeta.tsx` splitMeta + `stepSubtitle` (`"60% / 40%"`).
- **Catalog/insert:** `catalog.ts:76-79` RULE_ITEMS entry ("Percentage split") — one entry
  feeds both palette and InsertMenu; **explicit `buildStep` case** (`InsertMenu.tsx:22-35`
  — the default branch silently builds a malformed *action* step); `firstOpenSlot:183`
  descends fork A-branches.
- **Config:** `ConfigPanel.tsx:193-199` route + `SplitConfig` form (percent slider/number,
  A gets X% / B gets 100−X% copy); dry-run chips: `types.ts:134` branch union already
  yes/no — gate `ConfigPanel.tsx:95-99` on fork types, canvas chip in SplitNode.
- Tests: SplitNoMerge suite (clone IfElseNoMerge.test.ts), StepTreeOps fork cases
  (absorb, depth, duplicate-refusal), graph fan-out + A-left, ConfigPanel routing.
  Expect coordinated updates in the recon's list: IfElseNoMerge, StepTreeOps,
  parseStepPath, dragReorder, graph, ConfigPanel tests.

### S3 — copilot enablement (backend, small, after S2)

`ai_draft.go:356-365` prompt vocabulary gains the split shape. **Not before S2**:
`applyDraft` applies drafts verbatim with no step-type gate (`store.ts:1244-1276`) — a
draft containing a split before FE support lands on the canvas and NaN-breaks the layout
rather than being rejected.

### Non-goals

A/B outcome *measurement* (per-branch conversion) — the action logs make it queryable
later; M9's subject-line A/B stays a separate campaign feature. N-way splits (>2 branches)
— would force the new-branch-arrays representation; revisit only with real demand.

---

## Shared traps checklist (apply to every phase)

- Prod runs **boot guards only** — this plan needs **zero schema changes**; if any v2 item
  adds one, it goes in main.go guards (raw DDL, fresh index names, lock_timeout pattern),
  never GORM tags alone.
- Backend gates: `go build` + `go vet`; **never `gofmt -w`**. FE gates: `npx tsc -b` +
  `npx vitest run` (raw, not rtk). CI's `needs:` list is the only deploy gate — Lint Go
  (golangci incl. staticcheck) blocks the frontend deploy too.
- Deploy ordering is load-bearing twice: S1 before S2 (silent step-skip), S2 before S3
  (verbatim drafts). G1 before G2 is merely tidy (G2 without G1 just 400s on save).
- Fire-and-forget emissions use `context.Background()` (the inbound-webhook lesson).
- The FE/BE mirror pairs that must change in lockstep: `entityKindForTrigger`,
  `MAX_STEP_TREE_DEPTH`/`MaxStepTreeDepth`, `parseStepPath`/`BuildStepPath`,
  `resolvableObjectsForTrigger`/`buildEvalContext`.
