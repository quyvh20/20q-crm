import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import type { ActionSpec, TriggerSpec } from '../types';

// A6.4: create_record builder validation — object required + at least one named field.

function seed(trigger: TriggerSpec, params: Record<string, unknown>) {
  const action: ActionSpec = { id: 'c1', type: 'create_record', params };
  useBuilderStore.setState({
    name: 'Test WF',
    trigger,
    actions: [action],
    steps: [{ id: 'c1', type: 'action', action }],
    conditions: null,
  });
}

beforeEach(() => useBuilderStore.getState().reset());

describe('create_record validation', () => {
  it('requires an object', () => {
    seed({ type: 'contact_created' }, { fields: [{ field: 'company.name', value: 'x' }] });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.object']).toBeTruthy();
  });

  it('requires at least one field', () => {
    seed({ type: 'contact_created' }, { object: 'company', fields: [] });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.fields']).toBeTruthy();
  });

  it('rejects field rows with no target field', () => {
    seed({ type: 'contact_created' }, { object: 'company', fields: [{ field: '', value: 'x' }] });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['actions.0.params.fields']).toBeTruthy();
  });

  it('accepts an object with a named field', () => {
    seed({ type: 'contact_created' }, { object: 'company', fields: [{ field: 'company.name', value: '{{contact.last_name}}' }] });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });
});
