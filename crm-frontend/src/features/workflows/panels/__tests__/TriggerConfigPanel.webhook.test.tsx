import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within, act } from '@testing-library/react';
import { useBuilderStore } from '../../store';
import { TriggerConfigPanel } from '../TriggerConfigPanel';
import { getWebhookToken, revealWebhookSecret, regenerateWebhookSecret } from '../../api';
import type { TriggerSpec } from '../../types';

// ─────────────────────────────────────────────────────────────────────
// P17 — Webhook Setup section of TriggerConfigPanel.
//
// The store imports the API layer, so (matching the other panel tests) we
// mock '../../api' and drive the webhook calls per test.
// ─────────────────────────────────────────────────────────────────────

vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    getWebhookToken: vi.fn(),
    revealWebhookSecret: vi.fn(),
    regenerateWebhookSecret: vi.fn(),
    getWorkflowSchema: vi.fn(),
  };
});

const TOKEN_INFO = {
  token: 'tok_abc123',
  secret_masked: '••••••••••••cd34',
  url: 'http://localhost:8080/api/webhooks/inbound/tok_abc123',
};

// Full current secret (last 4 = cd34, matching the masked form above).
const REVEAL_SECRET = 'aaaabbbbccccddddeeeeffff0000111122223333444455556666777788cd34';

// Rotated secret (last 4 = beef → masked becomes 12 bullets + "beef").
const REGEN_INFO = {
  token: 'tok_abc123',
  secret: 'ffffeeeeddddccccbbbbaaaa9999888877776666555544443333222211beef',
  url: 'http://localhost:8080/api/webhooks/inbound/tok_abc123',
};

function seedTrigger(trigger: TriggerSpec) {
  useBuilderStore.setState({ trigger });
}

