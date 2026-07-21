import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LeadSource } from '../../../features/integrations/types';

// The setup card is the whole surface for a feature whose failure modes are silent:
// a form that accepts nothing looks identical to one nobody has submitted to, and
// an origin allowlist looks like a security boundary until someone tests it with
// curl. Both are pinned here as copy, deliberately.

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
}));

import { updateSource } from '../../../features/integrations/api';
import FormEmbedSetupCard from '../FormEmbedSetupCard';

function makeSource(over: Partial<LeadSource> = {}): LeadSource {
  return {
    id: 's1',
    org_id: 'o1',
    kind: 'form_embed',
    name: 'Website contact form',
    token_prefix: 'crm_lead_ab12cd34',
    target_slug: 'contact',
    match_fields: ['email'],
    field_map: {},
    owner_pool: [],
    batch_enroll_automation: false,
    update_policy: 'fill_blank_only',
    public_token: 'tok_form123',
    allowed_origins: ['https://customer.com'],
    config: {
      form: {
        enabled: true,
        honeypot: 'company_website',
        fields: [
          { name: 'email', label: 'Email', type: 'email', required: true },
          { name: 'first_name', label: 'First name', type: 'text', required: false },
        ],
      },
    },
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
      <FormEmbedSetupCard source={source} />
    </QueryClientProvider>,
  );
}

describe('FormEmbedSetupCard', () => {
  beforeEach(() => {
    vi.mocked(updateSource).mockResolvedValue(makeSource());
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('generates a snippet posting to this form’s own endpoint', () => {
    renderCard(makeSource());
    const snippet = screen.getByText((t) => t.includes('/api/capture/forms/tok_form123'));
    expect(snippet).toBeInTheDocument();
  });

  it('the snippet sends the page address, so UTMs ride along without extra work', () => {
    renderCard(makeSource());
    expect(screen.getByText((t) => t.includes('page_url: location.href'))).toBeInTheDocument();
    expect(screen.getByText((t) => t.includes('document.referrer'))).toBeInTheDocument();
  });

  it('renders an input per declared field, and the honeypot off-screen', () => {
    renderCard(makeSource());
    const snippet = screen.getByText((t) => t.includes('<form id="crm-lead-form">')).textContent ?? '';
    expect(snippet).toContain('name="email"');
    expect(snippet).toContain('name="first_name"');
    // Positioned off-screen rather than display:none — some bots skip hidden
    // inputs, and the whole point is that they fill this one in.
    expect(snippet).toContain('company_website');
    expect(snippet).toContain('left:-9999px');
  });

  it('warns loudly when no website is allowed yet — the state of every new form', () => {
    renderCard(makeSource({ allowed_origins: [] }));
    expect(screen.getByText(/No website is allowed to submit/i)).toBeInTheDocument();
  });

  it('says plainly that the allowlist does not stop a script', () => {
    // If this sentence disappears, someone eventually removes the bot check
    // "because we have an allowlist".
    renderCard(makeSource());
    expect(screen.getByText(/does not stop a script/i)).toBeInTheDocument();
  });

  it('adding a website saves the normalized list', async () => {
    renderCard(makeSource());
    const input = screen.getByPlaceholderText('https://example.com');
    fireEvent.change(input, { target: { value: 'https://shop.customer.com' } });
    fireEvent.click(screen.getByRole('button', { name: /^add$/i }));

    await waitFor(() => {
      expect(updateSource).toHaveBeenCalledWith('s1', {
        allowed_origins: ['https://customer.com', 'https://shop.customer.com'],
      });
    });
  });

  it('removing a website saves the shorter list', async () => {
    renderCard(makeSource());
    fireEvent.click(screen.getByRole('button', { name: /remove https:\/\/customer\.com/i }));
    await waitFor(() => {
      expect(updateSource).toHaveBeenCalledWith('s1', { allowed_origins: [] });
    });
  });

  it('explains that only declared fields are accepted', () => {
    renderCard(makeSource());
    expect(screen.getByText(/Only these fields are accepted/i)).toBeInTheDocument();
  });

  it('shows the bot check as optional and says what a form without it has', () => {
    renderCard(makeSource());
    expect(screen.getByText(/only the hidden honeypot field and the daily limit/i)).toBeInTheDocument();
  });

  it('the Turnstile secret is write-only — configured shows as a badge, never a value', () => {
    renderCard(makeSource({ turnstile_configured: true }));
    expect(screen.getByText('configured')).toBeInTheDocument();
    const secret = screen.getByLabelText(/secret key/i) as HTMLInputElement;
    expect(secret.value).toBe('');
    expect(secret.type).toBe('password');
  });
});
