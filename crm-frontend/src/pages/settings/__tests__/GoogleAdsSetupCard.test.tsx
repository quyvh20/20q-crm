import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LeadSource } from '../../../features/integrations/types';

// The google_ads setup panel is the ENTIRE integration surface — no OAuth, no
// connection flow. What these tests pin is the honesty of the two dangerous
// moments: the URL must contain the real token (a blank would paste a dead URL
// into Google), and rotation must say that the old key's leads are being
// rejected un-retried.

vi.mock('../../../features/integrations/api', () => ({
  listSources: vi.fn(),
  createSource: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  deleteSource: vi.fn(),
  rotateKey: vi.fn(),
  rotateGoogleKey: vi.fn(),
  listEvents: vi.fn(),
  sendTestLead: vi.fn(),
  getMapping: vi.fn(),
  saveMapping: vi.fn(),
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
  listSourceStats: vi.fn(),
}));

import { rotateGoogleKey } from '../../../features/integrations/api';
import GoogleAdsSetupCard from '../GoogleAdsSetupCard';

function makeSource(over: Partial<LeadSource> = {}): LeadSource {
  return {
    id: 's1',
    org_id: 'o1',
    kind: 'google_ads',
    name: 'Spring Google Form',
    token_prefix: 'crm_lead_ab12cd34',
    target_slug: 'contact',
    match_fields: ['email', 'phone'],
    field_map: {},
    owner_pool: [],
    batch_enroll_automation: false,
    update_policy: 'fill_blank_only',
    config: {},
    public_token: 'tok_abc123',
    status: 'active',
    consecutive_failures: 0,
    daily_cap: 1000,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  };
}

function renderCard(source: LeadSource) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <GoogleAdsSetupCard source={source} />
    </QueryClientProvider>,
  );
}

describe('GoogleAdsSetupCard', () => {
  beforeEach(() => {
    vi.mocked(rotateGoogleKey).mockResolvedValue({
      source: makeSource(),
      plaintext_key: '',
      google_key: 'crm_gads_NEWKEY123',
    });
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('renders the webhook URL with the real token', () => {
    renderCard(makeSource());
    expect(
      screen.getByText((t) => t.includes('/api/capture/google-ads/tok_abc123')),
    ).toBeInTheDocument();
  });

  it('walks the advertiser through the Google Ads editor', () => {
    renderCard(makeSource());
    expect(screen.getByText(/Export leads from Google Ads/i)).toBeInTheDocument();
    expect(screen.getByText(/Send test data/i)).toBeInTheDocument();
  });

  it('rotating shows the new key once and says the loss window out loud', async () => {
    renderCard(makeSource());
    fireEvent.click(screen.getByRole('button', { name: /rotate key/i }));

    await waitFor(() => expect(rotateGoogleKey).toHaveBeenCalledWith('s1'));
    expect(await screen.findByText('crm_gads_NEWKEY123')).toBeInTheDocument();
    // Google never retries a 4xx: the admin must hear that leads between
    // "rotate here" and "paste there" are gone from the webhook channel.
    expect(screen.getAllByText(/does not retry/i).length).toBeGreaterThan(0);
  });

  it('a failed rotation surfaces instead of pretending', async () => {
    vi.mocked(rotateGoogleKey).mockRejectedValue(new Error('boom'));
    renderCard(makeSource());
    fireEvent.click(screen.getByRole('button', { name: /rotate key/i }));
    expect(await screen.findByText('boom')).toBeInTheDocument();
  });
});
