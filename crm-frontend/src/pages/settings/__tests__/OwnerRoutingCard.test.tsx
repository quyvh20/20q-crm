import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LeadSource } from '../../../features/integrations/types';

// Owner routing is the difference between a lead and a lost lead, so this card's
// job is to never let an admin believe a rotation is healthier — or emptier — than
// it is.

vi.mock('../../../lib/api', () => ({
  getWorkspaceMembers: vi.fn(),
}));

vi.mock('../../../features/integrations/queries', () => ({
  useUpdateSource: vi.fn(),
}));

import { getWorkspaceMembers } from '../../../lib/api';
import { useUpdateSource } from '../../../features/integrations/queries';
import OwnerRoutingCard from '../OwnerRoutingCard';

const member = (id: string, name: string) => ({
  user_id: id,
  email: `${name}@x.test`,
  first_name: name,
  last_name: '',
  full_name: name,
  role_id: 'r1',
  role: 'Sales',
  status: 'active',
});

const SOURCE: LeadSource = {
  id: 's1',
  org_id: 'o1',
  kind: 'api',
  name: 'Website form',
  token_prefix: 'crm_lead_ab12',
  target_slug: 'contact',
  match_fields: ['email'],
  field_map: {},
  owner_pool: [],
  batch_enroll_automation: false,
  update_policy: 'fill_blank_only',
  config: {},
  status: 'active',
  consecutive_failures: 0,
  daily_cap: 0,
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const mutateAsync = vi.fn();

function renderCard(source: LeadSource) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OwnerRoutingCard source={source} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mutateAsync.mockResolvedValue(SOURCE);
  vi.mocked(useUpdateSource).mockReturnValue({ mutateAsync, isPending: false } as never);
  vi.mocked(getWorkspaceMembers).mockResolvedValue([
    member('u1', 'Ada'),
    member('u2', 'Grace'),
  ] as never);
});
afterEach(cleanup);

describe('OwnerRoutingCard', () => {
  it('shows the rotation in order, because the order IS the rotation', async () => {
    renderCard({ ...SOURCE, owner_pool: ['u2', 'u1'] });

    const items = await screen.findAllByRole('listitem');
    expect(items[0]).toHaveTextContent('Grace');
    expect(items[1]).toHaveTextContent('Ada');
  });

  it('badges an inactive member from the SERVER, not from a member-list join', async () => {
    // The server is the only authority on who can receive leads. Deriving this in the
    // browser would mean a slow or failed member fetch badges healthy reps as dead.
    renderCard({ ...SOURCE, owner_pool: ['u1', 'u2'], owner_pool_inactive: ['u2'] });

    expect(await screen.findByText("Can't receive leads")).toBeInTheDocument();
  });

  it('warns when nobody in the rotation can receive leads', async () => {
    renderCard({ ...SOURCE, owner_pool: ['u1'], owner_pool_inactive: ['u1'] });

    expect(await screen.findByText(/Nobody in this rotation can receive leads/i)).toBeInTheDocument();
  });

  it('keeps a saved rotation when the member list fails to load', async () => {
    // The trap: seeding editor state from an intersection with the member list means a
    // failed fetch renders an EMPTY rotation, and the next save PATCHes owner_pool: []
    // — destroying routing config because one request blipped.
    vi.mocked(getWorkspaceMembers).mockRejectedValue(new Error('network'));
    renderCard({ ...SOURCE, owner_pool: ['u1', 'u2'] });

    await waitFor(() => expect(screen.getAllByRole('listitem')).toHaveLength(2));
    // Unknown, NOT inactive — we could not load names, which says nothing about them.
    expect(screen.getAllByText('Unknown member')).toHaveLength(2);
    expect(screen.queryByText("Can't receive leads")).not.toBeInTheDocument();
  });

  it('offers the fallback owner in BOTH modes', async () => {
    // A toggle that hid and nulled the fallback would destroy the safety net at the
    // exact moment an admin adopts rotations.
    renderCard({ ...SOURCE, owner_pool: ['u1'] });

    expect(
      await screen.findByText(/Fallback owner — used when nobody in the rotation is available/i),
    ).toBeInTheDocument();
  });

  it('warns when a single-owner source has no owner at all', async () => {
    renderCard(SOURCE); // no pool, no default owner

    expect(await screen.findByText(/will not see them/i)).toBeInTheDocument();
  });
});
