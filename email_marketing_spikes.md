# Email Marketing — Blocking Spikes B1–B4: Resolution

> Resolves the four blocking spikes defined in `email_marketing_plan.md`. Each spike's
> **Decision** is what to build against; **Confirm against your Resend account** lists the
> empirical/account items only you can settle (I can't touch the live account or send test
> mail); **Open decisions** are product/infra choices. Researched July 2026 from Resend's
> primary docs (verbatim quotes + URLs) and the repo; the two load-bearing external facts
> (B1 rate limit, B3 `/emails` capabilities) were independently re-fetched to confirm.

## TL;DR

| Spike | Gates | Status | Decision |
|-------|-------|--------|----------|
| **B1** Resend limits + Return-Path | M2, M7 | ✅ Resolved (1 account confirm) | Global per-team token bucket, config-sized (default **10 rps**, run ~8); Return-Path is the auto-aligned `send.` domain subdomain (one verified domain per org); 429s branch by error name. |
| **B2** Email-HTML compile runtime | M6 | ✅ Resolved | **mjml-go** (pure-Go WASM, no Node, single binary preserved). Compile once into `body_html_compiled`. |
| **B3** `List-Unsubscribe` DKIM coverage | M3 exit → M7 | ⚠️ **Contingent on 1 empirical test** | Plan to send bulk on `/emails` (keeps CRM merge freedom), **pending a DKIM `h=` test**; fall back to Broadcasts only if it fails. **This is the one hard blocker.** |
| **B4** Merge-context scope | M6, M7 | ✅ Resolved (depends on B3) | Default `merge_scope` = **contact + org + campaign**; optional declared **company**; deal/custom-object deferred. Two hard save-time gates. |

**Two corrections to the plan surfaced by the research:**
1. B1 — Resend's default is **10 req/s per team**, not the 2 req/s the plan assumed; and quota exhaustion returns **429**, not 403.
2. B4 — the safe default merge scope is **contact/org/campaign**, not the plan's "contact/org/user": a callerless blast has no acting user and no per-recipient user loader exists.

---

## B1 — Resend account limits & aligned Return-Path (gates M2, M7)

**Decision**
- **Rate limiting (M7):** one **global token bucket per Resend team**, shared across *all orgs and both transactional + marketing* — because the limit is per-team across every API key, a per-org or per-channel bucket would collectively blow the ceiling. Size from config (`RESEND_MAX_RPS`, default **10**), never hard-coded; run at ~8 rps for burst headroom; feed `retry-after`/`ratelimit-reset` from any 429 back into the limiter to self-throttle.
- **429 handling:** branch on the error *name*, not just the status. `rate_limit_exceeded` → transient, honor `retry-after` and retry. `daily_quota_exceeded` / `monthly_quota_exceeded` → **also 429** (not 403) → do **not** hot-retry; park and surface. The only 403s are domain-verification errors.
- **Return-Path / DMARC alignment (M2):** the aligned Return-Path is a **domain setting, not a per-send field** — `/emails` has no return-path/mail-from parameter. Resend uses the `send` subdomain of the verified domain as the Return-Path, which the docs say is *"used for SPF authentication, DMARC alignment, and handling bounced emails"* — so **DMARC aligns automatically once the domain's DNS (SPF TXT + MX on `send.<domain>`, plus DKIM) verifies.** For per-org alignment: **create + verify one Resend domain per org.** `custom_return_path` (default `send`) is an optional per-domain rename, not needed by default.
- **Quota tracking:** there are **no `x-resend-*` quota headers** — remaining quota isn't exposed; track it out-of-band or react to the quota-exceeded 429. Both sent *and received* emails count toward quotas.

