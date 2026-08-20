import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AICopilotModal } from '../AICopilotModal';
import { useBuilderStore } from '../builderStore';
import type { Block } from '../blocks';

vi.mock('../../contentApi', () => ({ draftEmailAI: vi.fn() }));
import { draftEmailAI } from '../../contentApi';

const mockDraft = draftEmailAI as unknown as ReturnType<typeof vi.fn>;

function seed(blocks: Block[]) {
  useBuilderStore.getState().reset();
  useBuilderStore.getState().hydrate({
    name: 'n', subject: 'original subject', preheader: 'p',
    scope: ['contact', 'org', 'campaign'], blocks,
  });
}

const TEXT_BLOCK: Block = { id: 'b1', type: 'text', text: '<p>original words</p>', size: 22, color: '#112233' };

beforeEach(() => {
  vi.clearAllMocks();
  seed([TEXT_BLOCK]);
});
afterEach(cleanup);

async function generate(prompt = 'make it better') {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/How should this block change|What should it say/i), prompt);
  await user.click(screen.getByRole('button', { name: /Generate/i }));
  return user;
}

describe('AICopilotModal', () => {
  it('applies a rewrite to the block that was selected when the request STARTED', async () => {
    seed([TEXT_BLOCK, { id: 'b2', type: 'text', text: '<p>other block</p>' }]);
    useBuilderStore.getState().select('b1');
    mockDraft.mockResolvedValue({ blocks: [{ id: 'ai1', type: 'text', text: '<p>rewritten</p>' }] });

    render(<AICopilotModal open mode="rewrite" onClose={() => {}} />);
    const user = await generate('shorter');

    // The selection moves while the model is "thinking".
    act(() => { useBuilderStore.getState().select('b2'); });

    await user.click(await screen.findByRole('button', { name: /Use this rewrite/i }));

    const blocks = useBuilderStore.getState().blocks;
    expect(blocks[0].text).toBe('<p>rewritten</p>'); // the pinned target
    expect(blocks[1].text).toBe('<p>other block</p>'); // untouched
  });

  it('never writes undefined over fields the copilot did not return', async () => {
    useBuilderStore.getState().select('b1');
    // The model returns only text — label/title/alt are absent.
    mockDraft.mockResolvedValue({ blocks: [{ id: 'ai1', type: 'text', text: '<p>new words</p>' }] });

    render(<AICopilotModal open mode="rewrite" onClose={() => {}} />);
    const user = await generate();
    await user.click(await screen.findByRole('button', { name: /Use this rewrite/i }));

    const b = useBuilderStore.getState().blocks[0];
    expect(b.text).toBe('<p>new words</p>');
    // The author's own styling survives a rewrite.
    expect(b.size).toBe(22);
    expect(b.color).toBe('#112233');
  });

  it('refuses to apply a rewrite whose target was deleted mid-request', async () => {
    useBuilderStore.getState().select('b1');
    mockDraft.mockResolvedValue({ blocks: [{ id: 'ai1', type: 'text', text: '<p>rewritten</p>' }] });

    render(<AICopilotModal open mode="rewrite" onClose={() => {}} />);
    const user = await generate();

    act(() => { useBuilderStore.getState().removeBlock('b1'); });
    await user.click(await screen.findByRole('button', { name: /Use this rewrite/i }));

    // It must say so, not silently insert the block somewhere instead. The text
    // appears twice by design: once visibly, once in the screen-reader live region.
    expect((await screen.findAllByText(/no longer on the canvas/i)).length).toBeGreaterThan(0);
    expect(useBuilderStore.getState().blocks).toHaveLength(0);
  });

  it('discards a response that arrives after the dialog was reopened', async () => {
    useBuilderStore.getState().select('b1');
    let release: (v: unknown) => void = () => {};
    mockDraft.mockReturnValue(new Promise((res) => { release = res; }));

    const { rerender } = render(<AICopilotModal open mode="rewrite" onClose={() => {}} />);
    await generate('first ask');

    // Reopen in another mode while the first request is still in flight.
    rerender(<AICopilotModal open={false} mode="rewrite" onClose={() => {}} />);
    rerender(<AICopilotModal open mode="section" onClose={() => {}} />);

    await act(async () => {
      release({ blocks: [{ id: 'ai1', type: 'text', text: '<p>stale</p>' }] });
    });

    // The orphaned draft must not appear as an applicable result.
    await waitFor(() => {
      expect(screen.queryByRole('button', { name: /Use this rewrite|Add to email/i })).toBeNull();
    });
    expect(useBuilderStore.getState().blocks[0].text).toBe('<p>original words</p>');
  });

  it('surfaces the server-side repairs so the copilot is never silently wrong', async () => {
    mockDraft.mockResolvedValue({
      blocks: [{ id: 'ai1', type: 'text', text: '<p>hi</p>' }],
      repairs: ['removed the unavailable merge field "deal.value"'],
    });
    render(<AICopilotModal open mode="section" onClose={() => {}} />);
    await generate('a closing CTA');
    expect(await screen.findByText(/removed the unavailable merge field/i)).toBeTruthy();
  });

  it('applies a whole-email draft as one undoable step', async () => {
    mockDraft.mockResolvedValue({
      blocks: [{ id: 'ai1', type: 'heading', text: '<p>Generated</p>', level: 1 }],
      subject: 'Generated subject',
    });
    render(<AICopilotModal open mode="email" onClose={() => {}} />);
    const user = await generate('a welcome email');
    await user.click(await screen.findByRole('button', { name: /Use this email/i }));

    expect(useBuilderStore.getState().blocks).toHaveLength(1);
    expect(useBuilderStore.getState().subject).toBe('Generated subject');
    act(() => { useBuilderStore.getState().undo(); });
    expect(useBuilderStore.getState().blocks[0].id).toBe('b1');
    expect(useBuilderStore.getState().subject).toBe('original subject');
  });
});
