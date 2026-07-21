import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.mock('../../../features/integrations/api', () => ({
  listSources: vi.fn(),
  createSource: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  deleteSource: vi.fn(),
  rotateKey: vi.fn(),
  rotateGoogleKey: vi.fn(),
  listEvents: vi.fn(),
  getMapping: vi.fn(),
  saveMapping: vi.fn(),
  sendTestLead: vi.fn(),
  listEventLog: vi.fn(),
  retryEvent: vi.fn(),
  RetryRefusedError: class RetryRefusedError extends Error {
    reason: string;
    constructor(message: string, reason: string) {
      super(message);
      this.name = 'RetryRefusedError';
      this.reason = reason;
    }
  },
}));

// Imported from the MOCKED module, not redeclared locally: the component's branch is
// an `instanceof`, which compares identity — a local twin would silently never match
// and the test would assert the fallback path while claiming to cover the real one.
import { listEventLog, retryEvent, RetryRefusedError } from '../../../features/integrations/api';
import type { EventPage, IntegrationEvent } from '../../../features/integrations/types';
import DeliveryLogSection from '../DeliveryLogSection';

const EV = (over: Partial<IntegrationEvent> = {}): IntegrationEvent => ({
  id: 'e1',
  org_id: 'o1',
  status: 'quarantined',
  attempts: 1,
  raw_payload: { email: 'a@x.com' },
  context: {},
  quarantined_fields: {},
  created_at: new Date().toISOString(),
  ...over,
});

const page = (over: Partial<EventPage> = {}): EventPage => ({
  events: [],
  sources: {},
  ...over,
});

const renderLog = (path = '/settings/integrations/deliveries') =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[path]}>
        <DeliveryLogSection />
      </MemoryRouter>
    </QueryClientProvider>,
  );

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(listEventLog).mockResolvedValue(page());
});
afterEach(cleanup);

describe('DeliveryLogSection', () => {
  // The row this whole page exists for. A provider delivery that failed before a
  // source was resolved has source_id NULL forever and is invisible to the per-source
  // log — so it must be both visible AND explained, not rendered as a blank cell that
  // reads like missing data.
  it('shows a delivery that failed before a source was matched, and says so', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ source_id: undefined, retry: { mode: 'refetch' } })],
    }));
    renderLog();

    expect(await screen.findByText('before a source was matched')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Fetch again/ })).toBeInTheDocument();
  });

  it('labels a deleted source rather than showing a bare id', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ source_id: 's1' })],
      sources: { s1: { name: 'Old website form', kind: 'form_embed', deleted: true } },
    }));
    renderLog();

    expect(await screen.findByText('Old website form')).toBeInTheDocument();
    expect(screen.getByText('(deleted)')).toBeInTheDocument();
  });

  // No button on rows that cannot be retried, and the reason in words instead. A
  // disabled control invites a click and explains nothing.
  it('offers no retry button for a non-retryable delivery, and explains why in Details', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ status: 'failed', retry: { mode: 'none', reason: 'sync_channel' } })],
    }));
    renderLog();

    expect(await screen.findByRole('button', { name: 'Details' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Fetch again/ })).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Details' }));
    // Radix portals: query the screen, not the container.
    expect(await screen.findByText(/Re-send it from the system that sent it/)).toBeInTheDocument();
  });

  // A "processed" row with no record is a LOST LEAD. Reporting it as "nothing to
  // retry" would file the one row worth investigating under the heading that means
  // everything is fine.
  it('describes a finished-but-unlinked delivery as unresolved, not as done', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ status: 'processed', retry: { mode: 'none', reason: 'incomplete' } })],
    }));
    renderLog();

    fireEvent.click(await screen.findByRole('button', { name: 'Details' }));
    const copy = await screen.findByText(/no record was ever linked to it/);
    expect(copy).toBeInTheDocument();
    expect(screen.queryByText('This delivery finished. There is nothing to retry.')).toBeNull();
  });

  it('queues a retry and surfaces the server reason when it refuses', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ retry: { mode: 'refetch' } })],
    }));
    vi.mocked(retryEvent).mockResolvedValueOnce(undefined);
    renderLog();

    fireEvent.click(await screen.findByRole('button', { name: /Fetch again/ }));
    await vi.waitFor(() => expect(retryEvent).toHaveBeenCalledWith('e1'));

    // A 409 carries a machine-readable reason, which the UI renders through its OWN
    // copy table rather than echoing the server's sentence.
    vi.mocked(retryEvent).mockRejectedValueOnce(new RetryRefusedError('conflict', 'form_closed'));
    fireEvent.click(screen.getByRole('button', { name: /Fetch again/ }));
    expect(await screen.findByText(/form is not enabled/)).toBeInTheDocument();
  });

  // The empty state must not assert a fact it cannot know. "No deliveries yet" is
  // true for a quiet workspace and false on a filtered view of a busy one.
  it('does not claim nothing was ever sent when a filter is active', async () => {
    renderLog('/settings/integrations/deliveries?status=failed');

    expect(await screen.findByText('No deliveries match these filters')).toBeInTheDocument();
    expect(screen.queryByText('No deliveries yet')).toBeNull();
  });

  it('keeps the honest empty state when nothing is filtered', async () => {
    renderLog();
    expect(await screen.findByText('No deliveries yet')).toBeInTheDocument();
  });

  // duplicate is declared and badged but never written by any code path — offering it
  // would be a filter that always answers empty.
  it('does not offer a status filter that can never match', async () => {
    renderLog();
    const select = await screen.findByLabelText('Filter by result');
    expect(within(select).queryByText(/duplicate/i)).toBeNull();
    expect(within(select).getByText('Failed')).toBeInTheDocument();
  });

  // An empty payload with no explanation is indistinguishable from a delivery that
  // stored nothing — which is what a bug looks like. Retention has to say it erased
  // something, or support cannot tell a redacted row from a broken write.
  it('says a payload was erased rather than rendering a bare empty object', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ raw_payload: {}, redacted_at: new Date().toISOString() })],
    }));
    renderLog();

    fireEvent.click(await screen.findByRole('button', { name: 'Details' }));
    expect(await screen.findByText(/was erased/)).toBeInTheDocument();
    expect(screen.getByText(/erased automatically after 90 days/)).toBeInTheDocument();
  });

  it('still shows the payload on a delivery that was not redacted', async () => {
    vi.mocked(listEventLog).mockResolvedValue(page({
      events: [EV({ raw_payload: { email: 'a@x.com' } })],
    }));
    renderLog();

    fireEvent.click(await screen.findByRole('button', { name: 'Details' }));
    expect(await screen.findByText(/a@x.com/)).toBeInTheDocument();
    expect(screen.queryByText(/was erased/)).toBeNull();
  });

  it('passes the URL filters through to the query', async () => {
    renderLog('/settings/integrations/deliveries?status=failed&unresolved=1&connection_id=c1');
    await screen.findByText('No deliveries match these filters');

    expect(listEventLog).toHaveBeenCalledWith(
      expect.objectContaining({
        status: ['failed'],
        unresolved: true,
        connection_id: 'c1',
      }),
    );
  });
});
