import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LeadSource } from '../../../features/integrations/types';

// IntegrationsSection is where an admin mints the credential a third party uses to
// send leads in. The tests below pin the two things that are silent when wrong: a
// one-time key must be shown exactly once and never persist, and an integrator must
// be told how to actually use it.

vi.mock('../../../features/integrations/api', () => ({
  listSources: vi.fn(),
  createSource: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  deleteSource: vi.fn(),
  rotateKey: vi.fn(),
  listEvents: vi.fn(),
}));

import { listSources, createSource } from '../../../features/integrations/api';
import IntegrationsSection from '../IntegrationsSection';

const SOURCE: LeadSource = {
  id: 's1',
  org_id: 'o1',
  kind: 'api',
  name: 'Website form',
  token_prefix: 'crm_lead_ab12cd34',
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

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <IntegrationsSection />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});
afterEach(cleanup);

describe('IntegrationsSection', () => {
  it('lists sources with a masked key prefix, never a usable key', async () => {
    vi.mocked(listSources).mockResolvedValue([SOURCE]);
    renderSection();

    expect(await screen.findByText('Website form')).toBeInTheDocument();
    // The prefix is a recognizable hint; the real key is not recoverable from it.
    expect(screen.getByText(/crm_lead_ab12cd34/)).toBeInTheDocument();
    expect(screen.getByText('active')).toBeInTheDocument();
  });

  it('shows an empty state that tells a new admin what to do', async () => {
    vi.mocked(listSources).mockResolvedValue([]);
    renderSection();
    expect(await screen.findByText('No lead sources yet')).toBeInTheDocument();
  });

  it('surfaces a load error instead of rendering an empty list', async () => {
    vi.mocked(listSources).mockRejectedValue(new Error('boom'));
    renderSection();
    // An empty list and a failed fetch must not look identical — that is how a
    // broken integrations page reads as "you have no sources".
    expect(await screen.findByText('boom')).toBeInTheDocument();
    expect(screen.queryByText('No lead sources yet')).not.toBeInTheDocument();
  });

  it('reveals the new key exactly once, with the setup steps to use it', async () => {
    vi.mocked(listSources).mockResolvedValue([]);
    vi.mocked(createSource).mockResolvedValue({
      source: SOURCE,
      plaintext_key: 'crm_lead_SECRETVALUE',
    });
    renderSection();

    // Two "New source" buttons render on an empty list (the header and the empty
    // state's call to action); either opens the same form.
    fireEvent.click((await screen.findAllByRole('button', { name: /new source/i }))[0]);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Website form' } });
    // Routing is a required choice — a source that silently produces unowned
    // contacts is the failure this platform exists to prevent.
    fireEvent.change(screen.getByLabelText('Who gets these leads'), { target: { value: 'unassigned' } });
    fireEvent.click(screen.getByRole('button', { name: /create source/i }));

    // The key, once.
    expect(await screen.findByTestId('secret-value')).toHaveTextContent('crm_lead_SECRETVALUE');
    // A key with no instructions is not an integration — the recipe ships with it.
    // The endpoint appears twice by design: once as the URL, once inside the curl.
    expect(screen.getAllByText(/api\/capture\/leads/).length).toBeGreaterThan(0);
    expect(screen.getByText(/Make \(free tier\)/)).toBeInTheDocument();
    // Make is named before Zapier deliberately: Zapier's webhook action is
    // paid-only, so leading with it walks people into a paywall.
    expect(screen.getByText(/Zapier \(paid plan\)/)).toBeInTheDocument();

    // Dismiss (the acknowledgement gate is SecretReveal's own contract).
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(screen.getByRole('button', { name: /^done$/i }));
    await waitFor(() => expect(screen.queryByTestId('secret-value')).not.toBeInTheDocument());
  });

  it('keeps the list visible when creating fails', async () => {
    vi.mocked(listSources).mockResolvedValue([SOURCE]);
    vi.mocked(createSource).mockRejectedValue(new Error('name taken'));
    renderSection();

    fireEvent.click(await screen.findByRole('button', { name: /new source/i }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'dupe' } });
    fireEvent.change(screen.getByLabelText('Who gets these leads'), { target: { value: 'unassigned' } });
    fireEvent.click(screen.getByRole('button', { name: /create source/i }));

    // An action error must not replace the page: the admin needs to still see what
    // they have while they fix the input.
    expect(await screen.findByText('name taken')).toBeInTheDocument();
    expect(screen.getByText('Website form')).toBeInTheDocument();
  });
});

// The section has THREE registration sites (SETTINGS_SECTIONS, the App.tsx routes,
// the api layer) and missing the first is silent: the layout's deep-link guard
// rejects any first segment not in the visible sections, so /settings/integrations
// redirects to the default section and the routes you just wired never render —
// with no error to say why. This pins the entry and its capability gate.
describe('Integrations settings registration', () => {
  it('is registered as a capability-gated workspace section', async () => {
    const { SETTINGS_SECTIONS, visibleSections } = await import('../SettingsLayout');
    const entry = SETTINGS_SECTIONS.find((s) => s.path === 'integrations');
    expect(entry, 'no SETTINGS_SECTIONS entry — the route would silently redirect').toBeDefined();
    expect(entry!.group).toBe('workspace');

    // Visible only with the capability, in the palette as well as the nav.
    const withCap = visibleSections((c: string) => c === 'integrations.manage');
    expect(withCap.some((s) => s.path === 'integrations')).toBe(true);
    const without = visibleSections(() => false);
    expect(without.some((s) => s.path === 'integrations')).toBe(false);
  });

  it('does not change where a bare /settings lands', async () => {
    const { defaultSectionPath } = await import('../SettingsLayout');
    // defaultSectionPath returns the FIRST visible workspace section. Inserting
    // integrations above 'general' would silently relocate every admin's landing
    // page — so an admin with both must still land on General.
    expect(defaultSectionPath((c: string) => c === 'org.settings' || c === 'integrations.manage')).toBe('general');
    // An integrations-only admin still gets a page they can use.
    expect(defaultSectionPath((c: string) => c === 'integrations.manage')).toBe('integrations');
  });

  it('will not create a source until routing is chosen', async () => {
    // The live defect this closes: the form used to send only {name, update_policy},
    // so every source created through the UI produced unowned contacts — invisible to
    // every own-scoped rep.
    vi.mocked(listSources).mockResolvedValue([]);
    renderSection();

    fireEvent.click((await screen.findAllByRole('button', { name: /new source/i }))[0]);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Website form' } });

    expect(screen.getByRole('button', { name: /create source/i })).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Who gets these leads'), { target: { value: 'unassigned' } });
    expect(screen.getByRole('button', { name: /create source/i })).toBeEnabled();
  });

  it('says out loud that unassigned leads are invisible to own-scoped reps', async () => {
    vi.mocked(listSources).mockResolvedValue([]);
    renderSection();

    fireEvent.click((await screen.findAllByRole('button', { name: /new source/i }))[0]);
    fireEvent.change(screen.getByLabelText('Who gets these leads'), { target: { value: 'unassigned' } });

    expect(screen.getByText(/will not see these leads/i)).toBeInTheDocument();
  });
});
