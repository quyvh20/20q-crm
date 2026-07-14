import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// The three surfaces that can CREATE an account (U7.6). It is easy to remember
// /register and forget the other two, which is exactly what happened before: the
// Google button on the SIGN-IN page signs up a brand-new Google user, and the
// invite page is where a brand-new invitee sets their first password. All three
// must show the consent line, so all three are asserted here.

vi.mock('../../lib/auth', () => ({
  useAuth: () => ({
    login: vi.fn(),
    register: vi.fn(),
    acceptInvitation: vi.fn(),
  }),
}));

vi.mock('../../lib/api', () => ({
  ENROLL_TWO_FACTOR_PATH: '/enroll-2fa',
  getInvitationPreview: vi.fn(),
}));

import { getInvitationPreview } from '../../lib/api';
import LegalConsent from './LegalConsent';
import LoginPage from '../../pages/LoginPage';
import RegisterPage from '../../pages/RegisterPage';
import AcceptInvitePage from '../../pages/AcceptInvitePage';
import { TERMS_URL, PRIVACY_URL } from '../../lib/legal';

const mockPreview = getInvitationPreview as unknown as ReturnType<typeof vi.fn>;

const renderAt = (ui: React.ReactNode, path = '/') =>
  render(<MemoryRouter initialEntries={[path]}>{ui}</MemoryRouter>);

const termsLink = () => screen.getByRole('link', { name: 'Terms of Service' });
const privacyLink = () => screen.getByRole('link', { name: 'Privacy Policy' });

describe('LegalConsent', () => {
  afterEach(cleanup);

  it('links to both legal documents', () => {
    renderAt(<LegalConsent />);
    expect(termsLink()).toHaveAttribute('href', TERMS_URL);
    expect(privacyLink()).toHaveAttribute('href', PRIVACY_URL);
  });

  it('states what the user is agreeing to', () => {
    const { container } = renderAt(<LegalConsent />);
    expect(container.textContent).toContain(
      'By continuing, you agree to our Terms of Service and Privacy Policy.',
    );
  });

  // With no VITE_TERMS_URL/VITE_PRIVACY_URL configured these fall back to the
  // in-app routes, so they must stay inside the SPA — a full reload here would
  // discard a half-filled signup form.
  it('defaults to the in-app placeholder routes', () => {
    renderAt(<LegalConsent />);
    expect(termsLink()).toHaveAttribute('href', '/terms');
    expect(privacyLink()).toHaveAttribute('href', '/privacy');
  });
});

describe('consent on every account-creation surface', () => {
  beforeEach(() => mockPreview.mockReset());
  afterEach(cleanup);

  it('RegisterPage shows it (email signup)', () => {
    renderAt(<RegisterPage />, '/register');
    expect(termsLink()).toBeInTheDocument();
    expect(privacyLink()).toBeInTheDocument();
  });

  // Not a redundant case: /login has a "Continue with Google" button, and for a
  // Google user with no account that button IS the sign-up.
  it('LoginPage shows it (its Google button creates accounts)', () => {
    renderAt(<LoginPage />, '/login');
    expect(screen.getByRole('button', { name: /Continue with Google/ })).toBeInTheDocument();
    expect(termsLink()).toBeInTheDocument();
    expect(privacyLink()).toBeInTheDocument();
  });

  it('AcceptInvitePage shows it for a brand-new invitee', async () => {
    mockPreview.mockResolvedValue({
      email: 'new@acme.com',
      org_name: 'Acme',
      role_name: 'sales_rep',
      status: 'valid',
      has_account: false,
    });

    renderAt(<AcceptInvitePage />, '/accept-invite?token=abc');

    await waitFor(() => expect(termsLink()).toBeInTheDocument());
    expect(privacyLink()).toBeInTheDocument();
  });

  // Someone who already has an account agreed when they made it; re-asking on a
  // workspace join is noise.
  it('AcceptInvitePage hides it for an invitee who already has an account', async () => {
    mockPreview.mockResolvedValue({
      email: 'existing@acme.com',
      org_name: 'Acme',
      role_name: 'sales_rep',
      status: 'valid',
      has_account: true,
    });

    renderAt(<AcceptInvitePage />, '/accept-invite?token=abc');

    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Accept & continue/ })).toBeInTheDocument(),
    );
    expect(screen.queryByRole('link', { name: 'Terms of Service' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Privacy Policy' })).not.toBeInTheDocument();
  });
});

describe('signup accessibility fixes (U7.6)', () => {
  beforeEach(() => mockPreview.mockReset());
  afterEach(cleanup);

  it('RegisterPage names the Workspace Type radio group and reports the selection', () => {
    renderAt(<RegisterPage />, '/register');

    // Previously a <label htmlFor="reg-orgtype"> pointing at a non-existent id.
    const group = screen.getByRole('radiogroup', { name: 'Workspace Type' });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Company' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Personal' })).not.toBeChecked();
  });

  // A new-password field with no username field beside it gives a password manager
  // nothing to bind the saved credential to.
  it('RegisterPage exposes the email as the credential username', () => {
    renderAt(<RegisterPage />, '/register');
    expect(screen.getByLabelText('Email')).toHaveAttribute('autocomplete', 'username');
    expect(screen.getByLabelText('Password')).toHaveAttribute('autocomplete', 'new-password');
  });

  it('AcceptInvitePage labels every input and supplies a username for the password manager', async () => {
    mockPreview.mockResolvedValue({
      email: 'new@acme.com',
      org_name: 'Acme',
      role_name: 'sales_rep',
      status: 'valid',
      has_account: false,
    });

    renderAt(<AcceptInvitePage />, '/accept-invite?token=abc');

    // All four inputs were placeholder-only — no label, no id, no aria-label.
    const email = await screen.findByLabelText('Email');
    expect(email).toHaveAttribute('autocomplete', 'username');
    expect(email).toHaveValue('new@acme.com');
    expect(email).toHaveAttribute('readonly');

    expect(screen.getByLabelText('First name')).toBeInTheDocument();
    expect(screen.getByLabelText('Last name')).toBeInTheDocument();
    expect(screen.getByLabelText('Create a password')).toHaveAttribute('autocomplete', 'new-password');
    expect(screen.getByLabelText('Confirm password')).toHaveAttribute('autocomplete', 'new-password');
  });
});
