package mailer

import (
	"fmt"
	"html"
)

// ctaEmail renders a branded email with a single call-to-action button. bodyHTML
// is trusted, caller-controlled copy (escape any interpolated user data before
// passing it in); actionURL is escaped here for the href.
func ctaEmail(heading, bodyHTML, ctaText, actionURL string) string {
	safeURL := html.EscapeString(actionURL)
	return emailShell(fmt.Sprintf(`
        <h1 style="margin:0 0 16px;font-size:20px;color:#0f172a;">%s</h1>
        <p style="margin:0 0 24px;font-size:15px;line-height:1.6;color:#334155;">%s</p>
        <p style="margin:0 0 24px;">
          <a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;font-weight:600;font-size:15px;padding:12px 24px;border-radius:10px;">%s</a>
        </p>
        <p style="margin:0;font-size:13px;line-height:1.6;color:#64748b;">
          If the button doesn't work, copy and paste this link into your browser:<br>
          <a href="%s" style="color:#2563eb;word-break:break-all;">%s</a>
        </p>`,
		html.EscapeString(heading), bodyHTML, safeURL, html.EscapeString(ctaText), safeURL, safeURL))
}

// plainEmail renders a branded email with a heading and a paragraph, no button
// (used for security alerts).
func plainEmail(heading, message string) string {
	return emailShell(fmt.Sprintf(`
        <h1 style="margin:0 0 16px;font-size:20px;color:#0f172a;">%s</h1>
        <p style="margin:0;font-size:15px;line-height:1.6;color:#334155;">%s</p>`,
		html.EscapeString(heading), html.EscapeString(message)))
}

// emailShell wraps body content in a minimal, email-client-safe layout.
func emailShell(inner string) string {
	return fmt.Sprintf(`<!doctype html>
<html>
  <body style="margin:0;padding:0;background:#f1f5f9;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;">
    <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="padding:32px 16px;">
      <tr><td align="center">
        <table role="presentation" width="480" cellpadding="0" cellspacing="0" style="max-width:480px;background:#ffffff;border-radius:16px;padding:32px;box-shadow:0 1px 3px rgba(0,0,0,0.08);">
          <tr><td>%s</td></tr>
        </table>
        <p style="margin:24px 0 0;font-size:12px;color:#94a3b8;">Guerrilla CRM</p>
      </td></tr>
    </table>
  </body>
</html>`, inner)
}
