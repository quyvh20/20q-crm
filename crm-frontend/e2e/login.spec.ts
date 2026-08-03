import { test, expect } from '@playwright/test';
import { login } from './fixtures';

/**
 * Journey 1 — sign in.
 *
 * The gate every other journey depends on. If this breaks, the product is down
 * for everyone, so it is worth asserting both directions: a correct password
 * reaches the authenticated shell, and a wrong one does not.
 */
test.describe('authentication', () => {
  test('signs in with seeded credentials and lands on the authenticated shell', async ({ page }) => {
    await login(page);

    // PublicRoute redirects an authenticated visitor away from /login to '/'.
    await expect(page).toHaveURL(/\/$|\/\?/);

    // The sidebar only renders inside AppLayout, which only renders behind
    // ProtectedRoute — so these links are proof of a real session, not just of
    // a redirect having happened.
    //
    // `exact` matters: the dashboard's setup checklist also renders a /contacts
    // link, named "Import your contacts — done", which a substring match on
    // "Contacts" would also select.
    await expect(page.getByRole('link', { name: 'Contacts', exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Deals', exact: true })).toBeVisible();
  });

  test('rejects a wrong password and stays on the sign-in page', async ({ page }) => {
    await page.goto('/login');

    await page.locator('#login-email').fill(process.env.E2E_EMAIL as string);
    await page.locator('#login-password').fill('definitely-not-the-password-9x');
    await page.getByRole('button', { name: 'Sign In' }).click();

    // LoginPage renders the failure in a role="alert" container, so this asserts
    // the error is ANNOUNCED, not merely painted.
    await expect(page.getByRole('alert')).toBeVisible();

    // The negative half matters as much as the message: a failed sign-in must
    // not leave a usable session behind.
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByRole('link', { name: 'Dashboard' })).toHaveCount(0);
  });
});
