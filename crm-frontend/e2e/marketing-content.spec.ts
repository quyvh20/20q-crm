import { test, expect, openFromSidebar } from './fixtures';

/**
 * Journey 4 — marketing email content composes, and the server-rendered preview
 * returns real compiled HTML.
 *
 * NOTE ON SCOPE: this was originally specified as a campaign "test send". That
 * cannot be a CI journey. `resendBaseURL` is a package-level const with no env
 * override, and main.go registers the email executor unconditionally, so
 * POST /api/marketing/content/:id/test-send makes a genuine outbound HTTPS
 * request to https://api.resend.com/emails on every run — hitting a third party
 * from CI, and returning 502 ("the email provider rejected the test send") when
 * no RESEND_API_KEY is configured, which it never is in CI.
 *
 * The preview path exercises the same interesting logic — the mjml-WASM compile,
 * the merge-tag grammar, the CAN-SPAM footer resolver — and needs no network and
 * no credentials at all. So this drives that instead.
 */
test('composes email content and renders a server-compiled preview', async ({ appPage: page }) => {
  const stamp = Date.now().toString(36);
  const contentName = `E2E smoke ${stamp}`;
  const subject = `Quarterly update ${stamp}`;

  // Sidebar click, not page.goto — see the auth-budget note in fixtures.ts.
  // Reaching the builder the way a user does also proves the marketing section
  // of the nav renders at all, which it only does behind the marketing.manage
  // capability.
  await openFromSidebar(page, 'Email templates');
  await expect(page).toHaveURL(/\/marketing\/content$/);

  // `.first()` is REQUIRED, not defensive. CampaignContentListPage renders a
  // "New template" button unconditionally in the page header AND a second one
  // inside the empty state when there are no rows. A freshly seeded org has zero
  // templates (the seed creates none; starter templates are apply-on-demand), so
  // both are present and a bare getByRole is a strict-mode violation that fails
  // deterministically on the first run. The repo's own unit test documents the
  // duplication: CampaignContentListPage.test.tsx asserts getAllByText length > 0.
  await page.getByRole('button', { name: 'New template' }).first().click();
  await expect(page).toHaveURL(/\/marketing\/content\/new$/);

  // The builder's document-name field. Its only accessible name is an
  // aria-label, so getByLabel is the correct locator here.
  await page.getByLabel('Content name').fill(contentName);

  // The inspector's Email tab is the default, so the subject field is already
  // mounted and visible.
  await page.locator('#insp-subject').fill(subject);

  // The editor debounces a live compile against POST /api/marketing/content/preview
  // and only renders this size badge once a response lands with a byte count.
  // Waiting on it is therefore a wait for a real server compile — no arbitrary
  // sleep, and no networkidle (which never fires; see fixtures.ts).
  await expect(page.getByText(/^\d+ KB$/)).toBeVisible();

  await page.getByRole('button', { name: 'Preview' }).click();

  const dialog = page.getByRole('dialog', { name: 'Email preview' });
  await expect(dialog).toBeVisible();

  // The compiled document is rendered in a sandboxed iframe. Read its srcdoc
  // ATTRIBUTE rather than reaching into the frame: sandbox="" gives the frame an
  // opaque origin with scripting disabled, so frame-content evaluation is not
  // something to depend on — while the attribute is an ordinary DOM read on the
  // parent document, and it contains the exact HTML a recipient would receive.
  const frame = dialog.locator('iframe[title="Compiled email preview"]');
  await expect(frame).toBeVisible();

  const srcdoc = await frame.getAttribute('srcdoc');
  expect(srcdoc, 'the preview iframe should carry the compiled document').toBeTruthy();

  // A real mjml-compiled email document, not an error page or an empty shell.
  expect(srcdoc as string).toMatch(/<!doctype html/i);
  // The CAN-SPAM footer is injected by the server-side resolver, so its presence
  // proves the compile ran the full pipeline rather than echoing the blocks back.
  expect(srcdoc as string).toMatch(/unsubscribe/i);

  // Close the preview and persist the document.
  await dialog.getByRole('button', { name: 'Close' }).click();
  await expect(dialog).toBeHidden();

  await page.getByRole('button', { name: 'Save', exact: true }).click();

  // Creating navigates to the saved record's own URL (replace: true). That
  // redirect is the durable proof of a successful save — unlike the toast, which
  // is torn down by a wall-clock timer.
  await expect(page).toHaveURL(/\/marketing\/content\/[0-9a-f-]{36}$/i);

  // And the saved document is really the one that was composed.
  await expect(page.getByLabel('Content name')).toHaveValue(contentName);
});
