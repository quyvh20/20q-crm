import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import type { ActionSpec, TriggerSpec } from '../types';

// A7.1: ai_generate builder validation — prompt required, max_tokens 1..1024.

function seed(action: ActionSpec) {
  const trigger: TriggerSpec = { type: 'contact_created' };
  useBuilderStore.setState({
    name: 'Test WF',
    trigger,
    actions: [action],
    steps: [{ id: action.id, type: 'action', action }],
    conditions: null,
  });
}

beforeEach(() => useBuilderStore.getState().reset());

describe('ai_generate validation', () => {
  it('requires a prompt', () => {
    seed({ id: 'g1', type: 'ai_generate', params: { max_tokens: 512 } });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.prompt']).toBeTruthy();
  });

  it('rejects an out-of-range max_tokens', () => {
    seed({ id: 'g1', type: 'ai_generate', params: { prompt: 'hi', max_tokens: 5000 } });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.max_tokens']).toBeTruthy();
  });

  it('accepts a prompt with a valid max_tokens', () => {
    seed({ id: 'g1', type: 'ai_generate', params: { prompt: 'Summarize {{deal.title}}', max_tokens: 300 } });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });
});
