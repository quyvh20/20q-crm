import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

const verifyTwoFactor = vi.fn();
const navigate = vi.fn();

vi.mock('../../lib/auth', () => ({
  useAuth: () => ({ verifyTwoFactor }),
}));

vi.mock('react-router-dom', async (orig) => {
  const actual = await orig<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => navigate };
});

import { TwoFactorVerifyError } from '../../lib/api';
import TwoFactorChallengePage from '../TwoFactorChallengePage';

// state=undefined models the GOOGLE flow, where the challenge rides in an httpOnly
// cookie and never reaches the SPA.
const renderPage = (state: unknown = { challengeToken: 'chal-1' }) =>
  render(
    <MemoryRouter initialEntries={[{ pathname: '/login/2fa', state }]}>
      <TwoFactorChallengePage />
    </MemoryRouter>,
  );

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('TwoFactorChallengePage', () => {
  it('exchanges the challenge from router state for a session', async () => {
    verifyTwoFactor.mockResolvedValue(undefined);
    renderPage();

    fireEvent.change(screen.getByLabelText(/authentication code/i), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    await waitFor(() => expect(verifyTwoFactor).toHaveBeenCalledWith('chal-1', '123456'));
    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('sends an empty challenge in the Google flow — the server reads its cookie', async () => {
    verifyTwoFactor.mockResolvedValue(undefined);
    // null, not undefined: undefined would re-apply renderPage's default state.
    renderPage(null);

    fireEvent.change(screen.getByLabelText(/authentication code/i), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    await waitFor(() => expect(verifyTwoFactor).toHaveBeenCalledWith('', '123456'));
  });

  it('honors a safe ?next return-to path after verifying', async () => {
    verifyTwoFactor.mockResolvedValue(undefined);
    renderPage({ challengeToken: 'chal-1', next: '/deals/42' });

    fireEvent.change(screen.getByLabelText(/authentication code/i), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/deals/42', { replace: true }));
  });

  it('refuses an off-origin next path', async () => {
    verifyTwoFactor.mockResolvedValue(undefined);
    renderPage({ challengeToken: 'chal-1', next: '//evil.com' });

    fireEvent.change(screen.getByLabelText(/authentication code/i), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/', { replace: true }));
  });

  it('a wrong code (401) keeps the form so the user can retry', async () => {
    verifyTwoFactor.mockRejectedValue(new TwoFactorVerifyError("that code isn't right", 401));
    renderPage();

    fireEvent.change(screen.getByLabelText(/authentication code/i), { target: { value: '000000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    expect(await screen.findByText(/that code isn't right/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Verify' })).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });

  it('a dead challenge (429) kills the form and sends the user back to sign in', async () => {
    verifyTwoFactor.mockRejectedValue(
      new TwoFactorVerifyError('too many incorrect codes — please sign in again', 429),
    );
    renderPage();

    fireEvent.change(screen.getByLabelText(/authentication code/i), { target: { value: '000000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    expect(await screen.findByText(/too many incorrect codes/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Verify' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: /back to sign in/i })).toHaveAttribute('href', '/login');
  });

  it('offers a backup code instead of the authenticator', async () => {
    verifyTwoFactor.mockResolvedValue(undefined);
    renderPage();

    fireEvent.click(screen.getByRole('button', { name: /use a backup code instead/i }));
    const input = screen.getByLabelText(/backup code/i);
    expect(input).toHaveAttribute('placeholder', 'XXXXX-XXXXX');

    fireEvent.change(input, { target: { value: 'AAAAA-11111' } });
    fireEvent.click(screen.getByRole('button', { name: 'Verify' }));

    await waitFor(() => expect(verifyTwoFactor).toHaveBeenCalledWith('chal-1', 'AAAAA-11111'));
  });
});
