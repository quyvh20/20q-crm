# Recipe: a "leads by source" report

A documented recipe rather than a built-in report, deliberately. P9 has no seed or
template mechanism — reports are rows a user creates — so shipping this as a feature
would mean inventing one, and the recipe below takes about a minute in the existing
builder.

## What makes it possible

Every captured lead is stamped with attribution at ingest time, into ordinary contact
custom fields (seeded on first source creation, L2.3):

| Field | What it holds |
|---|---|
| `lead_source` | the channel, e.g. `integration:google_ads` |
| `lead_source_detail` | the specific source's name |
| `utm_source`, `utm_medium`, `utm_campaign`, `utm_term`, `utm_content` | parsed from the submitted `page_url` |
| `gclid`, `fbclid` | click ids, where the platform sent one |
| `referrer_url` | where the visitor came from |

They are ORDINARY custom fields, which is the whole point: they are reportable in P9,
filterable in list views and usable in workflow conditions with no special support.

## The recipe

1. **Reports → New report**, object **Contacts**.
2. **Group by** `lead_source` (or `lead_source_detail` for per-source rather than
   per-channel).
3. **Measure**: count of contacts. Add a second grouping on `Created at` (month) for a
   trend.
4. **Filter**: `Created at` within the period you care about. Add
   `lead_source is not empty` to exclude contacts created by hand or by import — without
   it, the largest bucket is usually "blank" and the chart reads as though most leads
   have no source.
5. Save, and share it if the team needs it.

## Reading it honestly

- **A contact appears under the source that FIRST created it.** Attribution is
  first-touch on create and last-touch on update, so a person who arrives twice through
  two channels counts once, under the first. That is the right default for "where do our
  customers come from" and the wrong one for "which campaign did this deal close from" —
  the second question needs the deal, not the contact.
- **This counts contacts, not deliveries.** A source that received a hundred leads and
  failed to write ninety of them shows ten. The delivery log
  (Settings → Integrations → View all deliveries) is what answers "what arrived", and
  the per-source chart on a source's page splits written / skipped / failed by day.
- **Test leads are excluded from the chart, not from the report.** A test lead creates a
  real contact with a `.invalid` address; filter `Email does not contain @lead-test.invalid`
  if the number matters.
