import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SystemTemplateSummary } from '../../../lib/api';

// Why this section exists: the starter-template picker used to live ONLY inside the
// dashboard setup checklist, which hides itself once every step is done or the card
// is dismissed. A finished workspace therefore had no way to reach the 25 industry
// templates, and Settings is where people looked.

vi.mock('../../../lib/api', () => ({
  listTemplates: vi.fn(),
}));

vi.mock('../../../features/onboarding/StarterTemplateModal', () => ({
  default: ({ open }: { open: boolean }) => (open ? <div data-testid="picker" /> : null),
}));

import { listTemplates } from '../../../lib/api';
import StarterTemplateSection from '../StarterTemplateSection';

function tpl(slug: string, name: string, category: string, applied = false): SystemTemplateSummary {
  return {
    slug, name, category, description: `${name} desc`, icon: '🏠', sort_order: 10,
    stage_count: 7, object_count: 2, field_count: 8, workflow_count: 3,
    has_kb: true, applied,
  };
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter><StarterTemplateSection /></MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.mocked(listTemplates).mockResolvedValue([
    tpl('real_estate', 'Real Estate', 'property'),
    tpl('b2b_saas', 'B2B SaaS', 'software'),
    tpl('dental_practice', 'Dental Practice', 'health', true),
  ]);
});
afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe('StarterTemplateSection', () => {
  it('states how many templates are available, and across how many industries', async () => {
    renderSection();
    // The count is its own bold span, so match it exactly rather than by a
    // /templates/ regex — that also hits the heading and the button label.
    expect(await screen.findByText('3')).toBeInTheDocument();
    expect(screen.getByText(/across 3 industries/)).toBeInTheDocument();
  });

  it('names the templates already applied, so re-applying is an informed choice', async () => {
    renderSection();
    await waitFor(() => expect(screen.getByText(/already applied here/)).toBeInTheDocument());
    expect(screen.getByText(/Dental Practice/)).toBeInTheDocument();
  });

  it('does not claim anything is applied on a fresh workspace', async () => {
    vi.mocked(listTemplates).mockResolvedValue([tpl('real_estate', 'Real Estate', 'property')]);
    renderSection();
    await screen.findByText('1');
    expect(screen.queryByText(/already applied here/)).not.toBeInTheDocument();
  });

  // The reassurance that matters most: someone reaching this from Settings has a
  // configured workspace and needs to know a template will not overwrite it.
  it('says applying is additive', async () => {
    renderSection();
    await waitFor(() => expect(screen.getByText(/adds/)).toBeInTheDocument());
    expect(screen.getByText(/left untouched/)).toBeInTheDocument();
  });

  it('opens the picker on demand, not on load', async () => {
    renderSection();
    const btn = await screen.findByRole('button', { name: /browse templates/i });
    expect(screen.queryByTestId('picker')).not.toBeInTheDocument();
    btn.click();
    await waitFor(() => expect(screen.getByTestId('picker')).toBeInTheDocument());
  });

  // A failed catalog fetch must not strand the page: the button still opens the
  // modal, which owns the real error state.
  it('still offers the picker when the catalog fetch fails', async () => {
    vi.mocked(listTemplates).mockRejectedValue(new Error('network'));
    renderSection();
    expect(await screen.findByRole('button', { name: /browse templates/i })).toBeInTheDocument();
  });
});
