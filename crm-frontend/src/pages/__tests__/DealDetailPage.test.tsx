import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Deal, PipelineStage } from '../../lib/api';

// The page pulls deal/stages/activities/tasks/users through react-query; every
// named export it touches must exist on the mock.
vi.mock('../../lib/api', () => ({
  getDeal: vi.fn(),
  deleteDeal: vi.fn(),
  getActivities: vi.fn().mockResolvedValue([]),
  getStages: vi.fn(),
  // R9.3: the stage picker is keyed on the DEAL's pipeline, not the org's.
  getPipelines: vi.fn(),
  changeDealStage: vi.fn(),
  updateDeal: vi.fn(),
  getTasks: vi.fn().mockResolvedValue([]),
  createTask: vi.fn(),
  updateTask: vi.fn(),
  getUsers: vi.fn().mockResolvedValue([]),
  submitScoreDeal: vi.fn(),
  getAccessToken: vi.fn().mockReturnValue(null),
  // R7.7: money and dates on this page render in the WORKSPACE's currency and
  // locale, which useWorkspaceFormat reads from here. Without it the hook's
  // queryFn would throw and the page would silently fall back to USD — the
  // assertions below would still pass, which is exactly why it's pinned.
  getCurrentWorkspace: vi.fn().mockResolvedValue({
    id: 'o1', name: 'Acme', type: 'business',
    currency: 'EUR', locale: 'de-DE', timezone: 'UTC',
    member_count: 1, is_owner: true, require_two_factor: false,
    created_at: '2026-01-01T00:00:00Z',
  }),
}));

// Self-contained side panels (activity form, AI modals, voice notes) are out of
// scope here — the tests exercise the deal card's permission gates.
vi.mock('../../components/deals/ActivityForm', () => ({ default: () => null }));
vi.mock('../../components/ai/EmailComposer', () => ({ default: () => null }));
vi.mock('../../components/ai/MeetingSummary', () => ({ default: () => null }));
vi.mock('../../components/voice/VoiceUploader', () => ({ default: () => null }));
vi.mock('../../components/voice/VoiceLibrary', () => ({ default: () => null }));

// U3.7: the page gates Edit/stage-move/Won/Lost/Delete on the caller's OLS
// bits. Tests flip individual bits through this map; anything unset stays
// allowed.
let objectAccess: Record<string, boolean> = {};
vi.mock('../../lib/auth', () => ({
  usePermissions: () => ({
    can: () => true,
    canAccess: (slug: string, action: string) => objectAccess[`${slug}.${action}`] ?? true,
    loaded: true,
  }),
}));

import { getDeal, getPipelines, getStages } from '../../lib/api';
import DealDetailPage from '../DealDetailPage';

const stages: PipelineStage[] = [
  { id: 's1', org_id: 'o1', name: 'Qualification', position: 0, color: '#3b82f6', is_won: false, is_lost: false },
  { id: 's2', org_id: 'o1', name: 'Proposal', position: 1, color: '#8b5cf6', is_won: false, is_lost: false },
  { id: 's3', org_id: 'o1', name: 'Won', position: 2, color: '#10b981', is_won: true, is_lost: false },
  { id: 's4', org_id: 'o1', name: 'Lost', position: 3, color: '#ef4444', is_won: false, is_lost: true },
];

const deal: Deal = {
  id: 'd1', org_id: 'o1', title: 'Acme renewal', value: 1000, probability: 50,
  stage_id: 's1', stage: stages[0], is_won: false, is_lost: false,
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
};

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/deals/d1']}>
        <Routes>
          <Route path="/deals/:id" element={<DealDetailPage />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  objectAccess = {};
  vi.mocked(getPipelines).mockResolvedValue([{ id: 'p-default', org_id: 'org-1', name: 'Sales Pipeline', position: 0, is_default: true, created_at: '', updated_at: '' }]);
  vi.mocked(getDeal).mockResolvedValue(deal);
  vi.mocked(getStages).mockResolvedValue(stages);
});

describe('DealDetailPage OLS gates', () => {
  it('shows the stage selector and Won/Lost actions when the role can edit', async () => {
    renderPage();

    expect(await screen.findByText('Move to stage')).toBeInTheDocument();
    // Non-terminal stages render as move buttons. AWAITED, not sync: since R9.3
    // the stage list is keyed on the deal's own pipeline, so it cannot be
    // fetched until the deal resolves. "Move to stage" therefore paints one
    // round trip before its buttons do.
    expect(await screen.findByRole('button', { name: 'Proposal' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mark Won' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Mark Lost' })).toBeInTheDocument();
  });

  it('replaces the stage selector with a static pill when edit is denied', async () => {
    objectAccess['deal.edit'] = false;

    renderPage();

    expect(await screen.findByText('Acme renewal')).toBeInTheDocument();
    // The interaction is gone…
    expect(screen.queryByText('Move to stage')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Proposal' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Mark Won' })).not.toBeInTheDocument();
    // …but the information stays: the current stage renders as a plain pill
    // ("Qualification" also appears in the header status pill).
    expect(screen.getByText('Stage')).toBeInTheDocument();
    expect(screen.getAllByText('Qualification').length).toBeGreaterThan(0);
  });
});

describe('DealDetailPage renders money in the workspace currency (R7.7)', () => {
  // Intl uses U+00A0 between a de-DE amount and its symbol.
  const norm = (s: string) => s.replace(/\s/g, ' ');
  const eur = (n: number) =>
    norm(new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n));

  it('formats Value and Expected Revenue as EUR/de-DE, not a hardcoded "$"', async () => {
    renderPage();

    await screen.findByText('Acme renewal');

    // value 1000 → "1.000,00 €"; expected revenue 1000 × 50% → "500,00 €".
    await waitFor(() => {
      expect(screen.getByText((c) => norm(c) === eur(1000))).toBeInTheDocument();
    });
    expect(screen.getByText((c) => norm(c) === eur(500))).toBeInTheDocument();

    // The old hardcoded rendering was "$1,000" / "$500" — neither may survive.
    expect(screen.queryByText('$1,000')).not.toBeInTheDocument();
    expect(screen.queryByText('$500')).not.toBeInTheDocument();
  });
});
