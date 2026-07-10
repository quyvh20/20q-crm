import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import type { ActionSpec, TriggerSpec } from '../types';

// A6.3: notify_user builder validation. Seeds the store the way NextBuilder does
// after a load (trigger + a single action step) and asserts validate()'s errors.

function seed(trigger: TriggerSpec, params: Record<string, unknown>) {
  const action: ActionSpec = { id: 'n1', type: 'notify_user', params };
  useBuilderStore.setState({
    name: 'Test WF',
    trigger,
    actions: [action],
    steps: [{ id: 'n1', type: 'action', action }],
    conditions: null,
  });
}

beforeEach(() => useBuilderStore.getState().reset());

describe('notify_user validation', () => {
  it('requires a title', () => {
    seed({ type: 'contact_created' }, { recipient: 'owner_field' });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.title']).toBeTruthy();
  });

  it('accepts record-owner mode on a contact trigger', () => {
    seed({ type: 'contact_created' }, { recipient: 'owner_field', owner_field: 'contact.owner_user_id', title: 'Hi' });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });

  it('accepts record-owner mode on a deal_stage_changed trigger', () => {
    seed({ type: 'deal_stage_changed', params: { to_stage: 's1' } }, { recipient: 'owner_field', owner_field: 'deal.owner_user_id', title: 'Moved' });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });

  it('rejects record-owner mode on a schedule trigger (no record owner)', () => {
    seed({ type: 'schedule', params: { cron: '0 9 * * 1' } }, { recipient: 'owner_field', title: 'Hi' });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.recipient']).toBeTruthy();
  });

  it('requires a user for a specific recipient', () => {
    seed({ type: 'contact_created' }, { recipient: 'specific', title: 'Hi' });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.user_id']).toBeTruthy();
  });

  it('accepts a specific recipient with a user (even on a schedule trigger)', () => {
    seed({ type: 'schedule', params: { cron: '0 9 * * 1' } }, { recipient: 'specific', user_id: 'u-123', title: 'Hi' });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });
});
