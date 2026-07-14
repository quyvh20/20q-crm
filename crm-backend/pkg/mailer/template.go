package mailer

import (
	"fmt"
	"html"
	"strings"

	"crm-backend/internal/domain"
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

// notificationEmail renders a single in-app notification as an email (U5): the
// title becomes the heading, the body the copy, and — when the notification has an
// in-app link — a button to open it. body is user/automation data so it is escaped.
func notificationEmail(title, body, link string) string {
	if link != "" {
		copy := body
		if strings.TrimSpace(copy) == "" {
			copy = "You have a new notification."
		}
		return ctaEmail(title, html.EscapeString(copy), "View in CRM", link)
	}
	return plainEmail(title, body)
}

// digestEmail renders a daily-digest email (U5): a heading plus one block per
// notification (title, body, and a link when present). Every interpolated field is
// user/automation data, so all of it is escaped.
func digestEmail(items []domain.NotificationDigestItem) string {
	var b strings.Builder
	b.WriteString(`<h1 style="margin:0 0 8px;font-size:20px;color:#0f172a;">Your notification digest</h1>`)
	suffix := ""
	if len(items) != 1 {
		suffix = "s"
	}
	b.WriteString(fmt.Sprintf(`<p style="margin:0 0 8px;font-size:14px;color:#64748b;">You have %d new notification%s.</p>`, len(items), suffix))
	for _, it := range items {
		title := html.EscapeString(it.Title)
		if it.Link != "" {
			title = fmt.Sprintf(`<a href="%s" style="color:#2563eb;text-decoration:none;">%s</a>`,
				html.EscapeString(it.Link), title)
		}
		b.WriteString(fmt.Sprintf(`
        <div style="border-top:1px solid #e2e8f0;padding:14px 0;">
          <div style="font-size:15px;font-weight:600;color:#0f172a;">%s</div>
          <div style="font-size:14px;line-height:1.5;color:#334155;margin-top:2px;">%s</div>
        </div>`, title, html.EscapeString(it.Body)))
	}
	return emailShell(b.String())
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