**Evidence:** `resend.com/docs/api-reference/rate-limit` ("10 requests per second per team … across all API keys"), `resend.com/docs/api-reference/errors` (rate_limit / daily_quota / monthly_quota all 429), `resend.com/docs/api-reference/domains/create-domain` + `.../dashboard/domains/introduction` (Return-Path subdomain, `custom_return_path`), `resend.com/docs/api-reference/emails/send-email` (no return-path field). *(Rate limit + no-return-path-field independently re-fetched and confirmed.)*

**Confirm against your Resend account**
- Your team's **actual** rate limit (Settings → Usage) — 10 rps is the documented default; a trial tier may be lower, a support-raised limit higher. Set `RESEND_MAX_RPS` from it.
- Your plan's **daily/monthly quota numbers** (not in docs; only surface as the 429).
- If you'll need >10 rps for bulk, open a support request to raise the per-team limit *before* raising the config ceiling.
- Empirically confirm a real 429 carries the `ratelimit-*`/`retry-after` headers.

**Open decisions**
- One shared Resend team for all orgs (simpler, but 10 rps + quotas are shared across your whole customer base — a scaling ceiling) vs. segmented teams per high-volume tenant (isolation, more account management). *This is the plan's deferred "separate Resend teams" escape hatch — B1 confirms it's the only real way past the shared ceiling.*
- Transactional-vs-marketing budget split when they contend for the shared bucket.
- Proactively meter quota to gate marketing before `daily_quota_exceeded`, vs. send-until-429-and-park.

---

## B2 — Email-HTML compile runtime (gates M6)

**Decision: adopt `github.com/Boostport/mjml-go` (pin v0.16.0).**

It's the only option that satisfies M6 *and* preserves this repo's defining constraint — one self-contained Go binary on Railway NIXPACKS with **no Node**. It's pure Go (wazero + brotli + jackc/puddle, no cgo; it embeds MJML 4.14.x compiled to a ~2.66 MB brotli WASM), so `go build -o bin/server cmd/server/main.go` is unchanged and `railway.toml`/`nixpacks.toml` need no edits — **the only CI/deploy change is `go.mod`/`go.sum`.** MJML 4.x fully covers M6's fixed block set (hero/text/button/image/divider/columns/footer + `mj-preview` for the preheader) and inherits MJML's battle-tested Outlook/Gmail output (MSO conditionals, VML bulletproof buttons, ghost-table column stacking) — the accumulated correctness a hand-rolled compiler would have to re-earn on the deliverability-critical path.

Its ~35–170 ms/compile (≈10–40× slower than Node) is irrelevant: **compile once** per template-save/campaign-launch into `body_html_compiled`; per-recipient merge stays in `InterpolateTemplateHTML`.

**Rejected:** (a) Node-in-container / react-email — heaviest infra change, react-email still *requires* Node, doubles the image; (c) Node sidecar — clean but a second deployable + network hop is over-engineered for a fixed 7-block set (documented as the future scale-out path); (d) hand-rolled pure-Go compiler — reinvents MJML's Outlook hacks (high risk). The pure-Go port `preslavrachev/gomjml` (no WASM at all) is a **watch-list item** — only ~70% MJML-compliant with Outlook gaps as of late 2025; revisit as a zero-WASM drop-in once it declares full compliance.

**Provisioning plan**
- CI/build: `go get github.com/Boostport/mjml-go@v0.16.0`, commit `go.mod`/`go.sum`, confirm `go build` still yields one static binary (CGO stays off). Deploy image unchanged.
- Runtime: new `internal/marketing/emailcompile` package; **warm-up compile at boot** to pay wazero instantiation off the hot path; on save/launch map block-JSON → MJML string → `mjml.ToHTML` (minify + inline) → `body_html_compiled`.
- In-code guards (not infra): reject/flag compiled HTML **≥100 KB** (Gmail clips); derive the `text/plain` alternative separately (MJML emits none); **on any compile error, block save/launch — never fall back to sending raw TipTap HTML.**

