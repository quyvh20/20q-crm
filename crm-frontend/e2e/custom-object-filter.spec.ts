import { test, expect, openFromSidebar } from './fixtures';

/**
 * Journey 5 — filter a CUSTOM object, then save that filter as a view.
 *
 * Why a custom object rather than contacts: the filtering overhaul's claim is
 * that one engine serves every object, and custom objects are the hard half of
 * that claim. Their fields live in a JSONB blob (`data ->> key`) rather than
 * typed columns, so a numeric comparison only works if the compiler's guarded
 * `::numeric` cast fires — and JSONB is exactly where the pre-overhaul list
 * could do nothing but exact string equality. A green contacts filter would
 * prove much less.
 *
 * It is also the only coverage of list_views against a real Postgres: the
 * table ships as a main.go boot guard (prod) mirrored by migrations/000078
 * (CI), and nothing else exercises save → clear → re-apply from a browser.
 *
 * The `asset` object and its three records come from
 * crm-backend/scripts/seed_local_account.js. Seeding rather than creating them
 * here buys two things: API writes need the in-memory Bearer token the SPA
 * holds (awkward to reach from a test), and an object that exists before the
 * app boots appears in the sidebar — so this spec spends ZERO page loads beyond
 * the shared login (see the auth-request budget note in fixtures.ts).
 */
test('filters a custom object by its JSONB fields and saves the filter as a view', async ({
  appPage: page,
}) => {
  // Seeded sizes: Alpha 500, Beta 1500, Gamma 5000 — so a 1000–2000 band keeps
  // exactly one, which makes every assertion below exact rather than "fewer".
  const ALPHA = 'Alpha Tower';
  const BETA = 'Beta Plaza';
  const GAMMA = 'Gamma Hall';

  const row = (name: string) => page.getByRole('row', { name: new RegExp(name) });
  // The filter editor and the views menu are both Popovers, which render as
  // role="dialog". Scoping to it stops a menu item colliding with the trigger
  // that opened it — the views trigger takes the applied view's NAME, so an
  // unscoped "Mid-size" would match two elements once the view is applied.
  const popover = () => page.getByRole('dialog');

  // Sidebar click, not page.goto — the seeded object is in the nav because it
  // existed before the app booted.
  await openFromSidebar(page, 'Assets');
  await expect(page).toHaveURL(/\/objects\/asset$/);
  for (const name of [ALPHA, BETA, GAMMA]) await expect(row(name)).toBeVisible();

  // ── A NUMBER range over a JSONB field ─────────────────────────────────────
  // Inexpressible anywhere in the product before this change: the list could
  // only ever test `data ->> 'sqft' = '1500'`, as text.
  //
  // `exact` matters — getByRole matches the accessible name by SUBSTRING, so a
  // bare 'Filter' would also select "Clear filters" and trip strict mode.
  await page.getByRole('button', { name: 'Filter', exact: true }).click();
  await popover().getByLabel('Search fields').fill('sq');
  await popover().getByRole('button', { name: /Sq Ft/ }).click();
  await popover().getByLabel('Operator').selectOption('between');
  await popover().getByLabel('From').fill('1000');
  await popover().getByLabel('To').fill('2000');
  await popover().getByRole('button', { name: 'Apply' }).click();

  await expect(row(BETA)).toBeVisible();
  await expect(row(ALPHA)).toBeHidden();
  await expect(row(GAMMA)).toBeHidden();

  // The filter rides the URL, so a filtered list is shareable and survives a
  // reload — `flt` is the overhaul's replacement for the old `f.<key>` pairs.
  await expect(page).toHaveURL(/[?&]flt=/);

  // ── Save it as a view ─────────────────────────────────────────────────────
  const viewName = `Mid-size ${Date.now().toString(36)}`;
  await page.getByRole('button', { name: 'Views', exact: true }).click();
  await popover().getByRole('button', { name: 'Save current view…' }).click();
  await popover().getByLabel('View name').fill(viewName);
  await popover().getByRole('button', { name: 'Save', exact: true }).click();

  // The trigger takes the applied view's name: the durable consequence of a
  // successful POST /views, asserted instead of a toast (see fixtures.ts).
  await expect(page.getByRole('button', { name: viewName })).toBeVisible();

  // ── Clear, then re-apply the saved view ───────────────────────────────────
  // Clearing also drops `vw`, so the trigger reverts to "Views" — which is what
  // keeps the selector below unambiguous.
  await page.getByRole('button', { name: /Clear filters/ }).click();
  for (const name of [ALPHA, BETA, GAMMA]) await expect(row(name)).toBeVisible();

  await page.getByRole('button', { name: 'Views', exact: true }).click();
  await popover().getByRole('button', { name: viewName, exact: true }).click();

  // Round-trip proof: the definition survived Postgres jsonb and recompiled to
  // the same predicate on the way back out.
  await expect(row(BETA)).toBeVisible();
  await expect(row(ALPHA)).toBeHidden();
  await expect(row(GAMMA)).toBeHidden();
});
