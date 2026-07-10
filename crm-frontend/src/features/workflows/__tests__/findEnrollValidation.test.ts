import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import type { ActionSpec, TriggerSpec } from '../types';

// A6.5: find_records / enroll_records builder validation.

function seed(trigger: TriggerSpec, action: ActionSpec) {
  useBuilderStore.setState({
    name: 'Test WF',
    trigger,
    actions: [action],
    steps: [{ id: action.id, type: 'action', action }],
    conditions: null,
  });
}

beforeEach(() => useBuilderStore.getState().reset());

const contactTrigger: TriggerSpec = { type: 'contact_created' };

describe('find_records validation', () => {
  it('requires an object', () => {
    seed(contactTrigger, { id: 'f1', type: 'find_records', params: { filters: [] } });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.object']).toBeTruthy();
  });

  it('accepts an object (filters optional)', () => {
    seed(contactTrigger, { id: 'f1', type: 'find_records', params: { object: 'deal', filters: [], limit: 50 } });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });
});

describe('enroll_records validation', () => {
  it('requires object and workflow_id', () => {
    seed(contactTrigger, { id: 'e1', type: 'enroll_records', params: {} });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.object']).toBeTruthy();
    expect(useBuilderStore.getState().errors['actions.0.params.workflow_id']).toBeTruthy();
  });

  it('accepts object + workflow_id', () => {
    seed(contactTrigger, { id: 'e1', type: 'enroll_records', params: { object: 'contact', workflow_id: 'wf-123' } });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });
});