**Confirm at build time**
- Actual binary-size delta + steady-state RSS on the real Railway plan (only the 2.66 MB WASM is confirmed; runtime memory is unmeasured — set the wazero pool ceiling from it).
- Render-QA all 7 blocks through mjml-go (Outlook desktop + Gmail web + Apple Mail dark) — the true B2 acceptance test.

**Open decisions:** accept the ~few-MB binary + wazero memory on the current plan vs. the Node sidecar; server-side block authoring (assumed, favors mjml-go) vs. react-email DX (forces Node); mjml-go is single-maintainer/annual-cadence pinned to MJML 4.14.x — accept that, with a trigger to re-evaluate gomjml.

---

## B3 — `List-Unsubscribe` DKIM coverage (gates M3 exit → M7) — the hard blocker

**Decision: plan to send true marketing bulk on the `/emails` send endpoint** (not Broadcasts), because only `/emails` lets us render arbitrary CRM contact/company/custom-object merge variables per recipient — the whole reason the plan chose the shared `send_email` executor with a per-send `channel` flag. **BUT this is contingent on one empirical test the docs cannot answer.**

What the docs settle (confirmed):
- `/emails` **accepts custom headers** (`headers` field) — Resend's own docs show adding `List-Unsubscribe` this way. On `/emails`, Resend does **not** auto-add the one-click headers — *you* supply `List-Unsubscribe` + `List-Unsubscribe-Post: List-Unsubscribe=One-Click` and host the POST endpoint (honor within 48h).
- **Broadcasts** *do* auto-manage RFC 8058 one-click (DKIM-covered) + a hosted unsubscribe page + account-wide suppression — **but** their merge tags are **contact/audience-level only** (`{{{contact.first_name|fallback}}}`, last_name, email, `{{{RESEND_UNSUBSCRIBE_URL}}}`); a broadcast sends one templated body to a segment and **cannot render CRM deal/company/custom-object variables per recipient.** That's the cost of the fallback.
- Bonus path (strongest candidate): `/emails` also accepts a **`topic_id`** that enforces per-recipient opt-in/opt-out server-side (each of to/cc/bcc checked separately) and ties into Resend's hosted unsubscribe pages — *while you still render the full HTML body yourself* (full merge freedom preserved).

The **undocumented** question: is a self-supplied (or `topic_id`-managed) `List-Unsubscribe`/`List-Unsubscribe-Post` **inside the DKIM `h=` signed set**? Gmail/Yahoo hide the one-click button (RFC 8058) if it isn't. No Resend doc states this. **Only an empirical send settles it — do not exit M3 until captured.**

**Confirm against your Resend account (the empirical test)**
- **Variant A (self-supplied headers):** from a Resend-verified domain, POST `/emails` to a Gmail inbox with `headers: { "List-Unsubscribe": "<https://YOURCRM/u/TOKEN>, <mailto:unsub@YOURCRM>", "List-Unsubscribe-Post": "List-Unsubscribe=One-Click" }`. In Gmail → **Show original** → read the `DKIM-Signature` `h=` tag. **PASS iff** both `list-unsubscribe` *and* `list-unsubscribe-post` appear in `h=`, `dkim=pass`, and Gmail renders the "Unsubscribe" link.
- **Variant B (`topic_id` managed):** create a Topic, send via `/emails` with `topic_id` and **no** custom header; check whether Resend auto-injected the headers, whether they're in `h=`, and whether Gmail renders the button. **Prefer B if it works** — managed RFC 8058 one-click + per-topic suppression *and* full merge freedom.
- Repeat both against **Yahoo/AOL** (enforces one-click independently of Gmail).
- Confirm DMARC is published/aligned on the domain (M2 dependency — without it the button is withheld regardless of `h=`).

**Decision tree:** Variant B passes → best path (managed compliance + merge freedom); model each campaign as a Resend Topic. Else Variant A passes → self-managed headers + own POST endpoint + M1/M4 ledger. Else (both fail) → **Broadcasts fallback**, and accept per-recipient personalization collapses to contact-level fields (flatten needed values into contact properties; no per-recipient layout).

