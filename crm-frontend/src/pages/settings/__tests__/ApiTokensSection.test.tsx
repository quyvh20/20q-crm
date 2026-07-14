import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react';

// The real capability catalog is imported by api.ts; the mock re-exports the
// constants the page reads so the scope list stays honest.
vi.mock('../../../lib/api', () => ({
  listApiTokens: vi.fn(),
  createApiToken: vi.fn(),
  revokeApiToken: vi.fn(),
  isTokenLive: (t: { revoked_at?: string; expires_at?: string }, now: number = Date.now()) => {
    if (t.revoked_at) return false;
    if (t.expires_at && new Date(t.expires_at).getTime() <= now) return false;
    return true;
  },
  SCOPE_RECORDS_READ: 'records.read',
  ALL_API_TOKEN_SCOPES: ['records.read', 'records.write', 'members.invite', 'audit.view'],
  API_TOKEN_SCOPE_LABELS: {
    'records.read': 'Read records',
    'records.write': 'Create/edit tasks, activities, notes & tags',
    'members.invite': 'Invite members',
    'audit.view': 'View audit log',
  },
  MAX_API_TOKENS_PER_USER: 20,
  DEFAULT_API_TOKEN_DAYS: 90,
}));

// The caller holds records.write only — so members.invite/audit.view must not be
// offered (the server would 403 them anyway: a token can never exceed its owner).
const authState = vi.hoisted(() => ({ held: ['records.write'] as string[], isOwner: false }));
vi.mock('../../../lib/auth', () => ({
  usePermissions: () => ({
    can: (code: string) => authState.held.includes(code),
    isOwner: authState.isOwner,
  }),
}));

import { listApiTokens, createApiToken, revokeApiToken, type APIToken } from '../../../lib/api';
import ApiTokensSection from '../ApiTokensSection';

const TOKENS: APIToken[] = [
  {
    id: 't1', name: 'Nightly export', prefix: 'crm_pat_a1b2', scopes: ['records.read'],
    created_at: '2026-07-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z',
  },
  {
    id: 't2', name: 'Old script', prefix: 'crm_pat_c3d4', scopes: ['records.write'],
    created_at: '2026-01-01T00:00:00Z', revoked_at: '2026-06-01T00:00:00Z',
  },
];

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  authState.held = ['records.write'];
  authState.isOwner = false;
  Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  vi.mocked(listApiTokens).mockResolvedValue(structuredClone(TOKENS));
});

describe('ApiTokensSection', () => {
  it('lists tokens with their prefix, scopes and revoked state', async () => {
    render(<ApiTokensSection />);

    expect(await screen.findByText('Nightly export')).toBeInTheDocument();
    expect(screen.getByText('crm_pat_a1b2…')).toBeInTheDocument();
    expect(screen.getByText('Revoked')).toBeInTheDocument();
    // A revoked token can't be revoked again — only the live one offers the button.
    expect(screen.getAllByRole('button', { name: 'Revoke' })).toHaveLength(1);
  });

  it('only offers scopes the caller actually holds, plus the token-only records.read', async () => {
    render(<ApiTokensSection />);
    fireEvent.click(await screen.findByRole('button', { name: /new token/i }));

    expect(screen.getByRole('checkbox', { name: /read records/i })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /create\/edit tasks/i })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /invite members/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /view audit log/i })).not.toBeInTheDocument();
  });

  it('offers every scope to the workspace owner', async () => {
    authState.isOwner = true;
    render(<ApiTokensSection />);
    fireEvent.click(await screen.findByRole('button', { name: /new token/i }));

    expect(screen.getByRole('checkbox', { name: /invite members/i })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /view audit log/i })).toBeInTheDocument();
  });

  it('creates a token and reveals the secret exactly once', async () => {
    vi.mocked(createApiToken).mockResolvedValue({
      token: { id: 't3', name: 'CI', prefix: 'crm_pat_e5f6', scopes: ['records.read'], created_at: '2026-07-14T00:00:00Z' },
      secret: 'crm_pat_e5f6_supersecret',
    });

    render(<ApiTokensSection />);
    fireEvent.click(await screen.findByRole('button', { name: /new token/i }));
    fireEvent.change(screen.getByLabelText(/what is this token for/i), { target: { value: 'CI' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create token' }));

    await waitFor(() =>
      expect(createApiToken).toHaveBeenCalledWith({ name: 'CI', scopes: ['records.read'], expires_in_days: 90 }),
    );

    // Shown once, behind an acknowledgement — there is no way to recover it later.
    expect(await screen.findByTestId('secret-value')).toHaveTextContent('crm_pat_e5f6_supersecret');
    const done = screen.getByRole('button', { name: 'Done' });
    expect(done).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox', { name: /copied my token/i }));
    fireEvent.click(done);
    expect(screen.queryByTestId('secret-value')).not.toBeInTheDocument();
  });

  it('refuses to submit a token with no name or no scopes', async () => {
    render(<ApiTokensSection />);
    fireEvent.click(await screen.findByRole('button', { name: /new token/i }));

    // No name yet.
    expect(screen.getByRole('button', { name: 'Create token' })).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/what is this token for/i), { target: { value: 'CI' } });
    expect(screen.getByRole('button', { name: 'Create token' })).toBeEnabled();

    // Unticking the only default scope disables it again — a scopeless token is a footgun.
    fireEvent.click(screen.getByRole('checkbox', { name: /read records/i }));
    expect(screen.getByRole('button', { name: 'Create token' })).toBeDisabled();
  });

  it('revokes a token behind a confirmation', async () => {
    vi.mocked(revokeApiToken).mockResolvedValue(undefined);
    render(<ApiTokensSection />);

    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent(/nightly export/i);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

    await waitFor(() => expect(revokeApiToken).toHaveBeenCalledWith('t1'));
  });

  it('keeps the list visible when an action fails (separate error banner)', async () => {
    vi.mocked(revokeApiToken).mockRejectedValue(new Error('nope'));
    render(<ApiTokensSection />);

    fireEvent.click(await screen.findByRole('button', { name: 'Revoke' }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }));

    expect(await screen.findByText('nope')).toBeInTheDocument();
    expect(screen.getByText('Nightly export')).toBeInTheDocument();
  });

  it('replaces the list with the error when the LOAD fails', async () => {
    vi.mocked(listApiTokens).mockRejectedValue(new Error('backend down'));
    render(<ApiTokensSection />);

    expect(await screen.findByText('backend down')).toBeInTheDocument();
    expect(screen.queryByText('Nightly export')).not.toBeInTheDocument();
  });
});