beforeEach(() => {
  useBuilderStore.getState().reset();
  vi.mocked(getWebhookToken).mockResolvedValue(TOKEN_INFO);
  vi.mocked(revealWebhookSecret).mockResolvedValue(REVEAL_SECRET);
  vi.mocked(regenerateWebhookSecret).mockResolvedValue(REGEN_INFO);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe('TriggerConfigPanel — Webhook Setup (P17)', () => {
  it('shows the URL and masked secret, with a collapsible curl example and a payload-fields accordion', async () => {
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    expect(await screen.findByText(TOKEN_INFO.url)).toBeInTheDocument();
    expect(screen.getByText(/This URL is permanent/i)).toBeInTheDocument();
    expect(screen.getByText(TOKEN_INFO.secret_masked)).toBeInTheDocument();
    expect(revealWebhookSecret).not.toHaveBeenCalled();

    // curl is collapsed by default; expanding pre-fills URL + sample body + a
    // signature placeholder.
    expect(screen.queryByText((_, el) => el?.tagName === 'PRE')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Test with curl/i }));
    const pre = await screen.findByText((_, el) =>
      el?.tagName === 'PRE' && el.textContent?.includes('curl -X POST') === true,
    );
    expect(pre.textContent).toContain(TOKEN_INFO.url);
    expect(pre.textContent).toContain('"email":"jane@example.com"');
    expect(pre.textContent).toContain('sha256=<hmac-sha256-of-body>');

    // Payload fields are an accordion, collapsed by default.
    expect(screen.queryByText(/is required and identifies the contact/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Payload Fields/i }));
    expect(await screen.findByText(/is required and identifies the contact/i)).toBeInTheDocument();
  });

  it('reveals the full secret on demand, then hides it', async () => {
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);
    expect(screen.getByText(TOKEN_INFO.secret_masked)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));

    expect(await screen.findByText(REVEAL_SECRET)).toBeInTheDocument();
    expect(screen.getByText(/Visible for 30 seconds/i)).toBeInTheDocument();
    expect(revealWebhookSecret).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole('button', { name: 'Hide' }));
    expect(screen.queryByText(REVEAL_SECRET)).not.toBeInTheDocument();
    expect(screen.getByText(TOKEN_INFO.secret_masked)).toBeInTheDocument();
  });

  it('re-fetches the secret on each reveal (never cached client-side)', async () => {
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);

    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));
    expect(await screen.findByText(REVEAL_SECRET)).toBeInTheDocument();
    expect(revealWebhookSecret).toHaveBeenCalledTimes(1);

    // Hide drops the secret from memory.
    fireEvent.click(screen.getByRole('button', { name: 'Hide' }));
    expect(screen.queryByText(REVEAL_SECRET)).not.toBeInTheDocument();

    // Revealing again triggers a fresh server fetch (no client-side cache).
    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));
    expect(await screen.findByText(REVEAL_SECRET)).toBeInTheDocument();
    expect(revealWebhookSecret).toHaveBeenCalledTimes(2);
  });

  it('auto-hides the revealed secret after the 30s timeout', async () => {
    const setTimeoutSpy = vi.spyOn(window, 'setTimeout');
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);
    fireEvent.click(screen.getByRole('button', { name: 'Reveal' }));
    await screen.findByText(REVEAL_SECRET);

    // A 30s auto-hide was scheduled; fire it deterministically.
    const hideCall = setTimeoutSpy.mock.calls.find((c) => c[1] === 30000);
    expect(hideCall).toBeTruthy();
    act(() => { (hideCall![0] as () => void)(); });

    expect(screen.queryByText(REVEAL_SECRET)).not.toBeInTheDocument();
    expect(screen.getByText(TOKEN_INFO.secret_masked)).toBeInTheDocument();
  });

  it('copies the full secret without revealing it on screen', async () => {
    const writeText = vi.fn();
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });

    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);
    fireEvent.click(screen.getByRole('button', { name: 'Copy signing secret' }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(REVEAL_SECRET));
    expect(revealWebhookSecret).toHaveBeenCalledTimes(1);
    // Copied, but never shown on screen.
    expect(screen.queryByText(REVEAL_SECRET)).not.toBeInTheDocument();
    expect(screen.getByText(TOKEN_INFO.secret_masked)).toBeInTheDocument();
  });

  it('rotates the secret via a confirm dialog and shows the new one', async () => {
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);

    // Open the confirm dialog.
    fireEvent.click(screen.getByRole('button', { name: /Regenerate/i }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText(/Existing webhook integrations will break until updated with the new secret/i)).toBeInTheDocument();

    // Confirm → endpoint called, new secret revealed, dialog closed.
    fireEvent.click(within(dialog).getByRole('button', { name: 'Regenerate secret' }));

    expect(await screen.findByText(REGEN_INFO.secret)).toBeInTheDocument();
    expect(regenerateWebhookSecret).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('cancels rotation from the dialog without calling the endpoint', async () => {
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);

    fireEvent.click(screen.getByRole('button', { name: /Regenerate/i }));
    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(regenerateWebhookSecret).not.toHaveBeenCalled();
  });

  it('copies the URL to the clipboard and confirms', async () => {
    const writeText = vi.fn();
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });

    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    await screen.findByText(TOKEN_INFO.url);
    fireEvent.click(screen.getByRole('button', { name: 'Copy webhook URL' }));

    expect(writeText).toHaveBeenCalledWith(TOKEN_INFO.url);
    expect(await screen.findByText('✓ Copied')).toBeInTheDocument();
  });

  it('does not render webhook setup for a non-webhook trigger', async () => {
    seedTrigger({ type: 'contact_created' });
    render(<TriggerConfigPanel />);

    await waitFor(() => {
      expect(screen.queryByText('Webhook Setup')).not.toBeInTheDocument();
    });
    expect(getWebhookToken).not.toHaveBeenCalled();
  });

  it('shows an admin/manager hint when the token fetch fails', async () => {
    vi.mocked(getWebhookToken).mockRejectedValue(new Error('forbidden'));
    seedTrigger({ type: 'webhook_inbound', params: { source: 'custom' } });
    render(<TriggerConfigPanel />);

    expect(await screen.findByText(/Couldn't load webhook setup/i)).toBeInTheDocument();
    expect(screen.getByText(/requires admin or manager access/i)).toBeInTheDocument();
  });
});
