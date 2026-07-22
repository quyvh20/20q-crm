# Deploy checklist: L7 (lead platform expansion & legacy cleanup)

Covers the L7 commits in `e1ff82c..5d5a883` — `f6b93af`, `2e5a30a`, `54271a1`,
`f988b84`, `02aa85b`, `5d5a883`. Written for the FIRST prod deploy carrying them, and
specific to what they changed rather than as a general runbook.

Two other commits ride in the same range and are **not** covered here: `be1884b`
(starter templates reachable after onboarding) and `470d6e3` (deploy healthcheck
window), both from a parallel session. Whoever owns those should say what they need
watched.

Everything here is additive except one item, and that item is first because it is the
only change that can break a working integration on contact with production.

## Before deploying

### 1. Is `WEBHOOK_SKIP_SIGNATURE=true` set in the Railway environment?

This flag used to disable HMAC verification on `POST /api/webhooks/inbound/:org_token`
for **every org at once**. It is now gated on an `APP_ENV` allowlist
(`development` | `test`), and `cmd/server/main.go`'s own comments record that
**APP_ENV is unset on prod today**. So on deploy the gate evaluates false and
signature verification switches **on**.

- **Not set** → nothing changes. Continue.
- **Set, and every sender signs** → safe. Unset it while you are here; it no longer
  does anything on prod and its presence invites someone to "fix" the gate later.
- **Set, and some sender does NOT sign** → that integration starts getting
  `401 {"error":{"code":"UNAUTHORIZED","message":"missing X-Signature header"}}` the
  moment this deploys. Fix the sender first. Do **not** set `APP_ENV=development` on
  production to keep the bypass: that flag also unlocks the debug-token escape hatch
  (`usecase.debugTokensEnabled`), which is an account-takeover primitive.

### 2. Is `idx_contacts_org_email` actually unique in prod?

The duplicate-email race recovery added to the legacy webhook catches SQLSTATE 23505
**on that exact constraint name**. Its boot guard refuses to build the index over
pre-existing duplicate `(org_id, email)` pairs and only logs — so on a dirty database
the index is absent and the recovery path can never fire.

```sql
SELECT i.indisunique
  FROM pg_class c
  JOIN pg_index i ON i.indexrelid = c.oid
 WHERE c.relname = 'idx_contacts_org_email';
```

Expect one row, `t`. No row, or `f`, means concurrent first-time deliveries for the
same address still produce two contacts — the same as before this deploy, so it is not
a regression, but the fix you think you shipped is inert until the duplicates are
merged.

## In the boot log

Grep for `lead integrations boot guard failed`. Each hit names the guard in its `what`
field. Three are new in this deploy:

| Guard (`what`) | Consequence if it failed |
|---|---|
| `lead_sources one legacy webhook per org` | Two concurrent first deliveries can create duplicate legacy sources, which the delete guard then makes undeletable |
| `lead_sources legacy webhook backfill` | Orgs see no "Workflow webhook" source until their next delivery creates one. Cosmetic and self-healing |
| `lead_sources conn form tiktok unique` | Only matters once TikTok is connected; the enable-a-form race loses its idempotency backstop |

All three are index-or-insert only and degrade to "less tidy" rather than "broken". A
failure does mean migrations `000052`/`000053` and the boot guards now disagree, which
is the drift the plan's own rule exists to prevent — worth fixing before the next
deploy rather than after.

## Smoke tests, in order of risk

### Legacy inbound webhook — do this one first

It is the only changed path with existing users behind it.

1. **A normal signed lead** → `200 {"status":"accepted","contact_id":"…"}`, and the
   contact_id in the body **matches a row that exists**. Returning an id for a contact
   that was never inserted was the bug; a phantom id also enrolled workflows against a
   record that is not there.
2. **Two deliveries for the same email, each carrying a DIFFERENT custom field** →
   both keys survive on the contact. Previously the second delivery replaced the whole
   `custom_fields` blob, destroying values a human had typed.
3. **The delivery appears in Settings → Integrations → View all deliveries**, under a
   source named "Workflow webhook (legacy)". That source is new: it is created by the
   backfill guard, or by the first delivery if the guard did not run. Legacy traffic
   has never been visible in a delivery log before.
4. **Watch 500s on this route.** Payloads that used to fail silently now report the
   failure. A rise is the fix working — but read which payloads, because each one is a
   lead that was being lost before today without anyone knowing.

### Capture API

One lead through `POST /api/capture/leads` with a bearer key, confirming the L7.1 and
L7.5 changes did not disturb the channel that carries the most traffic.

### Facebook / Instagram

Only meaningful once Meta track M clears. When it does:

- The per-connection **Check connection** panel's *Delivery subscription* row should
  report a real verdict. It has said "unknown" since L6.3 shipped, because the Facebook
  implementation of that probe did not exist.
- A delivery's context should carry `platform` (`fb` or `ig`) and `is_organic`. That is
  the only thing in the ledger that distinguishes an Instagram lead from a Facebook one.

### TikTok

Nothing to test until `TIKTOK_APP_ID`, `TIKTOK_APP_SECRET` **and** `TIKTOK_AUTH_URL`
are all set — the registration gate requires all three, because a provider registered
without its auth URL renders a Connect button that lands nowhere.

Note that the public route `/api/integrations/tiktok/webhook` **is mounted regardless**.
With the provider unregistered an unauthenticated POST hits the registry miss and acks
`200` without writing anything, which is the same shape the Facebook route has always
had for an unconfigured deployment.

## Rollback

Everything is additive except the signature gate, and that one should not be rolled
back: reverting it re-opens a global HMAC bypass across every org on a public
endpoint whose token travels in the URL and is written to the access log. If it bites,
the fix belongs at the sender.

The new source kinds (`webhook_inbound`, `tiktok_form`) are values in a `VARCHAR` with
no CHECK constraint, and the two migrations are index-only, so a rollback of the binary
leaves no schema to undo.
