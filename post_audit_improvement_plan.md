# Post-Audit Improvement Plan (R0–R9)

> **Status:** 📋 **PLAN — nothing in this document is built yet.** Source: the 2026-07-29 eight-dimension audit (plans / code debt / security / product / UX / testing / perf / ops, payments excluded) plus a six-scout implementation-fact pass. Every file path and line number below was verified on `main` @ `62c21bd` on 2026-07-29 — **they are research pointers, not guarantees; re-grep at implementation time.**
>
> **Deploy doctrine (load-bearing, repeated from prior plans):** `golang-migrate` is dead on prod (dirty at v2; numbered `migrations/*.sql` do **not** run). Non-automation schema changes ship as idempotent boot guards in `cmd/server/main.go` (+ `ENABLE ROW LEVEL SECURITY` + pg_class sweep entry, never `FORCE`, + mirrored dev-only migration file). **Automation tables are the exception:** they change via the engine's `Repository.AutoMigrate` (`internal/automation/repository.go:30-66`), which runs on prod. Unique indexes use the probe-and-refuse ritual; non-zero-default columns carry a DDL `DEFAULT` (GORM omits zero values). Backend gates are `go build` + `go vet` — **never run `gofmt -w`** (tree isn't gofmt-clean). Frontend checks: `npx vitest run` (rtk breaks vitest) + `npx tsc -b`; in a worktree run `npm ci` first. Bare `C:\DEV\20q-crm\...` paths hit the MAIN checkout — check `git branch --show-current` before committing.

---

## Why

The platform is feature-complete across its big arcs (objects P2–P9, auth P1–P10, users U0–U7, automation A1–A8, marketing M1–M9, leads L0–L7) but the audit found four kinds of debt:

1. **Stranded/dormant shipped work** — a security fix that exists only on a local branch; the entire marketing stack dormant behind ~2 hours of operator setup.
2. **Real bugs** — Kanban silently capped at 25 records, sorted pagination that skips rows, list pages that render errors as empty states, `date_field` triggers that ignore pre-existing records, an unauthenticated `/builder-demo` route, AI budgets unlocked to 10B tokens for every tier.
3. **Security + engineering hygiene** — SSRF in `send_webhook`, plaintext webhook HMAC secrets, prod bootable on the default `JWT_SECRET`, zero security headers, CI that runs no frontend checks and silently skips every DB integration test, a 2026-09-01 teardown deadline.
4. **Product gaps vs commercial CRMs** — no 1:1 email, no global task queue, no dup-merge, contacts-only 6-column import, no list export, single pipeline, no API docs.

Phases R0–R5 are ordered (each unblocks or de-risks the next). R6–R9 are parallelizable batches — pick by value.

| Phase | Name | Type | Effort |
|-------|------|------|--------|
| **R0** | Land stranded work + key hygiene | ops + 1 merge | half-day |
| **R1** | Marketing go-live | operator checklist | ~2h (user) |
| **R2** | Security hardening batch | backend + FE | 3–5 days |
| **R3** | CI/testing overhaul | infra | 2–3 days |
| **R4** | Correctness bug batch | backend + FE | 2–3 days |
| **R5** | Flat-Actions teardown (**deadline 2026-09-01**) | backend + FE | 2–3 days |
| **R6** | Performance batch | backend + FE | 2–3 days |
| **R7** | UX batch | mostly FE | 1–2 weeks |
| **R8** | Product: daily-driver batch (tasks / import-export / dup-merge) | full-stack | 2–3 weeks |
| **R9** | Product bets (pick 1–2 per cycle) | full-stack | large each |

---

## R0 — Land stranded work + key hygiene (do first, half a day)

**Problem.** Four items are pure risk with near-zero effort to clear.

