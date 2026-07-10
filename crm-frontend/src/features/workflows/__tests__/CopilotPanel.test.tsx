import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// Mutable mutation handle the mock returns; each test tunes it.
let mockMutation: {
  mutate: ReturnType<typeof vi.fn>;
  isPending: boolean;
  isError: boolean;
  isSuccess: boolean;
  data: unknown;
  error: unknown;
};

vi.mock('../queries', () => ({
  useDraftWorkflow: () => mockMutation,
}));

import { CopilotPanel } from '../builder/config/CopilotPanel';
import { useBuilderStore } from '../store';

beforeEach(() => {
  useBuilderStore.getState().reset();
  useBuilderStore.setState({ name: 'Old', trigger: { type: 'contact_created' }, steps: [], actions: [] });
  mockMutation = {
    mutate: vi.fn((_prompt: string, opts?: { onSuccess?: (r: unknown) => void }) => {
      opts?.onSuccess?.({
        draft: { name: 'Drafted WF', trigger: { type: 'deal_stage_changed', params: {} }, steps: [] },
        validation: { valid: true },
      });
    }),
    isPending: false,
    isError: false,
    isSuccess: false,
    data: undefined,
    error: null,
  };
});

describe('CopilotPanel', () => {
  it('renders the prompt box and generate button', () => {
    render(<CopilotPanel />);
    expect(screen.getByPlaceholderText(/When a deal moves to Won/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /generate draft/i })).toBeInTheDocument();
  });

  it('clicking an example fills the prompt', () => {
    render(<CopilotPanel />);
    const example = screen.getByRole('button', { name: /notify the owner and create a follow-up task/i });
    fireEvent.click(example);
    const textarea = screen.getByPlaceholderText(/When a deal moves to Won/i) as HTMLTextAreaElement;
    expect(textarea.value).toMatch(/moves to Won/i);
  });

  it('generate calls the mutation and applies the returned draft to the store', () => {
    render(<CopilotPanel />);
    const textarea = screen.getByPlaceholderText(/When a deal moves to Won/i);
    fireEvent.change(textarea, { target: { value: 'notify the owner on win' } });
    fireEvent.click(screen.getByRole('button', { name: /generate draft/i }));

    expect(mockMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ prompt: 'notify the owner on win' }),
      expect.any(Object),
    );
    // onSuccess applied the draft into the store.
    const s = useBuilderStore.getState();
    expect(s.name).toBe('Drafted WF');
    expect(s.draftSnapshot).not.toBeNull();
  });

  it('generate is disabled while the prompt is empty', () => {
    render(<CopilotPanel />);
    expect(screen.getByRole('button', { name: /generate draft/i })).toBeDisabled();
  });

  it('shows an error when drafting fails', () => {
    mockMutation.isError = true;
    mockMutation.error = new Error('AI copilot is unavailable');
    render(<CopilotPanel />);
    expect(screen.getByText(/AI copilot is unavailable/i)).toBeInTheDocument();
  });

  it('shows the success banner while a valid draft is pending review', () => {
    // A draft is pending (draftSnapshot set) and validation passed.
    useBuilderStore.getState().applyDraft({ trigger: { type: 'contact_created' }, steps: [] });
    mockMutation.isSuccess = true;
    mockMutation.data = { draft: { name: 'D', trigger: { type: 'contact_created' }, steps: [] }, validation: { valid: true } };
    render(<CopilotPanel />);
    expect(screen.getByText(/Draft applied to the canvas/i)).toBeInTheDocument();
  });

  it('lists validation issues with a "+N more" indicator when the draft has problems', () => {
    useBuilderStore.getState().applyDraft({ trigger: { type: 'contact_created' }, steps: [] });
    mockMutation.isSuccess = true;
    const errors = Array.from({ length: 7 }, (_, i) => ({ field: `f${i}`, message: `problem ${i}` }));
    mockMutation.data = { draft: { name: 'D', trigger: { type: 'contact_created' }, steps: [] }, validation: { valid: false, errors } };
    render(<CopilotPanel />);
    expect(screen.getByText('problem 0')).toBeInTheDocument();
    expect(screen.getByText(/\+1 more/)).toBeInTheDocument();
  });

  it('hides the success banner once no draft is pending (kept/undone)', () => {
    // Mutation still reports success, but draftSnapshot is null (draft committed/reverted).
    mockMutation.isSuccess = true;
    mockMutation.data = { draft: { name: 'D', trigger: { type: 'contact_created' }, steps: [] }, validation: { valid: true } };
    render(<CopilotPanel />);
    expect(screen.queryByText(/Draft applied to the canvas/i)).not.toBeInTheDocument();
  });

  // A7.4: the Command Center hands off a prompt via initialPrompt → auto-draft once.
  it('auto-generates from initialPrompt (Command Center handoff)', () => {
    render(<CopilotPanel initialPrompt="when a deal is won, email the owner" />);
    expect(mockMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ prompt: 'when a deal is won, email the owner' }),
      expect.any(Object),
    );
    const ta = screen.getByPlaceholderText(/When a deal moves to Won/i) as HTMLTextAreaElement;
    expect(ta.value).toBe('when a deal is won, email the owner');
  });

  it('does not auto-generate without an initialPrompt', () => {
    render(<CopilotPanel />);
    expect(mockMutation.mutate).not.toHaveBeenCalled();
  });

  // A7.4: an existing workflow on the canvas is sent as edit context, so the copilot
  // edits rather than replaces.
  it('sends the current workflow as edit context when one exists', () => {
    useBuilderStore.setState({
      trigger: { type: 'deal_stage_changed', params: {} },
      steps: [{ id: 'a1', type: 'action', action: { id: 'a1', type: 'create_task', params: { title: 'x' } } }],
    });
    render(<CopilotPanel />);
    fireEvent.change(screen.getByPlaceholderText(/When a deal moves to Won/i), { target: { value: 'add a delay' } });
    fireEvent.click(screen.getByRole('button', { name: /generate draft/i }));
    expect(mockMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ prompt: 'add a delay', current: expect.objectContaining({ trigger: expect.objectContaining({ type: 'deal_stage_changed' }) }) }),
      expect.any(Object),
    );
  });

  it('sends no edit context for an empty/new workflow', () => {
    useBuilderStore.setState({ trigger: null, steps: [] });
    render(<CopilotPanel initialPrompt="build something new" />);
    expect(mockMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ prompt: 'build something new', current: null }),
      expect.any(Object),
    );
  });
});
