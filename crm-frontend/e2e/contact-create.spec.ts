import { test, expect, openFromSidebar } from './fixtures';

/**
 * Journey 2 — create a contact, and see it in the list.
 *
 * This is the highest-value journey in the suite. Contacts render through
 * ObjectListView + ObjectForm, the schema-driven renderer that EVERY object
 * (contacts, deals, companies, and any custom object a customer defines) is
 * drawn with. A regression here is never a regression in one page.
 *
 * The form fields are reached with `getByLabel`, which is only possible because
 * ObjectForm now threads a generated `object-field-${key}` id into each control
 * and points its <Label htmlFor> at it. Before that fix the labels were bare
 * siblings with no association, so every schema-driven input was also
 * unlabelled for screen-reader users.
 */
test('creates a contact and shows it in the contacts list', async ({ appPage: page }) => {
  // Unique per run. The suite runs against a seeded database that is NOT reset
  // between runs locally, and contacts are unique on email — a fixed value
  // would pass once and then fail forever with a duplicate-key error.
  const stamp = Date.now().toString(36);
  const firstName = 'Ada';
  const lastName = `Lovelace-${stamp}`;
  const email = `ada.${stamp}@e2e.test`;

  // Sidebar click, not page.goto — see the auth-budget note in fixtures.ts.
  await openFromSidebar(page, 'Contacts');
  await expect(page).toHaveURL(/\/contacts$/);

  // The list header renders once the schema resolves; `Add ${schema.label}`.
  await page.getByRole('button', { name: 'Add Contact' }).click();

  // The create panel is the shared Radix modal primitive, so it is a real
  // role="dialog" named by its (visually hidden) title.
  const dialog = page.getByRole('dialog', { name: 'New Contact' });
  await expect(dialog).toBeVisible();

  // Scoped to the dialog so "Email" cannot resolve to something on the page
  // behind it. 'First Name' matches the "First Name *" required label by
  // substring.
  await dialog.getByLabel('First Name').fill(firstName);
  await dialog.getByLabel('Last Name').fill(lastName);
  await dialog.getByLabel('Email').fill(email);

  await dialog.locator('#object-form-submit').click();

  // Deliberately NOT asserting on a success toast — see the note in fixtures.ts.
  // The durable consequences are that the modal closes and the row exists.
  await expect(dialog).toBeHidden();

  // ObjectListView refetches the first page synchronously in its onSaved
  // handler, and the list is plain useState/useEffect rather than react-query,
  // so there is no cache-staleness window to wait out here.
  //
  // The table is semantic, and a row's accessible name is the concatenation of
  // its cells — so matching the unique last name proves the record is really in
  // the rendered list, not merely that the POST returned 201.
  await expect(page.getByRole('row', { name: new RegExp(lastName) })).toBeVisible();

  // The displayed name is built from display_field, so this also checks the
  // record was stored with both name parts rather than just the first.
  await expect(page.getByRole('row', { name: new RegExp(`${firstName} ${lastName}`) })).toBeVisible();
});