**Build / do:**
1. **Merge `2a501c0`** (branch `claude/exciting-noyce-710c13`, local-only, 1 commit ahead of main, exists nowhere else — loss risk). It row-scopes legacy `GET /api/tasks`, `/api/activities`, and per-record audit reads (589 insertions incl. migration `000043_task_created_by` + a `main.go` boot guard). Until merged, own/team-scoped users read every task org-wide on prod, and audit rows return FLS-hidden values. Merge → push → verify the boot guard ran on deploy (check logs for the guard's log line). *Bonus:* this adds `tasks.created_by`, which R8's task queue needs.
2. **Set `TOTP_ENC_KEY` on Railway NOW, before any thought of rotating `JWT_SECRET`.** Today the TOTP encryption key derives from `JWT_SECRET` (`pkg/config/config.go:26-32`, derivation in `internal/usecase/two_factor_crypto.go:41`) — rotating JWT_SECRET would brick every enrolled 2FA user. Compute the currently-derived key value and set `TOTP_ENC_KEY` to exactly that, so existing secrets keep decrypting and the two keys are decoupled from now on.
3. **Rotate the committed prod admin credential.** `crm-backend/scripts/seed_live_account.js:4,39-40` hardcoded a live admin login against the live Railway URL (reused by `seed_full_account.js` + several `test_*.js`). Rotate or disable the account; change the scripts to read `SEED_EMAIL`/`SEED_PASSWORD` env vars. *(Credentials redacted from this doc — the repo is public, and this file is destined for it.)*
4. **Repo housekeeping.** Prune the 10 stale `.claude/worktrees` (all 0 ahead after step 1) and the 8 fully-merged `feat/email-marketing-m*` / `claude/*` branches. Confirm in Railway that `WEBHOOK_SKIP_SIGNATURE` is deleted (it's dead weight since the APP_ENV gate) and that `APP_ENV` is NOT set to `development` (that would unlock the debug-token escape hatch).

**Done when:** `git branch -a` shows main only (+ origin), 2a501c0's row-scope behavior verified live (own-scope user sees only own tasks), `TOTP_ENC_KEY` set and a 2FA login still works, `live_admin` password rotated.

---

## R1 — Marketing go-live (operator checklist — the user does these)

**Problem.** M1–M9 is deployed and review-hardened but **dormant**: `campaign_sender.go:24` gates live send on the B3 DKIM test; `MARKETING_UNSUB_KEY` unset keeps the send gate closed (`cmd/server/main.go:3062` warns exactly this); `RESEND_WEBHOOK_SECRET` unset 401s every delivery webhook, so M4 auto-suppression and M9 analytics stay dark.

**Checklist (order matters):**
1. **B3 DKIM `h=` test** on the real Resend account (procedure in `email_marketing_spikes.md` B3): send via `/emails` with `List-Unsubscribe` headers, inspect the received DKIM signature's `h=` tag for header coverage. This is *the* go/no-go for the M3/M7 architecture — if headers aren't covered, we regroup before flipping anything else.
2. **Verify a sending domain** in Settings → the M2 wizard (SPF/DKIM/DMARC gate), and publish the **tracking CNAME** (M9: open/click tracking activates only once it's live — `AddDomain` PATCHes Resend tracking on).
3. **Set Railway env:** `MARKETING_UNSUB_KEY` (keyring format `1:<base64 32-byte key>` — same `ParseKeyring` grammar as `INTEGRATION_ENC_KEY`), `RESEND_WEBHOOK_SECRET` (from step 4), optionally `RESEND_MAX_RPS` (default 8).
4. **Register the webhook endpoint** in the Resend dashboard → `POST https://<prod>/api/marketing/webhooks/resend`, copy the Svix signing secret into `RESEND_WEBHOOK_SECRET`.
5. **Set `TRUSTED_PROXIES`** (Railway/Cloudflare CIDRs — `cmd/server/main.go:81-105` documents why: without it every caller shares one rate-limit bucket, so a single abuser throttles all logins and lead-form posts).
6. Smoke: test-send a campaign to a seed contact, confirm the webhook ledger row lands (`marketing_email_events`), open the message, confirm the open event + unsubscribe link round-trips.

**Done when:** one real campaign delivers, its events appear in per-campaign analytics, and an unsubscribe suppresses.

---

## R2 — Security hardening batch

### R2.1 SSRF guard on `send_webhook` (highest-severity code finding)

**Facts.** `executor_webhook.go` fetches a workflow-author URL that is **template-interpolated at run time** (`getStringParam` → `InterpolateTemplate`, executor.go:25-35) — so save-time validation alone can never constrain the final URL. Client is built fresh per call with only a timeout (`:63`) — default transport, default redirect policy (follows 10 redirects), no dialer control. Response body (1MB-capped) flows into `evalCtx.Actions[step.ID]` and is addressable as `{{actions.<id>}}` in later steps (engine.go:1052, 726-743) — **a read-back exfiltration path into Railway's internal network**, not blind SSRF. There is no IP/hostname validation helper anywhere in the backend (grep: zero hits for `IsPrivate|IsLoopback|DialContext|CheckRedirect`). `timeout_sec` has no upper cap. Go is 1.26.1 → `Dialer.ControlContext` available.

**Build.**
- New `internal/automation/safedial.go` (or a small shared pkg): an `http.Transport` whose `DialContext` uses `net.Dialer.ControlContext` to reject, **at connect time on the actual resolved address** (immune to DNS rebinding): loopback, RFC1918 private, link-local (v4 `169.254.0.0/16` incl. the metadata IP, v6 `fe80::/10`), unspecified, multicast, and v4-mapped forms — via `netip`. Scheme allowlist `http`/`https` only; block userinfo URLs.
- `CheckRedirect`: re-run the same validation per hop (or simply refuse redirects — decide at build; refusing is simpler and webhook receivers rarely redirect).
- Cap `timeout_sec` at 60 (currently uncapped, `:37-40`). Keep the 1MB body cap.
- Save-time hardening (defense-in-depth, not the fix): `validator.go:650-657` is presence-only — add URL-parse + scheme check; mirror in the zod side (`schemas.ts` only enumerates the action type).
- Optional env escape hatch `WEBHOOK_ALLOW_PRIVATE=true` for self-hosted/dev (APP_ENV-gated like `skipSignatureAllowed`, handlers.go:116-121).
- Tests: no `executor_webhook_test.go` exists today; the established stub pattern is `httptest.NewServer` (`integration_test.go:1112` uses the real executor). Unit-test the dialer guard directly with crafted addrs; integration-test that a `127.0.0.1` URL fails **permanent** (not retryable — don't burn the retry schedule on a blocked target).

### R2.2 Seal the inbound-webhook HMAC secret + rotation

**Facts.** `WorkflowOrgToken.Secret` is plaintext `varchar(128)` (`models.go:97-104`). All read/write sites are in `handlers.go`: create `:1374-1399`, rotate `:1342-1367`, reveal `:1317-1332`, masked display `:1288-1304`, HMAC verify `:962-963`; repo `:519/:533/:546/:552`. The envelope pkg (`internal/integrations/envelope`) is stdlib+uuid only — **no import cycle** (marketing already imports it, `unsubtoken.go:8`). The shared codec is `integrationCodec` (`main.go:60`), currently handed only to integrations (`:2800-2807`) + canary (`:2815`). Reveal requires recoverability → **envelope-encrypt, not hash**. **Column-size trap:** a row-bound `ienv1` blob for a 64-char secret is ~213 chars, stateless `senv1` ~131 — neither fits `size:128`; the struct tag must become `type:text` (automation tables AutoMigrate on prod, so the widen ships itself).

**Build.**
- Pass `integrationCodec` into `automation.NewHandler` (constructed `main.go:2440`; the handler already takes 5 args — add a 6th, keep the nil-capChecker panic).
- New `envelope.Purpose("workflow_org_webhook_secret")` (a Purpose is a namespace — never reuse). Binding: `{OrgID, purpose, ID: OrgID}` (the table's PK **is** `org_id`, so the row id = org id; Binding requires all three fields non-zero — satisfied).
- Seal on create + rotate; open before `computeHMAC` in `WebhookInbound` and before reveal/mask. **Nil-codec doctrine:** deployments legitimately run without `INTEGRATION_ENC_KEY` (`main.go:2998-3001`) — when codec is nil/unconfigured, keep plaintext behavior (log a warn once); prod should set the key (it already must for integrations).
- Migration: lazy — on first successful `Open` failure with a non-`ienv1` prefix, treat the stored value as plaintext (backward-compat read), and re-seal opportunistically on rotate. Then **force the rotation memory demands**: after deploy, call `POST /api/webhooks/regenerate-secret` per org (or a one-shot backfill in `Repository.AutoMigrate` that seals existing plaintext rows in place — cleaner; pick at build).
- Canary: add these rows to the startup canary sample (pattern: `ConnectionCanaryRows`, `connection_repository.go:439` → `main.go:2815`).
- Update the 5 test files that assume plaintext (`handlers_webhook_token_test.go:246-373`, `integration_test.go:1635`, `webhook_upsert_integration_test.go`, `dod_log_activity_e2e_test.go:92`, `dto_test.go:272`).

### R2.3 Boot guards + headers + small fixes

- **`JWT_SECRET` fail-fast:** viper default is `"dev-secret-change-me-in-production-32chars!"` (`config.go:210`). Add a boot check: if the value equals the default AND env is not development/test (**treat unset `APP_ENV` as prod** — prod runs with it unset per `docs/l7-deploy-checklist.md`), `log.Fatal`. Mirrors the existing prod gating of debug tokens.
- **Security headers:** add `crm-frontend/public/_headers` (Pages already picks up `public/_redirects`): HSTS, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, a minimal `Permissions-Policy`, and CSP in **Report-Only first** (the SPA inlines Vite preamble; tune before enforcing).
- **TOTP replay window:** `users.totp_last_step` column (users = non-automation → `main.go` boot guard + mirrored migration) + compare-and-swap in `consumeSecondFactor` — reject a code whose step ≤ last accepted step.
- **Remove `/builder-demo`:** route registered OUTSIDE `ProtectedRoute` (`App.tsx:157`, "TEMP A3 … remove after verifying"). Delete route + `features/workflows/builder/__demo__/` (nothing imports it).
- **AI budget guard:** `budget_guard.go:22-32` gives all four tiers 10 B tokens + advanced=true ("Temporary unlock"). Restore real per-tier numbers — **needs a business decision on the numbers**; wire-up is trivial (the map is the only change; unknown tier already falls back to `free`).
- **Audit-log gaps:** `recordAdminEvent` for `RevealWebhookSecret`/`RegenerateWebhookSecret` (the code comment already calls reveal "an explicit, auditable retrieval"), `DeleteWorkflow`, marketing admin actions (campaign delete, suppression edits, domain delete); ObjectAudit rows for bulk contact delete (single deletes are audited, bulk isn't — `contact_usecase.go:299-349`).
- **Voice-note hardening:** cap upload well below 500MB and stream instead of `io.ReadAll` (`voice_handler.go:43,56`); sniff content type (allowlist audio/*); org-scope `PreviewVoiceNote` (`:257-274` currently serves any file to any authed user of any org) + `nosniff`. The R2-bucket stub (`uploadToR2` errors when `R2_BUCKET_NAME` is set, `voice_note_usecase.go:241`) either gets implemented (S3-compatible PUT) or the env switch removed — decide; local-disk storage is lost on every redeploy either way.

**Done when:** a workflow pointing `send_webhook` at `http://169.254.169.254/` fails permanently with a clear error; DB dump shows `ienv1.` prefixed secrets; prod restart with default JWT_SECRET refuses to boot (verified in a dev sim); securityheaders.com scores the SPA; a replayed TOTP code fails; `/builder-demo` 404s; audit page shows secret-reveal events.

---

## R3 — CI/testing overhaul

**Facts.** Sole workflow `.github/workflows/deploy.yml`: `test` job = backend-only `go vet` + `go test -short` (every DB test skips — they're all testcontainers-gated behind `testing.Short()`); `deploy-frontend` has **no `needs:`** and runs `npm install` (not `ci`) + build; `deploy-backend` is disabled (`if: false`) — the real backend deploy is Railway GitHub auto-deploy, ungated by anything. `setup-go` pins 1.23 while `go.mod` says 1.26.1 (works only via toolchain auto-download). Integration tests self-provision postgres via testcontainers (`postgres:16-alpine`, two suites need `pgvector/pgvector:pg16`) — **ubuntu-latest has Docker, so no `services:` block is needed; just drop `-short`**. One helper honors `TEST_DATABASE_URL` as a Windows escape hatch (`object_registry_repository_test.go:25-38`). Frontend: `vitest` script is watch-mode (CI must call `vitest run`); vitest needs no env; eslint 9 configured but never run in CI.

**Build.**
1. **Restructure `deploy.yml` into:** `backend-unit` (vet + `-short`, PR+push), `backend-integration` (`go test -timeout 900s ./...` no `-short`, testcontainers pulls its own images; PR+push), `frontend` (`npm ci`, `npx tsc -b`, `npx eslint .`, `npx vitest run`; PR+push), `lint-go` (`golangci-lint` — the Makefile target exists; **do NOT add a gofmt gate**, tree isn't gofmt-clean by doctrine). Use `go-version-file: crm-backend/go.sum`-adjacent (`go-version-file: crm-backend/go.mod`) to kill the 1.23/1.26.1 drift.
2. **Gate deploys:** `deploy-frontend` gets `needs: [backend-unit, backend-integration, frontend]`. Backend can't be gated from Actions (Railway auto-deploys on push) — **enable Railway's "wait for CI checks" setting on the service** (ops toggle, document it in the workflow header comment where the old CLI-deploy incident is already written up).
3. **E2E smoke (new, separate workflow):** Playwright against the docker-compose stack (`docker-compose.yml` already defines pgvector+redis; seed via `scripts/seed_local_account.js`, which now requires `SEED_EMAIL`/`SEED_PASSWORD`). Five journeys: login → create contact → contact appears in list/kanban → build & run a simple workflow → campaign test-send renders. Run on PR label + nightly; keep it out of the merge-blocking path until it proves stable. Rationale: the two worst shipped bugs (A4 zod save-blocker, M4 jsonb 22P02) were only findable by driving the real UI.
4. **Targeted backend coverage** (rides along, not a gate): handler-level tests for `auth_handler.go` / `two_factor_handler.go` / `api_token_handler.go` (the delivery layer is 8 test files / 41 sources and it's where the U7 bug-hunt found 7 security fixes); direct tests for the run/log + campaign pruners (they hard-delete prod data >90d — a bug is destructive).

**Done when:** a PR with a frontend type error goes red; a PR breaking a DB-backed automation test goes red; Railway waits for green; E2E runs green 3 nights straight.

---

## R4 — Correctness bug batch

### R4.1 The Kanban-25 clamp
`record_service.go:164-167`: `if limit <= 0 || limit > 100 { limit = defaultRecordLimit }` — an over-limit request (Kanban asks 200, `ObjectKanban.tsx:56`) is **reset to 25**, not capped. Fix: clamp to 100 (`if limit > 100 { limit = 100 }`; keep 25 default for `<=0`). Then make Kanban page: follow `next_cursor` (the response already carries it — `domain.RecordList`, `object_registry.go:192-195`) until exhausted or a sane board cap (e.g. 500, with a "showing first 500" notice). Same fix for the relation-filter options fetch (`ObjectListView.tsx:166`, also asks 200 → gets 25). `ObjectForm.tsx:58` asks exactly 100 — already fine. Pin the clamp in a unit test so the reset behavior can't regress back.

### R4.2 Keyset cursors must match ORDER BY
- **deals** (`deal_repository.go:27-104`): cursor is `base64(id)` applied as `id < ?` while ORDER BY is a user-chosen column (`value|probability|title|created_at` + ASC/DESC). Fix: cursor carries `(sort_value, id)` (JSON→base64 like contacts' `cursorData`), comparator `(sortCol, id) < / >` matching direction, minted from the last row's actual sort value.
- **contacts** (`contact_repository.go:101-256`): cursor is already a `(created_at, id)` tuple but **hardwired** — wrong for `name`/`email` sorts and for ASC (comparator must flip). Extend `cursorData` with the sort key + direction awareness.
- **companies** (`company_repository.go:27-73`): fixed `created_at DESC` ordering but an `id < ?` cursor — switch to the `(created_at, id)` tuple. (Also noted: company List lacks `applyScopeFromCtx` unlike deals/contacts — verify intent while in the file.)
- Today `SortBy` is only reachable via AI paths (`command_center.go:956`, `intent_router.go:225`) — the HTTP handlers never bind it despite form tags existing (`interfaces.go:666-667/843-844`). Fix the repos first (correctness), optionally wire handler sorting after R7's column-sort UI lands.

### R4.3 List pages must show errors
Copy the `SharedWithMePage.tsx:43-50` pattern (react-query `error` branch → destructive banner + message) into: `ObjectListView.fetchFirstPage` (`:238-240`, currently `catch { setRecords([]) }` → renders "No X yet."), `ObjectKanban` (`:55-56`, `.catch(() => [])`), `ReportsListPage` (`:29-49`). Add a retry button; `loadMore`'s keep-what-we-have catch (`:257-258`) gets a toast instead of silence (toast primitive arrives in R7 — a plain inline banner is fine until then).

### R4.4 `date_field` backfill on activation
**Facts.** Timers materialize only from live writes (`engine.go:346` → `materializeDateFieldTimers`); pre-existing records arm on their *next* write by design (`datefield_timers.go:27-32`). The natural hook is `Handler.ToggleWorkflow` → `armWorkflowTimers` (`handlers.go:504`). **The trap (pinned by `engine_silence_test.go`):** `fireTimerRun` builds runs straight from the stored payload and consults **no** predicate — any guard must be applied at ARM time, and cancels must never be skipped.
**Build:** on activation (and on trigger-params change — check every `armWorkflowTimers` call site), run a backfill scan for the trigger's object: engine holds `db` directly (callerless — every query carries explicit `org_id`, Guardrail-8 doctrine); iterate records in batches (raw SQL per slug; custom objects via `custom_object_records`), evaluate `computeDateFieldFireAt` per record, arm via the existing `MaterializeDateFieldTimer` — it's already idempotent (`OnConflict(workflow_id, dedupe_key) DO NOTHING`, dedupe key `df:<recordID>:<unixts>`), so re-activation double-arms nothing. Respect `isAutomationSilenced` at arm time. Cap the scan (e.g. 10k records, log truncation per the no-silent-caps rule).

**Done when:** a 30-deal board shows 30 cards; page 2 of a value-sorted AI deal list neither skips nor repeats; killing the API mid-load shows an error banner with retry, not "No contacts yet."; activating a date_field workflow on an org with existing matching deals arms timers for them (integration test: activate → `SELECT count(*) FROM automation_timers WHERE kind='date_field'`).

---

## R5 — Flat-Actions teardown (hard deadline 2026-09-01)

**The gate.** Run the verification SQL from `repository.go:617-622` on prod:
```sql
SELECT count(*) FROM automation_workflows
WHERE (steps IS NULL OR steps::text = 'null') AND deleted_at IS NULL;
```
**Plus a versions check the doc-comment omits** (the backfill wrote both tables; versions has no `deleted_at`):
```sql
SELECT count(*) FROM automation_workflow_versions WHERE steps IS NULL OR steps::text = 'null';
```
Both must be zero. **Rollback warning from the plan doc:** a prior backend version still reads the column — so ship as **two deploys**: deploy 1 removes all code (columns remain, harmless orphans); deploy 2 (after a safe soak) drops the columns.

**Backend removal inventory (verified 2026-07-29; line numbers drift):**
- `models.go`: `Workflow.Actions` (:25) + `WorkflowVersion.Actions` (:44) — **both are `NOT NULL` with no DB default: dropping the Go fields without dropping the columns breaks every INSERT.** Deploy-2's drop = raw idempotent `ALTER TABLE … DROP COLUMN IF EXISTS actions` on both tables, added to `Repository.AutoMigrate` alongside the existing raw Execs (precedent: the P7 FK drops in `object_link_repository.go:181-184`); safe because AutoMigrate won't re-add a field that no longer exists on the struct. `FlattenStepsToActions` (:230-262).
- `handlers.go`: `deriveActionsFromSteps` (:222-239) + derive/validate/assign sites in Create (:256-279) and Update (:423-442); `dto.go`: request `Actions` fields (:18, :28), `WorkflowResponse.Actions` (:62), `ToWorkflowResponse` fallback (:275-280, :290).
- `repository.go`: `MigrateFlatActionsToSteps` (:623-700) + its AutoMigrate call (:60) + version-snapshot `Actions:` copies (:87, :108) + the adjacent legacy action-log backfill Exec (:63).
- `engine.go`: the legacy flat branch of `processRun` (:772-930) + `DelayExecutor` registration (:272-274); `DelayExecutor` itself lives in `executor_webhook.go:100-137` (non-test callers: the registration only).
- `engine_dryrun.go`: flat fallback (:59-76); `validator.go`: flat branch (:46-48) + `validateActions` (:487+).
- **KEEP (grep noise):** `EvalContext.Actions` (runtime step-output map — steps path), `CompletedActions`/`CurrentActionIdx` bookkeeping (shared with durable-wait resume), `WithRecordActions`, `executeAction`, `executeStepsRecursive`, and `domain.Workflow.Actions` (`domain/models.go:444` — a *different* pre-overhaul table).
- **Tests:** delete/rewrite the Actions-only fixture set (`integration_test.go:184-221` helper + ~10 inline fixtures; `engine_delay_test.go:388-446`; `engine_wait_until_test.go:94/241/251`; `validator_test.go:35/685`; `executor_update_record_test.go` ~30 direct `validateActions` calls; `engine_dryrun_test.go:227`; `dto_test.go` ActionCount set). The dual-write set (fixtures that also set Steps: `datefield_timers_test.go:137`, `engine_suppression_test.go:57`, `timers_test.go:85`, …) just drops its `Actions:`/`Flatten` lines.

**Frontend (same PR as deploy 1):** `api.ts` deprecated `actions?` params (:37, :62 — never populated; `buildSavePayload` is steps-only); `store.ts` shims `insertAction`/`removeAction` (zero callers) + `reorderActions` (one shim test, `StepTreeOps.test.ts:1349-1362`); `applyLoadedWorkflow`'s actions→steps mapping (:1196-1203) — removable once the prod SQL proves steps everywhere; `types.ts` `Workflow.actions` (:67) + the `WorkflowList.tsx:308` `wf.actions?.length` fallback — only after the backend stops echoing `Actions`. **KEEP the in-memory flattened `actions` view** (`flattenSteps`, BuilderState `:42`) — it's load-bearing for `ActionConfig` and `validate()` (`store.ts:1369-1374` documents this).

**Deferred separately** (plan doc line 233-234, don't couple): `builtinObjectFieldDefs` → registry-backed workflow schema (would add per-viewer FLS to `/api/workflows/schema`), and the store's test-coupled load/save cleanup.

**Done when:** both SQLs return 0; deploy 1 soaks a week with zero `actions`-related errors; deploy 2 drops both columns; an Actions-only POST to `/api/workflows` is rejected with a clear validation error.

---

## R6 — Performance batch

1. **Code-splitting.** One 3.4 MB chunk today (`dist/assets/index-*.js`; no `React.lazy` anywhere, no `manualChunks`). Route-level `lazy()` in `App.tsx` (~70 static imports) + `manualChunks` for `@xyflow/react`, TipTap, recharts, dnd-kit. Login page should load none of them. Target: initial chunk under ~800 KB. (R2 already removed `/builder-demo` from the bundle.)
2. **Indexes (all boot guards; marketing table is non-automation → `main.go`):**
   - `marketing_email_events`: `(org_id, campaign_id, event_type, email_normalized)` — M9 analytics + the MPP LATERAL probe currently walk `(org_id, created_at)` for the whole org ledger per dashboard load.
   - `custom_object_records`: GIN `(data jsonb_path_ops)` — powers `data->>key` relation filters + reverse related lists (`related_lists_usecase.go:58-61` seq-scans per record-detail load); pg_trgm on `display_name` for the leading-wildcard ILIKE — **needs `CREATE EXTENSION IF NOT EXISTS pg_trgm`** (only vector + uuid-ossp exist today). R8's dup detection reuses trgm.
   - ANN for embeddings: **deliberately deferred** (migration comment) — document the growth trigger (≥ ~50k embedded rows → one-line hnsw boot guard) instead of adding it now.
3. **Embedding pipeline reconciliation.** `EmbeddingWorker` queues on buffered channels and **drops jobs when full** (`embedding_worker.go:78-84`), losing them on redeploy too — a dropped job = permanently invisible to semantic search (no `embedding IS NULL` sweep exists). Add a periodic reconciliation ticker (main.go goroutine pattern like the digest, `:2382-2395`): embed rows with NULL embedding, org-batched. (Moving the queue to Redis like `ai_queue.go` is the bigger alternative — the sweep is cheaper and also heals historical drops.)
4. **Tasks/activities caps:** `task_repository.go:45` hard `Limit(200)`, `activity_repository.go:34` `Limit(100)`, no cursor — a deal with >100 activities silently loses its older timeline. Add keyset pagination (R8's task page needs it anyway).
5. **Note for future scale (no code now):** the provider rate-limiter's Redis-error fallback gives each process a full local bucket (`ratelimit.go:36-44`) — fine single-instance; divide by replica count before ever scaling backend replicas horizontally.

---

## R7 — UX batch (ordered by pain)

1. **Bulk actions on `ObjectListView`.** The P7 cutover lost multi-select (dead `ContactList.tsx` had it; live view has none). Backend `POST /api/contacts/bulk-action` **still exists** (`router.go:327` — delete + assign_tag; `BulkDeleteByIDs` is row-scoped and redacts ledgers). Add a selection model + toolbar to `ObjectListView`; contacts get delete/assign-tag immediately; **fix the gate while there: bulk delete is gated `ActionEdit`, should be `ActionDelete`**. Generic objects need a `RecordService` bulk surface (interface has none — single-record only) or a bounded loop; start with contacts parity.
2. **Saved views, step 1: URL persistence.** Copy the `WorkflowList.tsx:52-117` pattern (useSearchParams + debounced `q` + back/forward sync) into `ObjectListView` (today ALL state is `useState`, reset on slug change — refresh/share loses everything). Step 2 (server-side saved views): new table shaped like `notification_preferences` (org+user unique, jsonb) serializing `RecordListInput` — later, after step 1 proves the shape.
3. **Column sort + column chooser.** `MAX_COLUMNS = 4` hardcoded, no sort anywhere. Extend `RecordListInput` with sort params through the registry endpoint (R4.2 fixed the repos to make this honest), `aria-sort` headers, per-view column selection (persisted with saved views).
4. **One toast primitive + kill native dialogs.** 12 hand-rolled `useState` toasts (all marketing pages, EmailTemplatesPage, WorkflowList…), `alert()` in `VoiceLibrary`/`FormEmbedSetupCard`, `confirm()` in EmailTemplates/WorkflowList/ReportBuilder. Build one `components/ui` toast (aria-live) + sweep; route confirms through the existing `ConfirmDialog`. (U7 rule: overlays compose `components/common/Modal.tsx`; no `autoFocus` in modals.)
5. **GlobalSearch → real command palette.** It's a hand-rolled overlay (no role=dialog, no focus trap) whose results are raw `<a href>` → **full page reload** — the exact defect the sidebar was cured of (`AppLayout.tsx:127-129`). Recompose on the Modal primitive, client-side navigation, ArrowUp/Down, and add action commands ("New contact", "New workflow"). Same treatment for the AppLayout user menu + workflow InsertMenu.
6. **Kanban keyboard support.** Only `PointerSensor` registered (`ObjectKanban.tsx:49`); the repo's own fix pattern (KeyboardSensor) already ships in `EmailBuilder`/`BlockPalette`. Also make Enter open a focused card.
7. **Currency/locale actually applied.** Workspace stores currency+locale but `DealDetailPage` hardcodes `$` (4 sites), `ForecastChart` hardcodes `en-US`, `fieldHelpers` renders numbers as `String(value)`. One `formatCurrency`/`formatNumber`/`formatDate` helper trio reading workspace settings via `Intl.*`; sweep the hardcoded sites. (Full i18n stays R9 — this is just honest formatting.)
8. **Responsive pass on inner grids** — fixed `grid-cols-2/3` in ObjectDetailView sections, DealDetailPage stats, DealFormModal, ImportModal, RegisterPage, ObjectsManager (the shell is responsive; content isn't).

*Deliberately deferred:* undo/trash (needs soft-delete across RecordService — real design work; confirm dialogs remain the guard), onboarding sample data, shortcut system beyond Ctrl+K.

---

## R8 — Product: the daily-driver batch

### R8.1 Global task queue + reminders
**Facts.** `domain.Task` has DueAt/AssignedTo/Priority/CompletedAt (+`created_by` once R0 merges 2a501c0) but **no status field**; `TaskFilter` = 4 predicates, no due-range/q/pagination; hard 200 cap; the only UI is DealDetailPage; task writes emit **no** automation events, and no scanner watches `DueAt`. Notification infra is ready: `NotificationUseCase.Create` publishes in-app + SSE; **a new event type must be appended to `domain.NotificationEventTypes`** (`notification.go:197-205`) or it delivers with defaults but never renders in the preference center.
**Build:** `/tasks` page (My tasks / Today / Overdue / by rep for managers) on a new route; extend `TaskFilter` with due ranges + `q` + keyset cursor (R6.4); a due-task scanner ticker in main.go (digest pattern) creating `task_reminder` notifications (register the type; dedupe per task+day); "add task" from any record page, not just deals. *Decision:* whether tasks join the object registry (reportable, OLS grid) — bigger lift, defer; a registry descriptor is also what R9's "reportable activities" needs, so decide once there.

### R8.2 Import wizard + list export
**Facts.** Import = contacts-only (`supportsImport = slug === 'contact'`), 6 hardcoded header aliases (`mapColumns`, `contact_usecase.go:614-637`), no mapping UI; `BulkCreate` is chunked-500 `ON CONFLICT DO NOTHING`; a documented silent-loss trap sits in overwrite mode (`:469-474`). `RecordService` has **no bulk-create** — a generic importer loops single `Create`s (each firing events + indexing) or adds a bulk method. Export exists only for reports + audit; the CSV streaming + `csvSafe` formula-injection guard (`audit_handler.go:122-129`, shared) is directly reusable; `CapDataExport` already exists.
**Build:** (a) client-side parse (papaparse) + column-mapping step in `ImportModal` mapping headers → registry schema fields (incl. custom fields) with per-column preview; (b) generic import for companies/deals/custom objects through a new `RecordService.BulkCreate` (batch, suppress per-row automation events per the L0 doctrine — **emit via RecordService only, and silence bulk imports like lead-ingest does**; index once at end); (c) `GET /api/registry/objects/:slug/records/export.csv` honoring current filters, streaming through RecordService.List pages (OLS/FLS apply per row), `csvSafe` every cell, `CapDataExport`-gated; wire an Export button into `ObjectListView`.

### R8.3 Duplicate detection + merge (contacts first)
**Facts.** Only exact `(org_id, email)` uniqueness exists (partial unique index + probe-and-refuse boot guard `main.go:1476-1497`); `emailutil.Normalize` is the canonical helper; **no trgm/similarity infra** (R6.2 adds pg_trgm). No merge function exists anywhere.
**Build:** (a) candidate finder — exact email collisions can't exist (index), so candidates = same normalized email on *deleted* pairs, same phone, `similarity(first_name||' '||last_name) > 0.4` via trgm, org-scoped; surfaced on a "Duplicates" settings page + a banner on record detail; (b) merge = pick survivor, re-point `object_links` (P4 made it the sole relationship store — one table to update), `tasks`, `activities`, `contact_tags` (via LinkRepository bridge), union `CustomFields` JSON (survivor wins conflicts), then delete loser through the normal RecordService path (ledger redaction + audit fire); write an ObjectAudit "merged from X" row on the survivor. Wrap in one transaction; add the advisory-lock trigger trick from the L7 memory if a concurrent-merge race test is needed. Companies second (same skeleton, no ledger redaction).

---

## R9 — Product bets (pick 1–2 per cycle; each is its own plan doc when chosen)

Ordered by likely buyer impact over effort:

1. **1:1 email from a record** (medium): send via the org's verified M2 domain through the existing send seam, log to the timeline as an activity, thread replies later. The `EmailComposer.tsx` AI-drafter becomes the compose surface with a real Send. *Two-way inbox sync (Gmail/Outlook OAuth + sync worker) is the large follow-on — separate decision.*
2. **Double-opt-in confirmation** (small-medium, legally load-bearing): the `double_opt_in_at` column + consent model exist; build the confirmation email + public confirm endpoint reusing the `SealStateless` token pattern (`unsubtoken.go` is the template). Unlocks cold-list mailing.
3. **Multiple pipelines** (medium): `pipelines` table + `pipeline_id` on stages/deals + board switcher; migration seeds one default pipeline per org.
4. **Lead scoring** (medium): rules engine over data already collected (M9 `marketing_email_events` opens/clicks + field-fit) writing a score field; score-based segments plug into M5's AST.
5. **Reportable tasks/activities** (medium): registry descriptors for both (decided jointly with R8.1), unlocking rep-productivity reports in the P9 builder.
6. **Public API docs** (small-medium): hand-written OpenAPI spec for the registry + records + tasks endpoints, served with a swagger-ui page; PAT auth documented. (Tokens already ship; customers are flying blind.)
7. **Calendar + scheduler links** (large): .ics + Google/Outlook OAuth + bookable pages.
8. **Quotes/products/price books** (large; explicitly not payment processing).
9. **Automation object-generalization leftovers** (medium, from A2 deferrals): Run Now/dry-run beyond contact/deal (`entityKindForTrigger`), `no_activity_days` for all objects, configurable inbound-webhook field mapping.
10. **i18n + multi-currency-per-deal, SSO/SAML/SCIM, GDPR self-serve export** (large; enterprise-pull driven).

---

## Decisions needed from you (everything else proceeds without input)

1. **AI tier token limits** (R2.3) — real numbers per tier for `tierLimits`.
2. **R2 voice storage** — implement R2-bucket upload, or drop the env switch and accept ephemeral local storage for now?
3. **Railway "wait for CI checks"** (R3) — an ops toggle only you can flip.
4. **R9 ordering** — my default recommendation: 1:1 email + double-opt-in first (they compound the marketing investment you just made live in R1).
