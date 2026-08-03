import { test, expect, openFromSidebar } from './fixtures';

/**
 * Journey 3 — a deal appears on the pipeline board.
 *
 * NOTE ON SCOPE: this was originally specified as "a CONTACT appears in the
 * kanban", which is not a thing the product can do. ObjectListView only renders
 * the Table/Board toggle when the object's schema carries a field with
 * `key === 'stage'` and `type === 'relation'`, and the contact schema has no
 * such field (first_name, last_name, email, phone, company). Deal is the only
 * seeded object with a stage, so the board journey belongs to deals.
 *
 * The board itself is generic — ObjectKanban groups any stage-bearing object by
 * pipeline stage — so this exercises the shared component, not a deal-specific
 * page.
 *
 * Drag-to-change-stage is deliberately NOT covered. It is a dnd-kit pointer
 * gesture with an 8px activation constraint driving an optimistic write; making
 * that reliable in a smoke suite is a project of its own, and a flaky spec is
 * worse than an absent one.
 */
test('shows the seeded deal on the pipeline board', async ({ appPage: page }) => {
  // Sidebar click, not page.goto — see the auth-budget note in fixtures.ts.
  await openFromSidebar(page, 'Deals');
  await expect(page).toHaveURL(/\/deals$/);

  // The board toggle exists only for stage-bearing objects — its presence is
  // itself the assertion that the deal schema still has its stage relation.
  const boardToggle = page.getByRole('button', { name: 'Board' });
  await expect(boardToggle).toBeVisible();
  await boardToggle.click();

  // The board replaces the table outright. Asserting the table is gone proves
  // the view actually switched, rather than the card text merely existing
  // somewhere in a still-rendered list.
  await expect(page.getByRole('table')).toHaveCount(0);

  // Columns come from the org's pipeline stages.
  //
  // Only stage names that are UNIQUE in the seeded org are asserted here. A
  // fresh org gets a default pipeline seeded at registration (Lead In,
  // Qualified, Proposal, Negotiation, Closed Won) and seed_local_account.js
  // then adds five more (Discovery, Proposal Made, Negotiation, Won, Lost) —
  // ten stages, of which "Negotiation" legitimately appears twice. That is a
  // property of the seed data, not a bug, so the spec works with it rather than
  // asserting something the fixture cannot deliver.
  for (const stage of ['Discovery', 'Proposal Made', 'Won', 'Lost']) {
    await expect(page.getByText(stage, { exact: true })).toBeVisible();
  }

  // The cards themselves: each renders its record's display value (deal title).
  await expect(page.getByText('Acme Software License', { exact: true })).toBeVisible();
  await expect(page.getByText('Stark Cloud Migration', { exact: true })).toBeVisible();
});
