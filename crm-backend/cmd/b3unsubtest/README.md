# B3 spike: does Resend DKIM-sign `List-Unsubscribe`?

A throwaway tool to settle email-marketing spike **B3** (see `email_marketing_spikes.md`):
whether a `List-Unsubscribe` header on Resend's `/emails` endpoint is inside the DKIM
`h=` signed set. Gmail/Yahoo hide the one-click Unsubscribe button (RFC 8058) if it
isn't — and Resend's docs don't say — so only a real send settles it. The answer
decides M7's send architecture: self-supplied headers on `/emails`, the `topic_id`
managed path, or the Resend Broadcasts fallback.

It reads `RESEND_API_KEY` **from the environment** and never prints it. Not part of the
server binary (`cmd/server/main.go` is built alone); delete after the spike.

## Prerequisites
- A **verified** Resend sending domain (DKIM DNS green), so `-from` uses it (e.g. `noreply@send.yourdomain.com`).
- A Gmail inbox you control (and, ideally, a Yahoo one — Yahoo enforces one-click independently).
- For **variant B** only: a Resend **Topic** (`topic_xxx`); the recipient must be a contact opted-in to it, or the topic's default subscription must be opt-in, or Resend marks the send failed.

## 1. Send the test email(s)

PowerShell (Windows):
```powershell
$env:RESEND_API_KEY = "re_your_key"
cd crm-backend
go run ./cmd/b3unsubtest send `
  -from "Marketing Test <noreply@send.YOURDOMAIN.com>" `
  -to you@gmail.com `
  -unsub "https://YOURCRM.example.com/api/marketing/u/TESTTOKEN" `
  -topic topic_xxxxxxxx
```

bash:
```bash
cd crm-backend
RESEND_API_KEY=re_your_key go run ./cmd/b3unsubtest send \
  -from "Marketing Test <noreply@send.YOURDOMAIN.com>" \
  -to you@gmail.com \
  -unsub "https://YOURCRM.example.com/api/marketing/u/TESTTOKEN" \
  -topic topic_xxxxxxxx
```

- `-unsub` → runs **variant A** (self-supplied `List-Unsubscribe` + `List-Unsubscribe-Post`). Omit to skip.
- `-topic` → runs **variant B** (Resend-managed, no custom header). Omit to skip.
- Provide at least one. Run both to compare.

## 2. Grab the raw source
In Gmail, open each message → **⋮ → "Download original"** → save the `.eml`.
(Or "Show original" → copy the raw text.)

## 3. Check DKIM coverage
```powershell
go run ./cmd/b3unsubtest check -file "b3-variant-a.eml"
# or pipe:  Get-Content b3-variant-a.eml -Raw | go run ./cmd/b3unsubtest check
```

**PASS** = `list-unsubscribe` **and** `list-unsubscribe-post` both appear inside a
`dkim=pass` signature's `h=` tag → that path is viable; M3/M7 proceed on `/emails`.
Exit code 0 on PASS, 1 on FAIL.

## Interpreting the result
- **Variant B passes** → best outcome: Resend-managed one-click + per-topic suppression *and* full CRM merge-tag freedom. Model campaigns as Resend Topics.
- **Only variant A passes** → self-manage the headers + your own one-click POST endpoint + the M1/M4 suppression ledger.
- **Both fail** → route bulk through **Resend Broadcasts** (managed one-click, but per-recipient personalization collapses to contact-level fields — no deal/company/custom-object merge tags). This re-plans B4.

Repeat step 1–3 against a **Yahoo** inbox too. Also confirm DMARC is published/aligned on
the domain (M2) — without it the button is withheld regardless of `h=` coverage.
