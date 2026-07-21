# Recipe: LinkedIn Lead Gen Forms into the CRM

A documented relay rather than a native connector, deliberately. LinkedIn's Lead Sync
API is gated on a verified company page plus a business-domain email and an
application that is reviewed per-app, and its versions retire on a published
schedule — so a native adapter is a permanent maintenance commitment that can be
switched off by someone else. A relay through the Lead Capture API takes about ten
minutes and cannot be revoked.

## What makes it possible

The Lead Capture API is the same port every other channel funnels into, so a relayed
LinkedIn lead gets the whole pipeline rather than a side door:

| You get | Because |
|---|---|
| Field mapping | The source's mapping table turns LinkedIn's question names into contact fields |
| Deduping | `match_fields` matches on email, then phone — a repeat enquiry updates rather than duplicates |
| Owner routing | One owner or a rotation, set per source |
| Attribution | `lead_source` reads `integration:api`, `lead_source_detail` the source's name |
| A delivery log | Every relayed lead is a row, with its payload, whether it wrote, and why not |

## The recipe

1. **Settings → Integrations → New source**, kind **Capture API**, name it
   `LinkedIn` (that name is what `lead_source_detail` reports, so name it for the
   channel, not for the relay tool). Pick a routing option. Copy the key — it is
   shown once.
2. In **Make** (free tier is enough) or **Zapier** (its webhook action needs a paid
   plan), create a scenario triggered by **LinkedIn Lead Gen Forms → New Lead Gen
   Form Response**. Connect the LinkedIn account that owns the ad account.
3. Add an **HTTP → Make a request** step: `POST` to
   `https://<your-crm-host>/api/capture/leads`, header
   `Authorization: Bearer crm_lead_…`, `Content-Type: application/json`.
4. Body — wrap the mapped values in a `fields` object, and nothing else:
   `{"fields": {"email": "{{email}}", "first_name": "{{firstName}}", "last_name": "{{lastName}}", "phone": "{{phoneNumber}}", "job_title": "{{jobTitle}}"}}`.
   Send the questions you actually collect; anything the CRM does not know yet is
   recorded on the delivery and can be mapped to a new field in one click afterwards.
5. Add `Idempotency-Key` with the LinkedIn lead's own id. Without it a scenario
   re-run relays the same person again, and the capture API has nothing to dedupe on —
   the key is the only thing that makes a retry safe.
6. Turn the scenario on and submit a test lead through LinkedIn's own form preview.
   It lands in the source's delivery log within a few seconds.

## Reading it honestly

- **The relay is a third system that can fail silently.** If the scenario is paused,
  out of operations, or its LinkedIn connection expires, leads stop arriving and
  nothing here can tell — the CRM's health signal only counts deliveries that reached
  it, so a relay that stops relaying looks identical to a quiet week. Set an alert in
  Make or Zapier on scenario failure; that is the monitor, not this delivery log.
- **LinkedIn will not resend.** The relay tool holds the only retry budget, and most
  free-tier plans stop after a few attempts. If a batch was lost, the recovery path is
  `POST /api/capture/leads/batch` with each lead's LinkedIn id as its
  `idempotency_key` — already-written leads are skipped, so it is safe to resend the
  whole export.
- **`lead_source` will say `integration:api`, not `linkedin`.** Attribution names the
  channel the lead arrived through, and it genuinely arrived through the capture API.
  Report on `lead_source_detail` — the source's name — when you want LinkedIn broken
  out, which is why step 1 says to name the source for the channel.
