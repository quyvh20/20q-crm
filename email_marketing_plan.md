# Email Marketing — Phased Plan

> **Status:** Planning. **Deploy rule (load-bearing, not defensive):** Resend is the only sender — reuse `pkg/mailer/resend_mailer.go` (transactional) and `internal/automation/executor_email.go` (`send_email`), never a second provider. `golang-migrate` is dead on prod (dirty at v2; numbered `migrations/*.sql` do **not** run), so **every new non-automation table or column is created by an idempotent `CREATE TABLE/ALTER … IF NOT EXISTS` boot guard in `cmd/server/main.go`**, gets an explicit `ENABLE ROW LEVEL SECURITY` line + an entry in the pg_class RLS sweep (never `FORCE`), and keeps a mirrored dev-only `migrations/*.sql`. Every unique index uses the probe-and-refuse ritual; every non-zero-default column carries a DDL `DEFAULT` (GORM omits zero-values on insert).
>
> **Every file path and line number cited below is a research pointer, not a guarantee — re-grep and confirm each one at implementation time.** Several cited line numbers are known to have drifted (e.g. `normalizeEmail` is at `ingest.go:720`, not `:712`; automation's `RegisterRoutes` signature is around `handlers.go:152-155`, not `:104-109`). The *substance* of each reference has been checked; the coordinates have not.
>
> Each phase (M1–M9) is independently deployable, ships legally, and never breaks in-flight data. **Three blocking spikes (B1–B3) gate the phases that depend on them and must be resolved before that phase's build starts — they are not "open decisions."**

---

## Why

The CRM can already send **one email at a time**. It cannot run a **marketing program**. Concretely:

**What exists (reuse, do not rebuild):**
- **Two Resend send paths.** `pkg/mailer/resend_mailer.go` `ResendMailer.send(ctx,to,subject,html)` for transactional (invite/reset/verify/security/digest); `internal/automation/executor_email.go` `EmailExecutor.sendEmail(...)` — the `send_email` workflow action with per-send `Idempotency-Key` (`runID/actionID`), 429/5xx→`NewRetryableError` vs 4xx-permanent classification, `cc`, `from_name`, 30s timeout. **This POSTs to `base+"/emails"` (single recipient) — it is NOT a batch endpoint.**
- **A5 reusable templates.** `internal/automation/email_template.go` + `handlers_email_templates.go` — org-scoped `automation_email_templates` (`BodyHTML` canonical send source, `BodyJSON` = TipTap doc for re-edit, `ObjectSlug` merge scope), soft-delete, case-insensitive unique name, gated by `domain.CapWorkflowsManage`. Frontend `EmailTemplateBodyEditor.tsx` (TipTap + `MergeTag` chip) + `mergeTagHtml.ts` `serializeMergeTags`.
- **Merge interpolation.** `template.go` `InterpolateTemplate` / `InterpolateTemplateHTML` — `{{path}}` over `contact/deal/trigger/org/user/actions`, HTML-escapes **merged values only** (authored markup passes through raw).
- **Durable automation engine.** `engine.go` worker pool + `FOR UPDATE SKIP LOCKED` (`LockAndGetRun`), idempotent `CreateRun`, version pinning, crash recovery (`RequeueInFlight`), retry sweeper, durable `wake_at` delays (`engine_wait.go`, `WakeDueWaitingRuns`), cron/date-field triggers (`scheduler.go`), enrollment fan-out (`executor_enroll_records.go` → `Engine.EnrollRun`).
- **Signed-webhook + async-processor patterns.** `integrations/facebook_webhook.go` (public route, IP limiter, `io.LimitReader`, verify-raw-bytes, `InsertEventDeduped`, strict ack taxonomy) + `webhook_processor.go` (`ClaimPendingEvents` poll/drain). `HealthReporter` (`integrations/health.go`) for off-hot-path alerts.
- **Reports P9.** `report_usecase.go` / `report_sql.go` / `report_runner_repository.go` — a parameterized, OLS/FLS-respecting SQL runner + live-preview endpoint to reuse for analytics.
- **SSE realtime.** `events.go` `EventsHandler.Stream` over `domain.OrgNotificationChannel` / `UserNotificationChannel`.

**What is entirely missing (this plan builds it):**
- A **marketing suppression + consent ledger** (unsubscribe / bounce / complaint / opt-in state) keyed on **normalized email**, separate from transactional email and separate from capture-side `integrations/consent.go` (which is record-only, `enforced=false`).
- A **pre-send chokepoint** that consults that ledger before every send. Today `sendEmail` sends unconditionally once `to` passes `isValidEmail`.
- **Per-org verified sending domains** (SPF/DKIM/DMARC) with an **aligned custom Return-Path stamped on the actual send**. Today the envelope address is the single global `MailFrom`; `from_name` only changes display text.
- **RFC 8058 one-click unsubscribe** + `List-Unsubscribe` headers. `resendEmailPayload` has only `From/To/Subject/HTML/Cc` — no headers field, zero `List-Unsubscribe` hits in the tree.
- **CAN-SPAM physical-address footer + preference center.** Body HTML is sent verbatim; no compliance layer.
- **Resend/Svix webhook ingestion** → auto-suppression + open/click/bounce/complaint **event ledger** + **complaint-rate auto-pause**.
- **Audiences** — static lists + dynamic segments (a stored, parameterized predicate).
- An **email-safe content editor** (compile-to-tables + inlined CSS; TipTap output is browser HTML, unsendable in Outlook) — **which introduces a JS-runtime infra dependency the Go image does not have today (see B2).**
- A **throttled bulk send engine** — a materialized per-recipient roster drained under Resend's configured request-rate cap. `enroll_records`/`find_records` silently cap at **100** (`record_service.go:165`, `executor_find_records.go:16`) and ignore `NextCursor`.
- **Campaign analytics + A/B testing** (MPP-aware opens, clicks as the headline metric).

---

## Blocking spikes (resolve before the gated phase builds)

These were previously mis-filed as "open decisions." Each is a hard exit criterion for the phase that depends on it. A phase does not start its build until its spike returns a decision.

- **B1 — Resend account limits & Return-Path (gates M2, M7).** Confirm, against the actual Resend account/tier, (a) the real API **request-rate limit** — Resend's *documented default is 2 requests/second*, raiseable on request; the plan must **never hard-code a number** and must size the shared token bucket from config; (b) whether a **custom aligned Return-Path** can be set per send and how (so DMARC aligns at send time, not just at domain-verify time); (c) daily/monthly quota headers (`x-resend-daily-quota`/`x-resend-monthly-quota`) and the 403-on-contact-quota behavior. Output: the configured rps ceiling, the Return-Path mechanism, quota-handling rules.
- **B2 — Email-HTML compile runtime (gates M6).** The Go backend image on Railway has **no Node**. Compiling authored blocks to email-safe nested-table + inlined-CSS HTML (MJML CLI / `mjml-go` / react-email) is a **real infrastructure lift**, not a code choice. Decide and stand up ONE of: (a) add Node to the backend container (build-time + image-size + ops cost), (b) `mjml-go` (embeds a JS runtime in the Go binary — evaluate size/perf), (c) a small sidecar/render service the backend calls, or (d) a pure-Go table-compiler for a constrained block set. **M6 cannot ship until this exists.** Output: the chosen runtime, provisioned in CI + the deploy image.
- **B3 — `List-Unsubscribe` DKIM coverage (gates M3 exit, therefore M7).** Gmail/Yahoo ignore a `List-Unsubscribe` header that is **not covered by the DKIM `h=` tag**. Confirm whether Resend `/emails` sends include custom headers in DKIM signing. **If yes:** M3/M7 proceed on `/emails`. **If no:** true marketing bulk must route through **Resend Broadcasts** (managed one-click unsubscribe) — which forces a merge-tag reconciliation (Broadcasts can't render CRM deal/company/custom-object tags; see B4 below) and changes the M7 send architecture. This must be answered **before M6/M7 are built**, or both may need rework.

---

## Guardrails (non-negotiable)

These must exist **before the first bulk send merges** (they land in M1–M4, ahead of the send engine in M7):

1. **Suppression is a hard gate, checked LIVE at claim/send time** — not at list-build time. The recipient set is filtered against the M1 ledger the moment each message is enqueued. A contact who unsubscribes between scheduling and a delayed send must be dropped.
2. **Every marketing message carries a DKIM-signed `List-Unsubscribe: <https://…>` + `List-Unsubscribe-Post: List-Unsubscribe=One-Click`** (value byte-exact, **confirmed inside DKIM `h=` coverage via B3**) **AND** a visible in-body unsubscribe link. Block the send if header injection or footer render fails. Header/footer injection applies **only to marketing-flagged sends** (see Guardrail 9) — transactional automation mail is never mutated.
3. **Domain authentication** — the sending domain passes SPF + DKIM + DMARC (aligned, `p=none` minimum) before any bulk send, **and the aligned custom Return-Path is stamped on the actual send** (B1), or DMARC fails at send despite a "verified" badge. Unverified domains cannot broadcast.
4. **CAN-SPAM footer** — the sending org has a configured, valid physical postal address rendered in every marketing message. Block send if absent.
5. **Positive, lawful basis per recipient** — every recipient has a recorded, still-valid basis to be mailed: explicit `subscribed`, an unexpired CASL implied-consent basis, or a documented existing-business-relationship / legitimate-interest basis (see M1). `pending`/`unconfirmed`/expired-CASL are excluded. **Absence of a suppression row is NOT consent.**
6. **Rate limits** — a **shared Redis** token bucket honors Resend's **configured** request-rate cap (from B1, default assumption 2 req/s until confirmed raised; **sized from config, never hard-coded**), plus a per-org fair-share bucket; 429 honors `Retry-After`/`ratelimit-reset` and backs off the **whole pool** with jitter. Per-process limiters are forbidden (scaling past one worker = 429 storm).
7. **Complaint-rate circuit breaker** — per-org complaint rate crossing **0.30%** auto-pauses that org's marketing lane; warn at **0.10%**. The pause target is an **org-level `marketing_paused` flag added in M4** (not the per-campaign status column, which does not exist until M7); transactional keeps flowing. This exists in M4, before the first send.
8. **Callerless-worker org isolation** — the bulk worker runs with no HTTP `Caller`, so OLS/FLS auto-enforcement does **not** apply. Every worker query carries an explicit `WHERE org_id = ?`, proven by a cross-tenant test.
9. **Marketing-vs-transactional scope is an explicit per-send signal.** The shared `send_email` executor is used by both transactional automations and marketing drips. A new per-action `channel` (`marketing` | `transactional`, default `transactional`) parameter on the send step decides whether the M1 suppression/consent gate + M3 header/footer injection apply. **Version-pinned in-flight runs carry no marketing flag, so their behavior is unchanged on resume** — the gate only activates for steps that explicitly set `channel=marketing`.

---

## Phase overview

| Phase | Name | Layer | Gates on |
|-------|------|-------|----------|
| **M1** | Suppression & consent ledger + `IsSendable()` chokepoint + `marketing.manage` cap | Backend + thin admin UI | — |
| **M2** | Per-org verified sending domain (SPF/DKIM/DMARC) + aligned Return-Path + `CanBulkSend()` | Backend + settings wizard | M1, **B1** |
| **M3** | One-click unsubscribe + preference center + CAN-SPAM footer + `List-Unsubscribe` headers | Backend + public pages | M1, **B3** |
| **M4** | Resend/Svix webhook ingestion → auto-suppression + event ledger + org-level complaint auto-pause | Backend | M1, M3 |
| **M5** | Audiences: static lists + dynamic segments | Backend + segment builder | M1 (OLS/FLS chokepoint) |
| **M6** | Email-safe content editor: block model → compile-to-email, preheader, merge fallbacks | Backend + composer | M3, **B2**, **B4** |
| **M7** | **Bulk send engine**: snapshot roster + throttled lane, every gate enforced | Backend + campaign UI | **M1, M2, M3, M4, M5, M6, B1, B3** |
| **M8** | Drip sequences over the automation engine + in-executor suppression | Backend + React Flow reuse | M1, M2, M3, M4, M5, M6 |
| **M9** | Campaign analytics + A/B testing (MPP-aware) | Backend + dashboards | M4, M7 |

> **B4 — Merge-context scope (gates M6/M7).** Pin per-campaign hydration scope (contact-only vs an explicitly declared deal/company/custom-object context) **before M6's composer and M7's per-recipient hydration are built**, or the hydration path reworks and subjects/bodies render blank tokens at scale. Sequenced as an M5/M6 precondition.

---

## M1 — Marketing foundation: suppression & consent ledger + pre-send chokepoint

### Problem
There is no authoritative do-not-mail state anywhere. `integrations/consent.go` is capture-side, event-keyed, and self-asserts `enforced=false`. The words "suppression" and "subscription" are already taken (automation enrollment suppression via `domain.WithAutomationSuppressed`; Paddle billing; `notification_preferences` is user-scoped). Before any send code exists, we need the ledger and the single gate everything passes through.

### Build
**Backend**
- New top-level package `internal/marketing` with `RegisterRoutes(router, protected []gin.HandlerFunc, requireCap func(string) gin.HandlerFunc)` — copy `internal/integrations/handlers.go` and pass the **full protected middleware slice**. *Rationale to confirm at build time:* integrations takes a full `[]gin.HandlerFunc` while automation's `RegisterRoutes` takes a single `authMiddleware`; before asserting this "silently skips PAT auth + workspace 2FA," inspect what the automation `authMiddleware` actually composes and mirror the integrations shape regardless (it is the safe superset). If any handler writes CRM records, `NewHandler` **panics on a nil authorizer** (mirror `integrations/handlers.go` OLS re-check).
- Add `CapMarketingManage = "marketing.manage"` in `internal/domain/role.go` in **all four spots**: the `Cap*` const, `AllCapabilities`, `CapabilityCatalog` (with label/group/`sensitive` so `role_catalog_test.go` stays 1:1), and `DefaultRoleCapabilities` for `RoleAdmin` + `RoleManager` (**never** `RoleOwner` — owner bypasses; an empty table must never lock it out). `SeedSystemRoles` backfills system roles on boot.
- `marketing.SuppressionGuard.IsSendable(ctx, orgID, emailNorm, channel, topicID) (bool, reason)` — the **sole** chokepoint. Key on normalized email (reuse `normalizeEmail`), never `contact_id`. The `channel` arg (`marketing` | `transactional`) is supplied by the caller (Guardrail 9). Semantics:
  - `channel=transactional` → consult only `scope=all` suppressions (hard bounce/complaint/global). Unsubscribes never block transactional mail.
  - `channel=marketing` → consult `scope=all` **and** `scope=marketing` suppressions, **and** require a positive lawful basis (below).
  - hard bounce / complaint → `scope=all`, immediate, permanent.
  - unsubscribe → `scope=marketing` only.
  - soft bounce → increment `soft_bounce_count`, suppress only after ~3 fails over ~14 days.
- **Lawful-basis model (not a boolean).** `contact_marketing_state.marketing_status` ∈ `{subscribed, unsubscribed, pending, cleaned}` **plus** a `consent_basis` ∈ `{express, double_opt_in, implied_transaction, implied_inquiry, existing_business_relationship, legitimate_interest}`. A marketing send is permitted when status is `subscribed` **or** a still-valid non-express basis exists (unexpired CASL implied; documented EBR/legitimate-interest). This is what lets an org lawfully mail its **own existing customers** without waiting on the deferred double-opt-in flow (see the surfaced dependency below).

**Frontend**
- New **Marketing** nav section gated by `usePermissions('marketing.manage')` with `AccessDeniedPanel`.
- Suppression admin: search/list by reason, manual add (immediate) + remove (logged); a per-contact marketing-status + consent-basis badge on the contact detail. No campaign UI yet.

### Data model
Both tables via `cmd/server/main.go` `IF NOT EXISTS` boot guard + explicit `ENABLE ROW LEVEL SECURITY` + pg_class sweep entry + mirrored dev-only `migrations/*.sql`. Never `FORCE`.
- **`marketing_suppressions`** — `org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE`, `email_normalized`, `reason ENUM[hard_bounce, soft_bounce, complaint, unsubscribe, manual, gdpr_erasure]`, `scope ENUM[all, marketing] DEFAULT 'marketing'`, `topic_id NULL`, `source`, `soft_bounce_count INT DEFAULT 0`, `created_at`.
  - **Uniqueness fix (critique):** a plain `UNIQUE(org_id, email_normalized, reason, topic_id)` will **not** dedupe topic-less rows — Postgres treats `NULL`s as distinct, so the common unsubscribe/bounce/complaint case (topic_id IS NULL) accumulates duplicates. Use **`UNIQUE(org_id, email_normalized, reason, COALESCE(topic_id, '00000000-0000-0000-0000-000000000000'::uuid))`** as a functional index (portable, no PG-version dependency), applied through the `idx_contacts_org_email` probe-and-refuse ritual. (`NULLS NOT DISTINCT` is PG15+ and would need a confirmed Supabase version; the COALESCE form avoids that risk.)
- **`contact_marketing_state`** — `org_id`, `email_normalized`, `contact_id NULL`, `marketing_status ENUM[...] DEFAULT 'pending'`, `consent_basis`, `consent_source`, `consent_at`, `consent_ip`, `region`, `casl_expires_at NULL`, `double_opt_in_at NULL`. `UNIQUE(org_id, email_normalized)` via probe ritual.

**Both tables are EXEMPT from `RedactForRecord` tombstoning** so an unsubscribe survives contact deletion, GDPR erasure, and CSV re-import.
**GDPR-erasure PII scope (critique):** the *suppression* row is retained in full (it is the minimum needed to keep honoring the opt-out). But on a GDPR erasure request, `contact_marketing_state` must **collapse to `email_normalized` + `marketing_status` only** — `consent_ip`, `region`, `consent_source`, `casl_expires_at`, `double_opt_in_at`, `contact_id` are nulled. Retaining full consent provenance after erasure exceeds the suppression-list exception. A dedicated `RedactMarketingStateForEmail(orgID, emailNorm)` performs this collapse, wired into the erasure path.
DDL `DEFAULT` on `scope`/`soft_bounce_count`/`marketing_status` because GORM omits zero-values on insert (the U5 `digest_only` trap).

### Surfaced dependency (was buried in "Deferred")
Double-opt-in confirmation is deferred (M1/M3 fast-follow), **but the lawful-basis model above is what prevents the "can mail almost no one" failure**: an org can mark its own existing customers with `consent_basis=existing_business_relationship`/`implied_transaction` at import and mail them under CAN-SPAM/CASL without a fresh opt-in. Cold, no-relationship imported lists still cannot be mailed until double-opt-in ships — that restriction is intentional and legally required, and is now stated here rather than hidden.

### Done when
- `IsSendable()` returns correct verdicts for both `channel` values (transactional never blocked by an unsubscribe; marketing blocked by `scope=all` or `scope=marketing` or missing lawful basis).
- Two topic-less suppressions for the same `(org,email,reason)` collapse to **one** row (COALESCE uniqueness proven).
- A GDPR erasure collapses `contact_marketing_state` to email+status while leaving the suppression row intact.
- Boot guard idempotent on re-run over a populated DB; RLS confirmed enabled.
- `marketing.manage` appears on Admin/Manager after boot and 403s otherwise; re-importing a suppressed address does not clear its suppression.
- **No send path exists anywhere in the codebase.**

### Traps
- Keying on `contact_id` leaks: `contact.email` is `*string`, case-**sensitive**-unique, multiple contacts can share one normalized address.
- A UNIQUE index over dirty prod data fails **silently** — the probe ritual is mandatory.
- Release-note that pre-existing **custom** admin-clone roles do **not** receive `marketing.manage` (`SeedSystemRoles` backfills system roles only — same caveat as `analytics.view`).

---

## M2 — Per-org verified sending domain (SPF/DKIM/DMARC) + aligned Return-Path + send gate

**Gates on B1** (Return-Path mechanism + account limits).

### Problem
Reputation is domain-first. A single shared platform `From:` domain pools every tenant's complaints/bounces — one bad actor tanks deliverability for all. SPF authenticates the **Return-Path**, not the visible `From:`; without an aligned send-subdomain Return-Path **stamped on the actual send**, DMARC fails even though SPF "passes."

### Build
**Backend**
- Resend **Domains API** client: create domain; read back SPF (TXT + MX), `resend._domainkey` DKIM CNAME, DMARC records; poll status over the 72h window.
- Recommend a dedicated **send subdomain** (e.g. `send.customer.com`) for an aligned custom Return-Path, **plus** a separate branded **tracking subdomain** (CNAME aligned to the from-domain) so click-redirects don't read as phishing.
- **Return-Path binding (critique):** the from-address resolver returns the org's verified domain **and the send path (M7/M8) must explicitly stamp the aligned custom Return-Path per B1's mechanism** — verifying the domain does not itself make the send aligned. Add a `CanBulkSend` sub-check `hasAlignedReturnPath(org)` and a send-time assertion.
- `HasVerifiedSendingDomain(org)` / `CanBulkSend(org)` return true only when SPF + DKIM + DMARC (`≥p=none`) verify **and** the aligned Return-Path is configured.
- Warmup stance: cap a freshly verified domain's initial daily marketing volume, or ride Resend's pre-warmed shared pool with a capped initial rate. **Do not** provision dedicated IPs below ~90k/mo. Seal any per-domain secret with the envelope codec (never plaintext, never in a URL). Respect `MAIL_DISABLED` (no-op, not error).
- **Reputation-isolation caveat (critique):** per-org custom domains isolate **domain** reputation, but on Resend's shared pool + one Resend team, **IP/pool reputation is shared** — a noisy tenant still affects pool deliverability. The M4 per-org complaint breaker limits blast radius; it does not isolate IP reputation. Do not oversell "reputation-isolated." True isolation needs separate Resend teams/subaccounts (Open decision) or dedicated IPs (deferred, ~90k/mo+).

**Frontend**
- Domain onboarding wizard: enter domain, render exact copyable DNS records, live re-check (`not_started`/`pending`/`verified`/`partially_verified`/`failed`), an explicit "marketing blocked until verified" state, DMARC `p=none`→tighten guidance.

### Data model
- **`org_email_domains`** — `org_id`, `domain`, `send_subdomain`, `tracking_subdomain`, `return_path`, `resend_domain_id`, `spf_verified BOOL DEFAULT false`, `dkim_verified BOOL DEFAULT false`, `dmarc_policy`, `status ENUM[...] DEFAULT 'not_started'`, `verified_at NULL`, `warmup_daily_cap INT NULL`. Boot guard + `ENABLE RLS` + sweep + dev mirror; `UNIQUE(org_id, domain)` via probe ritual; DDL `DEFAULT`s on all bool/enum columns.

### Done when
- An org adds a domain, sees records, reaches `verified`; `CanBulkSend` flips true only when all three pass **and** the aligned Return-Path is set; a test send stamps the verified domain **and** the aligned Return-Path (asserted). Still no send path.

### Traps
- SPF authenticates the Return-Path, not the visible `From:` — a "verified" domain still fails DMARC at send if the Return-Path isn't stamped.
- `from_name` must not spoof an unverified domain (envelope stays global `MailFrom` until verified).
- Resend domains are team-global — two orgs claiming the same domain string collide; enforce org ownership.
- DKIM keys are 1024-bit (satisfies Gmail/Yahoo).

---

## M3 — One-click unsubscribe + preference center + CAN-SPAM footer + `List-Unsubscribe` headers

**Gates on B3** (DKIM coverage of custom headers) — **B3 is a hard M3 exit criterion, not a parallel open question.**

### Problem
The legally-mandatory recipient controls don't exist, and `resendEmailPayload` has no headers field. These must ship before any send.

### Build
**Backend**
- Extend `resendEmailPayload` with a `Headers map[string]string` so **marketing-flagged** sends carry `List-Unsubscribe: <https://app/u/{opaque}>` + `List-Unsubscribe-Post: List-Unsubscribe=One-Click` (value **byte-exact**). **B3 decides the path:** if Resend signs custom headers in DKIM `h=`, proceed on `/emails`; if not, route true marketing bulk through Resend Broadcasts' managed unsubscribe and reconcile merge-tag limitations (B4). Injection applies **only when `channel=marketing`** (Guardrail 9) — transactional sends are untouched.
- Mint per-recipient **opaque HMAC tokens** (contact+campaign, **no PII/email in the URL**).
- **PUBLIC unauthenticated** `POST /api/marketing/u/:token` (mounted like the capture routes, no auth middleware): accepts body `List-Unsubscribe=One-Click`, writes `marketing_suppressions(reason=unsubscribe, scope=marketing)` **synchronously**, returns 200/202, **no redirect, no confirmation screen**. `GET /u/:token` serves the hosted preference/topic center.
- **Token privacy decision (critique):** stateless HMAC tokens are valid indefinitely (good — CAN-SPAM needs 30+ days) but a **forwarded** email hands the `GET` preference center (per-topic subscription state) to any token-bearer. Decision: the **POST one-click action** stays token-only (must work without auth per RFC 8058). The **GET preference center does NOT display existing per-topic state to an unauthenticated token-bearer** — it offers a global unsubscribe + topic *opt-down* controls without revealing current subscriptions, or requires a lightweight email-confirmation step to reveal state. Log this as the chosen posture.
- **Required org sender profile** (physical address + from identity) — marketing send **blocks** if absent.
- **Render-time footer + preheader injector** composes address + visible unsubscribe link **without mutating stored `BodyHTML`**, and **only for `channel=marketing`** (keeps templates reusable by transactional workflows and never alters in-flight non-marketing runs).
- Optional `marketing_topics` (`opt_in_default` **immutable** after creation); consent provenance derives `casl_expires_at`.

**Frontend**
- Public unauthenticated preference-center page (topic opt-down + global unsubscribe, no state-leak per the decision above).
- Marketing sender-profile settings form (postal address, reply-to) with a "required before sending" banner.
- Consent basis + CASL expiry surfaced read-only on the contact record.

### Data model
- **`org_marketing_profile`** — `org_id PK`, `physical_postal_address`, `from_name`, `reply_to`, **`marketing_paused BOOL DEFAULT false`** (the M4 circuit-breaker's org-level pause target — see M4).
- **`marketing_topics`** (optional) — `org_id`, `name`, `opt_in_default BOOL`.
- Both via boot guard + `ENABLE RLS` + sweep + dev mirror; DDL `DEFAULT`s.

### Done when
- **B3 answered.** A crafted marketing send carries both `List-Unsubscribe` headers within DKIM `h=` coverage (or the Broadcasts fallback path is chosen and specced).
- An unauthenticated POST to a fresh token suppresses idempotently, no auth/redirect/confirmation, returns 200; the GET center does not leak per-topic state.
- Footer + address render at send time for marketing sends only, and are **absent** from the stored body and from transactional sends.
- A marketing campaign cannot be enabled for an org with no postal address. Still no bulk engine.

### Traps
- A **GET** that unsubscribes lets link scanners opt users out — POST-only for the action.
- `List-Unsubscribe-Post` must be byte-exact and DKIM-covered or Gmail/Yahoo silently ignore it (B3).
- Footer/preheader must be **render-time**, not stored, and marketing-scoped (prevents drift, double-footers, and mutating transactional/in-flight runs).
- `integrations/consent.go` is capture-side, `enforced=false` — do not treat it as suppression.

---

## M4 — Resend/Svix webhook ingestion → auto-suppression + event ledger + org-level complaint auto-pause

### Problem
The suppression loop must close and the reputation circuit-breaker must exist **before** the first send. Resend signs with **Svix**, not Facebook's `X-Hub-Signature-256`.

### Build
**Backend**
- **PUBLIC unauthenticated** `POST /api/marketing/webhooks/resend`, mounted like the capture routes.
- Read body **once** via `io.ReadAll(io.LimitReader(body, 1<<20))` **before any binding**; verify a **fresh Svix scheme**: base64 HMAC-SHA256 over `{svix-id}.{svix-timestamp}.{rawBody}`, `whsec_` secret, timestamp-tolerance replay check, space-delimited multi-signature header for rotation. **Do not** point `facebook.go`'s hex verifier at it.
- Any skip hatch gates on `skipSignatureAllowed()` appEnv allowlist (`development`/`test`), **default-closed** — `APP_ENV` is unset on prod, so `!= "production"` fails **open**.
- **Dedupe on `svix-id`** (delivery is at-least-once, unordered).
- Async consumer copies `webhook_processor.go` (`ClaimPendingEvents` + `FOR UPDATE SKIP LOCKED` + retry taxonomy): hard bounce/complaint/`suppressed` → `marketing_suppressions scope=all` immediate; soft bounce → increment `soft_bounce_count`, suppress after ~3/14d; unsubscribe → `scope=marketing`.
- **Ack taxonomy:** 200 = received, 401 = bad-sig, 503 = transient (Resend redelivers).
- Resolve org from the endpoint→domain/campaign mapping **defensively** (no caller on a public route; drop + loud-log + ack when no org owns the event).
- **Circuit breaker with a real pause target (critique):** per-org complaint-rate + bounce-rate rollup; **warn at 0.10%, auto-pause at 0.30% by setting `org_marketing_profile.marketing_paused = true`** (introduced in M3, exists at M4). The M7 claim query later ANDs `NOT marketing_paused`; transactional keeps flowing. This makes M4 independently deployable with a persistent pause target. Add a `Feedback-ID` header spec for later per-campaign Gmail complaint attribution.

**Frontend**
- None required for MVP (the M1 suppression list auto-grows). Optional: raw-events debug view + a per-org deliverability tile (complaint/bounce vs the 0.1%/0.3% lines) + a "marketing paused" banner.

### Data model
- **`marketing_email_events`** — `org_id`, `svix_id`, `email_id`, `campaign_id NULL`, `recipient_email_normalized`, `type`, `occurred_at`, `bounce_type`, `link_url`, `raw JSONB`. Model as an **owner-less, org-level** store (**no `owner_user_id`**) so a later Reports `rowPredicateFor` returns org-scoping only (like `companies`). Boot guard + `ENABLE RLS` + sweep + dev mirror; **`UNIQUE(org_id, svix_id)`** via probe ritual + `INSERT … ON CONFLICT DO NOTHING` for at-least-once dedupe; DDL `DEFAULT`s. Consider month-partitioning at volume.

### Done when
- A signed Resend test event verifies, inserts once; a redelivery of the same `svix-id` is a no-op.
- A hard-bounce writes a `scope=all` suppression the M1 chokepoint then blocks.
- A complaint spike crossing 0.3% sets `marketing_paused` on the org (transactional unaffected).
- An unsigned request 401s and never writes; a DB outage 503s so Resend redelivers.

### Traps
- Verifying a re-marshalled body breaks the HMAC — raw bytes only, never `ShouldBindJSON` first.
- Pointing Facebook's hex verifier at Svix fails.
- `!= "production"` fails **open** — default-closed.
- Events are unordered + at-least-once (don't assume `opened` after `delivered`).
- No public Resend suppression-remove API — this local ledger is authoritative; treat Resend suppression as advisory.

---

## M5 — Audiences: static lists + dynamic segments

### Problem
Campaigns need a safe, parameterized, OLS/FLS-respecting "who." There is no segment/list entity, no reverse tag index, and `custom_fields` is currently only exact-match filtered.

### Build
**Backend**
- A **whitelisted field catalog** maps each allowed field key to a physical `contacts` column or a typed `custom_fields ->> 'key'` path (incl. attribution keys `lead_source`/`utm_*`, handling the `crm_`-prefixed collision variant), its type, and its allowed operators. **This registry is both the SQL-injection defense and the FLS boundary.**
- Compile the boolean AST to **fully parameterized SQL** (bind args only, never interpolate field names/values; reuse the `buildReportSQL` whitelist/regex/bind discipline), always ANDing `org_id=$ AND deleted_at IS NULL AND RecordAccessPredicate`. Tag leaves → `EXISTS` on `contact_tags`.
- Endpoints (gated `marketing.manage`): CRUD `/api/marketing/segments` (validate AST vs catalog), `GET :id/preview?limit=50`, `GET :id/count` (cached, short TTL).
- Materialization is **opt-in** via a plain `marketing_segment_members` table with incremental upserts — **not** `CREATE MATERIALIZED VIEW`. Engagement predicates (opened/clicked) deferred until M9 events accumulate; require a time window when added.

**Frontend**
- Segment builder: boolean AND/OR/NOT tree over catalog fields/tags/custom-fields with a live **cached** count badge + sample-row preview; static-list membership management (usable as a CSV-import target whose upsert **never** overwrites consent or clears suppression). Reuse existing contact-filter chrome.

### Data model
- **`marketing_segments`** — `org_id`, `name`, `type ENUM[static, dynamic]`, `definition JSONB`, `materialized BOOL DEFAULT false`, `count_cached`, `count_cached_at`, `refreshed_at`, `created_by`, `deleted_at`.
- **`marketing_segment_static_members`** — `segment_id`, `contact_id`, `source`, `PK(segment_id, contact_id)`.
- **`marketing_segment_members`** — `segment_id`, `contact_id`, `matched_at`, `PK(segment_id, contact_id)`.
- All via boot guards + `ENABLE RLS` + sweep + dev mirrors; DDL `DEFAULT`s.
- **Add the missing reverse index** `CREATE INDEX ON contact_tags(tag_id, contact_id)` (current PK is `(contact_id, tag_id)` only); add `GIN(custom_fields jsonb_path_ops)`; optionally promote hot `custom_fields` keys to `STORED` generated columns.

### Done when
- A dynamic segment compiles to fully parameterized SQL and previews/counts correctly respecting OLS/FLS/own-scope.
- A hidden (FLS) field cannot be filtered on; tag predicates use the new reverse index; static lists hold explicit membership; count badges are cached.

### Traps
- Interpolating `custom_fields` keys/values = injection **and** FLS bypass — catalog + bind args only.
- Bare `lead_source` misses orgs with the `crm_`-prefixed field.
- Unbounded "ever opened" predicates scan the events table — require a window/rollup.
- `count(*)` on every badge is expensive — cache with TTL.

---

## M6 — Email-safe content editor: block model, compile-to-email, preheader, merge fallbacks

**Gates on B2** (JS compile runtime provisioned) **and B4** (merge-context scope pinned).

### Problem
TipTap output is browser HTML (semantic `div`/`p`/`span`), not nested-table + inline-CSS email HTML — it breaks Outlook (Word engine: no flex/grid/float) and degrades in Gmail (all-or-nothing `<head><style>`). Shipping `BodyHTML` verbatim is unsendable at marketing quality. **The compiler is a JS runtime the Go image lacks — B2 must land first.**

### Build
**Backend**
- Store a structured **block/document JSON** model and **compile to email-safe nested-table + fully-inlined-CSS HTML at send time** via the **B2-provisioned runtime** (MJML CLI / `mjml-go` / react-email / pure-Go compiler). The compile step is load-bearing — never send TipTap/contenteditable HTML to an inbox.
- Dedicated **preheader** field injected as a hidden top-of-body span (`display:none;max-height:0;overflow:hidden;mso-hide:all;opacity:0` + zero-width padding — never color-based hiding).
- Merge tags require a **mandatory fallback** per variable (validated at save so no blank or literal `{{token}}` lands in a subject/preheader), usable in subject + preheader + body; keep `InterpolateTemplateHTML`'s value-escaping.
- **Merge-context scope pinned per B4:** a contact-scoped blast hydrates contact/org/user only, OR the campaign explicitly declares a hydrated deal/company/custom-object scope. **Reject merge tags outside the declared scope at save** so nothing renders blank at scale.
- Non-removable footer block (M3 address + unsubscribe). Emit `color-scheme`/`supported-color-schemes` meta + explicit colors for dark mode; auto-derive a plain-text multipart alternative; guard compiled HTML **<100KB** (Gmail clips >102KB).
- Pre-send **lint** endpoint (SpamAssassin score, image:text ratio, missing alt, broken/shortened links, HTML size, unsubscribe + address presence, `List-Unsubscribe` headers wired).

**Frontend**
- Block-based composer reusing `EmailTemplateBodyEditor.tsx` / TipTap + `MergeTag` chip as the **text authoring surface only** (hero/section/button/image/divider/columns/footer blocks; each new node adds its own `serializeMergeTags`).
- Merge-tag chips with fallback UI on subject + preheader + body, a dark-mode preview toggle, a pre-send checklist gate, and a real test-send (reuse `engine.SendTestEmail`, **no** idempotency key so every click delivers).

### Data model
- **`marketing_campaign_content`** — `org_id`, `name`, `subject`, `preheader`, `body_json`, `body_html_compiled`, `merge_scope`, `created_by`, timestamps. A **new marketing table**, **not** an ALTER of `automation_email_templates` (GORM ALTER/AutoMigrate fails silently on prod only). Boot guard + `ENABLE RLS` + sweep + dev mirror; DDL `DEFAULT`s.

### Done when
- **B2 runtime is live in CI + the deploy image; B4 scope is pinned.**
- Authored content compiles to <100KB table-based inlined HTML that renders in Outlook/Gmail/dark-mode previews.
- The preheader shows and isn't scraped from body; a merge tag with no value renders its fallback (never a blank subject or literal token); footer is non-removable; a plain-text alternative is generated; merge tags outside the declared scope are rejected at save.

### Traps
- TipTap output is browser HTML, unsendable in Outlook if shipped directly — compile is mandatory, and it needs a JS runtime the base Go image does not ship (B2).
- Any new content node (image/button/link) needs its own serialization or wrappers leak into the sent HTML.
- Adding preheader/variant **columns** to the existing template table via GORM ALTER fails silently on prod — use a new boot-guarded table.
- Unassisted subject merge tags need the same fallback validation as body.

---

## M7 — Bulk send engine: snapshot roster + throttled lane, every gate enforced

**Gates on M1–M6, B1, B3.**

### Problem
The first send-capable phase — and by construction legally shippable. `enroll_records`/`find_records` silently cap at 100 and ignore `NextCursor`; the automation engine's queue is per-run, not fan-out-to-N; there's no shared rate limiter and no live suppression at send.

### Build
**Backend**
- **Fan-out** = one cursor-paginating `INSERT … SELECT` snapshotting the segment/list union **minus** exclusions **minus** suppressions into `marketing_campaign_recipients`, `ON CONFLICT DO NOTHING` (structural cross-segment dedupe on `lower(email)`) — loop `RecordService.List` `NextCursor` to **fix the 100-record cap**. The roster is the **sole durable authority** for send state, dedup, progress, resume, and pause (never Redis/memory).
- **Send path (critique — sendEmail is single-`/emails`, and batch has no per-message idempotency):**
  - **Default lane = single `/emails` per recipient**, reusing `EmailExecutor.sendEmail` with a deterministic `Idempotency-Key = campaign:<id>:contact:<rosterRowID>`. This preserves the **"roster + idempotency key = no double-send"** guarantee across lost-ack retries and worker restarts (Resend replays the cached response for 24h; the roster row is the durable authority beyond that).
  - **Optional batch lane = `/emails/batch` (≤100/call)** is a **new code path** (a new batch payload builder — `sendEmail` and the `From/To/Subject/HTML/Cc`-shaped `resendEmailPayload` cannot be reused as-is). Because the batch endpoint honors **only a per-request** idempotency key (not per-message), a lost-ack after Resend accepted a 100-message batch would re-send all 100 on reaper retry. Therefore batch is **opt-in** and used **only** where a double-send window is acceptable, or gated behind a post-accept confirmation write; the default remains single-send. **The plan does not claim batch + exactly-once simultaneously.**
- Dedicated marketing send-lane workers: `UPDATE … FOR UPDATE SKIP LOCKED WHERE status='pending' AND scheduled_for<=now() AND campaign.status NOT IN (paused,canceled) AND NOT org.marketing_paused`; **LEFT JOIN M1 suppression LIVE at claim** with `channel=marketing` (membership frozen, mailability live — mark `suppressed` instead of sending); inject M3 headers/footer; resolve M2 verified from-address **and stamp the aligned Return-Path (B1)**.
- A **shared Redis token bucket** `rl:provider` sized from **config per B1** (Resend default 2 req/s until confirmed raised — **not hard-coded**) + a per-org fair-share bucket `rl:tenant:<org>`; on 429 honor `Retry-After`/`ratelimit-reset` by backing off the **whole pool** with jitter.
- Per-recipient retry classification (429/5xx/timeout → `pending` + backoff, ≤5 tries; other 4xx → `failed`/`suppressed`) **isolated per recipient**, not the step's all-or-nothing model.
- A **reaper** resets stuck `processing` rows past a lease (crash recovery). On the single-send lane this is safe (idempotency key); on the batch lane it carries the acknowledged double-send window above.
- **Hard pre-send launch gates that block:** verified sending domain + aligned Return-Path (M2/B1), org postal address (M3), `List-Unsubscribe` + footer wired + DKIM-covered (M3/B3), per-recipient positive lawful basis (M1), segment resolved (M5), content compiled + lint-clean (M6), org not `marketing_paused` (M4).
- **Callerless worker** ⇒ explicit `WHERE org_id=?` in **every** query, covered by a cross-tenant test.
- Live progress = raw JSON published to the org SSE channel (`type:'campaign_progress'`), **not** a `Notification` row per tick. Add a prune job for completed roster rows.

**Frontend**
- Campaign composer: pick segments/exclusions, choose M6 content + topic, schedule-or-send-now, recipient-lock (snapshot at schedule) vs determine-at-send; a pre-send checklist that must be all-green; a slide-to-confirm send guard; a live progress bar via SSE with pause/cancel/drain driven by the campaign status column.

### Data model
- **`marketing_campaigns`** — `org_id`, `name`, `content_id`, `segment_ids`, `exclude_segment_ids`, `sending_domain_id`, `topic_id NULL`, `status ENUM[draft, scheduled, sending, paused, sent, canceled] DEFAULT 'draft'`, `send_lane ENUM[single, batch] DEFAULT 'single'`, `scheduled_at NULL`, `recipient_lock_mode`, `feedback_id`, `snapshot_counts`, `created_by`.
- **`marketing_campaign_recipients`** (critique — reconcile the PK so email-only imported recipients are representable): a surrogate **`id UUID PRIMARY KEY`**, `campaign_id`, `contact_id NULL` (nullable — an imported email may have no contact row), `email_normalized NOT NULL`, `variant NULL`, `status ENUM[pending, processing, sent, failed, suppressed, skipped] DEFAULT 'pending'`, `attempts INT DEFAULT 0`, `next_attempt_at`, `scheduled_for`, `locked_at`, `provider_message_id`, `idempotency_key`. **Dedup key = `UNIQUE(campaign_id, email_normalized)`** (the real cross-segment/cross-import dedupe, works whether or not `contact_id` is set). This replaces the invalid `PK(campaign_id, contact_id)` (which could not represent an email-only recipient and NULL-collided in a PK).
- Boot guards + `ENABLE RLS` + sweep + dev mirrors; UNIQUE via probe ritual; DDL `DEFAULT`s.

### Done when
- A 5,000-recipient segment fans **fully** into the roster (not truncated at 100), including email-only imported members with no contact row.
- The single-send lane drains under the **configured** rps cap with no double-send across overlapping segments, and survives a killed-worker mid-batch with no re-send (idempotency key + roster status).
- The batch lane, if used, is documented as carrying a bounded double-send window on lost-ack retry.
- A mid-send unsubscribe is dropped at claim; an auto-paused (0.3%) or manually paused campaign/org halts the claim query.
- A cross-tenant test proves no worker query leaks across `org_id`.
- The engine never sends from an unverified domain / without an aligned Return-Path, or to an unconsented/suppressed recipient.

### Traps
- Reusing `enroll_records`/`find_records` silently caps at 100 — must paginate `NextCursor`.
- A per-process (not Redis) or hard-coded-number rate limiter blows the configured cap into a 429 storm.
- Suppression checked only at snapshot (not live) mails people who unsubscribed after scheduling.
- **Single-send:** a reused idempotency key silently drops every recipient but the first; keys expire after 24h — the roster row is the durable authority. **Batch:** no per-message key exists — do not assume idempotent retry there.
- Batch API does **not** support attachments — single-send fallback.
- The callerless worker has no OLS/FLS safety net — one missing `WHERE org_id=?` is a cross-tenant send primitive.

---

## M8 — Drip sequences over the automation engine + in-executor suppression

### Problem
Multi-step time-based nurture genuinely needs durable inter-step delays — the automation engine's strength (`wake_at` parks a run without holding a worker). But bulk entry hits the 100-cap, depth-2 ceiling, and jobs-channel overflow; and opt-out provably cannot be enforced at trigger time.

### Build
**Backend**
- A drip = an automation workflow (`delay` + `send_email` steps) where the `send_email` steps set **`channel=marketing`** (Guardrail 9); durable `wake_at` parks each run.
- Bulk entry = a **cursor-paginating throttled feeder** that resolves the M5 segment and calls `Engine.EnrollRun` in **bounded batches** (never the 100-capped `enroll_records` action), enrolling at **depth 0** so the sequence's own internal enroll steps keep the depth-2 headroom; feed at a rate that does not overflow the jobs channel (buffer=100), leaning on the stranded-pending sweep as backstop. Per-`(sequence, recipient)` enroll idempotency key.
- **CRITICAL — scoped in-executor gate (critique):** enforce M1 marketing suppression + positive lawful basis **inside the `send_email` executor, but only when the step carries `channel=marketing`**. Because `fireTimerRun` and the `date_field` materializer **provably ignore payload flags**, opt-out cannot be enforced at enrollment/trigger time; a contact who unsubscribes mid-sequence must be skipped at the **send step**. **Transactional automation sends (no `channel=marketing`) are unaffected**, and **version-pinned in-flight runs created before this change carry no marketing flag, so their behavior on resume is unchanged** — the gate cannot start silently skipping or footer-mutating legitimate transactional or pre-existing runs.
- Reuse the same shared config-sized rps + per-org bucket, verified from-domain + aligned Return-Path, and header/footer injector as M7. Add a **prune** for completed marketing runs + action logs (none exists today).

**Frontend**
- Sequence builder reusing the existing React Flow automation builder (`delay` + `send` steps), an "enroll a segment" entry action, and an enrolled/active/completed recipient view.

### Data model
- **No new send tables** — reuses `automation_workflow_runs`/`automation_workflow_action_logs` (AutoMigrate lane) + a small **`marketing_sequence_enrollments`** (`org_id`, `sequence_workflow_id`, `segment_id`, `feeder_cursor`, `status`, `created_at`) tracking table via boot guard + `ENABLE RLS` + sweep + dev mirror; DDL `DEFAULT`s.

### Done when
- A segment of thousands enrolls fully in batches without hitting 100 or overflowing the channel.
- Delays park runs (no worker held).
- A contact who unsubscribes mid-sequence is skipped at the send step (not just at entry).
- A **transactional** automation send and any pre-existing version-pinned run are provably unaffected by the new marketing gate.
- Depth-2 headroom remains for in-sequence enroll steps; completed runs + logs are pruned.

### Traps
- Run + action-log row volume at thousands × steps is heavy and previously un-pruned — add the prune.
- `isEnrollmentSuppressed` is deliberately anti-bulk — sequences must use the explicit feeder, never natural triggers.
- Marketing opt-out **must** live in the send executor (behind the `channel=marketing` flag) or unsubscribed contacts get mailed — and, without the flag scoping, legitimate transactional sends would be blocked.
- `send_email` needs a stable step ID or it gets no `Idempotency-Key` and retries double-send.

---

## M9 — Campaign analytics + A/B testing

### Problem
The M4 event ledger has no metrics layer, and there's no A/B testing. Apple MPP preloads every tracking pixel on delivery, inflating opens ~4pp and corrupting CTOR / send-time / winner selection.

### Build
**Backend**
- Derive **all** metrics at query time from `marketing_email_events` (unique delivered/open/click/bounce/complaint/unsubscribe per recipient) — **never persist rates** so the MPP filter stays recomputable.
- Flag Apple MPP opens (Apple proxy UA/IP ranges, opens firing at delivery time) and report opens-excluding-machine alongside raw; **headline clicks + conversions**, treat opens as directional only.
- Reuse the Reports P9 per-viewer runner discipline; because `marketing_email_events` is owner-less, `rowPredicateFor` returns org-scoping only (no per-viewer rollup bug).
- Attribution: append campaign-level UTMs (`utm_medium=email`) via a per-link template; capture UTMs on landing/lead-capture into contact `custom_fields` for click→deal→revenue ROI. Default to **campaign-level** (non-recipient-identifying) UTMs.
- **A/B:** test exactly one variable (subject/preheader OR content); send a configurable test fraction (default 15–20%) across two roster cells (reuse the `variant` column on `marketing_campaign_recipients`; idempotency key encodes `variant+rosterRowID`); wait a bounded window (4–24h; longer for send-time); pick the winner at **≥95% confidence** via a real significance test — **open rate** for subject/preheader, **clicks** for content (MPP-aware) — then fan the winner to the remainder over the **same M7 single-send lane**, respecting the same M1–M4 gates + live suppression. Warn when a cell is too small to reach significance rather than silently picking.

**Frontend**
- Per-campaign dashboard (delivered / opened [MPP include/exclude toggle] / clicked / bounced / complained / unsub, unique vs total, timeline) reusing Reports P9 chart primitives + the **dataviz** skill.
- Per-org deliverability health card (complaint rate vs the 0.1%/0.3% lines, bounce rate, suppression growth, marketing-paused state).
- A/B setup UI (single variable, split %, winner metric, test window) with a live significance readout, a "cell too small" warning, auto-send-winner controls; a distinct editor mount key per variant to prevent cross-contamination.

### Data model
- **`marketing_campaign_variants`** — `campaign_id`, `variant`, `subject`, `preheader`, `content_override`, `split_pct`, `winner_metric`, `is_winner`.
- The `variant` column on `marketing_campaign_recipients` is defined in M7's DDL (boot-guarded), **not** a GORM ALTER (fails silently on prod).
- Boot guard + `ENABLE RLS` + sweep + dev mirror; DDL `DEFAULT`s.

### Done when
- A completed campaign shows unique open/click/bounce/complaint computed idempotently from raw events with an MPP toggle.
- A subject A/B sends test cells, computes a significant winner (or warns it cannot), and auto-sends the winner to the remainder from the existing roster without double-sending.
- The winner send respects the same M1–M4 gates and live suppression re-check as M7.

### Traps
- Counting MPP image preloads as real opens inflates opens ~4pp and picks the wrong A/B winner — headline clicks.
- Non-idempotent event handling (if M4 dedupe fails) double-counts every rate.
- Modeling analytics **with** an owner column hides org rollups from row-scoped viewers.
- Test cells too small never reach significance.
- Recipient-identifying UTMs are personal data — default to campaign-level UTMs.

---

## Cross-cutting concerns

- **Deliverability.** SPF+DKIM+DMARC alignment **plus an aligned custom Return-Path stamped at send** per org (M2/B1), one-click `List-Unsubscribe` (M3, DKIM-covered per B3), per-tenant complaint rate held <0.10% target / <0.30% hard cap with org-level auto-pause (M4), email-safe compiled HTML <100KB + plain-text multipart (M6), and per-domain warmup (ride Resend's pre-warmed shared pool; no dedicated IPs below ~90k/mo) span every phase and gate M7/M8. **Shared-pool + single-Resend-team means IP/pool reputation is shared across tenants — per-org domains isolate *domain* reputation only; true IP isolation needs separate Resend teams/subaccounts or dedicated IPs.**
- **Suppression & consent.** One email-keyed ledger (M1) is the single authority, fed by webhooks (M4) and the one-click endpoint (M3), consulted **live at claim time** in the send lane (M7) **and inside the `channel=marketing`-flagged `send_email` executor** for drips (M8) — never at enrollment/snapshot time. Suppression rows are exempt from `RedactForRecord` (survive deletion/erasure/re-import); `contact_marketing_state` collapses to email+status on GDPR erasure (PII dropped). Lawful-basis gate (subscribed / valid CASL implied / EBR / legitimate-interest), not merely absence of suppression; cold no-relationship imports remain unmailable until double-opt-in ships.
- **Tracking.** Raw per-recipient events (M4) are the immutable source; rates are always derived at query time (M9) so MPP filtering stays recomputable; opens are directional, clicks are the headline; a branded aligned tracking subdomain (M2) keeps click-redirect reputation clean.
- **Tenancy / RLS.** Everything org-scoped; the callerless bulk worker gets no automatic OLS/FLS, so every query carries an explicit `WHERE org_id=?` enforced by a cross-tenant test; segments resolve through the RecordService/authz chokepoint (M5). Every new marketing table (marketing is **not** the automation package — no AutoMigrate exemption) gets a `main.go` boot guard + explicit `ENABLE ROW LEVEL SECURITY` + pg_class sweep entry + a mirrored dev-only `migrations/*.sql`; never `FORCE`; every unique index uses the probe-and-refuse ritual; later column additions also go through boot guards.
- **Shared executor safety.** M3's header/footer injection and M8's suppression/consent gate live in the shared `send_email` executor but activate **only for `channel=marketing`** sends. Transactional automations and version-pinned in-flight runs (which carry no marketing flag) are provably unaffected on resume — no silent skipping or mutation of non-marketing mail.
- **Resend webhooks.** Svix-signed (base64 HMAC over `{id}.{timestamp}.{rawBody}`, `whsec_` secret, timestamp tolerance, rotation-aware) — verified over **raw bytes**, deduped on `svix-id`, processed off the request path via a `webhook_processor.go`-style claim/drain loop, org resolved defensively from the endpoint→domain mapping.
- **Rate limiting.** A shared Redis token bucket honors Resend's **configured** request-rate cap (B1; default 2 req/s until confirmed raised, **sized from config**) with a per-org fair-share bucket; 429 backs off the whole pool. Documented escape hatch: split high-volume tenants onto separate Resend teams/subaccounts before the shared bucket becomes a throughput ceiling.

---

## Deferred / out of scope

- **Double-opt-in confirmation flow for cold-imported audiences** — required for GDPR/CASL cold, no-relationship lists; M1/M3 fast-follow. **Note:** existing-customer lists are NOT blocked on this — the M1 lawful-basis model (EBR / implied-transaction) lets orgs mail their own customers immediately; only cold no-relationship imports wait for this phase.
- **Dedicated sending IPs / IP-warmup automation** — not worth it below ~90k emails/month; ride Resend's managed pre-warmed shared pool.
- **Separate Resend teams/subaccounts per high-volume tenant** — the only real IP/pool-reputation isolation; layer in once a tenant's volume or risk warrants it (also relieves the shared rps bucket).
- **BIMI logo** — requires DMARC enforcement (`p=quarantine`/`reject`) + VMC/CMC; a downstream reward after tenants reach enforcement.
- **Seed-list / inbox-placement testing** (GlockApps / Litmus / Email on Acid) — pre-send QA enhancement, not a blocker.
- **Behavioral engagement segments** (opened/clicked predicates) — deferred until M9 events accumulate; require a bounded time window + rollup table.
- **Send-time optimization / per-recipient IANA-timezone delivery** — a scheduling enhancement layered on the roster's `scheduled_for` column after M7 (store IANA zone names, resolve with `time.LoadLocation`, never fixed UTC offsets).
- **`/emails/batch` throughput lane** — opt-in only, with an acknowledged double-send window on lost-ack retry (no per-message idempotency key); the default single-send lane is exactly-once-ish and ships first.
- **Native Resend Broadcasts as the primary send engine** — kept as the **fallback path if B3 shows Resend does not DKIM-sign custom headers on `/emails`**; rejected as primary because it cannot render CRM deal/company/custom-object merge tags (B4).
- **Month-partitioning of `marketing_email_events`** and deeper runs/action-log pruning — at-volume optimizations.

---

## Open decisions (product/policy, not build-blocking)

*(The former build-blocking items are now spikes B1–B4 above.)*

1. **Sending-domain model:** per-tenant custom sending domains (bring-your-own, reputation-isolated at the domain level — recommended default) vs a single shared platform domain with per-org subdomains for instant low-volume onboarding. Affects onboarding friction and blast radius (note the shared-IP-reputation caveat above).
2. **Editor build-vs-buy:** build a thin block editor on the existing TipTap + the B2 compile step (recommended, reuses owned code) vs buying a drag-drop suite (Unlayer/Beefree/Stripo) for non-technical marketers.
3. **Multi-tenant rate-limit / team strategy:** at what volume/tenant-count to split high-volume orgs onto separate Resend teams/subaccounts vs staying on the shared, config-sized bucket with per-org fair-share.
4. **Topic/preference-center granularity** and each topic's opt-in vs opt-out default (**immutable** after creation in Resend) — a product-owner policy call. Includes the M3 decision on whether the unauthenticated GET preference center may reveal per-topic state to a token-bearer or must gate that behind email confirmation.
5. **CASL/GDPR posture:** whether to require double-opt-in for EU/CA and cold-imported contacts, the region-aware consent-gating policy, and which non-express bases (EBR / legitimate-interest) are enabled for which regions.