**Open decisions:** if `topic_id` works, Resend-hosted unsubscribe/preferences vs. a first-party CRM preference center (and which suppression store — Resend's vs. the M1 ledger — is authoritative, to avoid double-send/silent-drop); whether record-personalized bulk is a required M7 capability at all if forced to Broadcasts.

---

## B4 — Merge-context scope (gates M6, M7)

**Decision: pin `merge_scope` to a small closed set grounded in the loaders that exist.**

- **Default = contact-only**, allowed roots **{contact, org, campaign}** — **not** the plan's "contact/org/user". A marketing blast is email/contact-keyed with no deal, run, trigger, or acting user; there is no per-recipient user loader and the worker is callerless, so `user.*` provably can't hydrate.
  - `contact.*` — hydrated per recipient by `loadContactForTrigger` (id, first_name, last_name, email, phone, owner_user_id, company_id, `custom_fields.*`). `engine.go:1229`
  - `org.*` — campaign-constant, injected once. `models.go:294`
  - `campaign.*` — **synthetic** (campaign.name; the unsubscribe link stays a render-time HMAC footer per M3, not an authored tag). Not in `EvalContext` today (`models.go:290`) — M7 must inject it.
- **Optional declared scope = company ONLY** — one-hop from `contact.company_id` via `loadCompanyForTrigger` (id, name, industry, website, custom_fields.*). `engine.go:1187`
- **`deal.*` and arbitrary custom-object slugs: deferred.** Nothing resolves them from a contact today (no contact→deal reverse hydration; a contact has zero-or-many deals — ambiguous). Supporting them needs a *net-new deterministic resolver* (e.g. "most-recently-updated open deal") — gate behind an explicit product-owned rule, never an implicit choice.

**Composer save rules (two hard gates — mandatory because the runtime resolver is fail-soft: unresolved *or empty* paths render `""` with only a warning, `template.go:59-63`):**
1. **Reject out-of-scope tags at save.** Parse subject + preheader + body with the resolver's own `templatePattern` (`template.go:13`, skip the escaped form), and **reject** (not warn) any tag whose root is outside the declared roots or whose leaf is outside the loader's known field set. This *hardens* `validator.go:947-969`, which today only warns and wrongly lists deal/trigger/user/actions as valid.
2. **Require an author fallback on every non-guaranteed tag.** The resolver has **no fallback grammar** today, so this needs a minimal extension: `templatePattern` + `interpolateTemplate` to parse `{{path|fallback}}` and emit the fallback on nil-*or-empty* (both blank branches, `template.go:59-63` and `166-169`), preserving `InterpolateTemplateHTML` value-escaping. Because `first_name`/`last_name` are plain strings that can be `""` and email/phone/company_id are absent when null, essentially only `contact.id`-class values and the injected `unsubscribe_url` are fallback-exempt.

**Dependency:** this server-side per-recipient hydration model is **only valid on the `/emails` path**. If B3 forces the Broadcasts fallback, B4 must be re-planned (Broadcasts can't render contact/company/custom tags). **Settle B3 before treating B4 as final.**

**Open decisions:** whether to ever allow a declared deal scope (+ its deterministic selection rule); the exact `campaign.*` surface; the fallback delimiter syntax; whether the fallback-required rule is retrofitted onto A5 transactional templates or scoped to marketing content only.

---

## What this unblocks

- **M2** can build now: one verified Resend domain per org, rely on the auto-aligned `send.` Return-Path, `CanBulkSend` checks SPF+DKIM+DMARC verified. (B1 done.)
- **M6** can build now: mjml-go compile step, compile-once into `body_html_compiled`, the two B4 composer gates. (B2 + B4 done.)
- **M7** is blocked on the **B3 empirical DKIM test** — its send-lane architecture (self-supplied headers vs. `topic_id` vs. Broadcasts) and B4's merge model both hinge on that one result. Run the B3 test next.
