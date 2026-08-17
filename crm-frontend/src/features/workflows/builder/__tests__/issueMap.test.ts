import { describe, it, expect } from 'vitest';
import { mapIssuesToCanvas } from '../issueMap';
import type { WorkflowStep, ActionSpec } from '../../types';

function mkAction(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'send_email', params: {} } };
}

function mkCondition(id: string, yes: WorkflowStep[] = [], no: WorkflowStep[] = []): WorkflowStep {
  return { id, type: 'condition', condition: { op: 'AND', rules: [] }, yes_steps: yes, no_steps: no };
}

const STEPS: WorkflowStep[] = [
  mkAction('a1'),
  mkCondition('c1', [mkAction('y1')], [mkAction('n1')]),
];
// flattenSteps order: a1, y1, n1 — what state.actions holds.
const ACTIONS = [
  { id: 'a1', type: 'send_email', params: {} },
  { id: 'y1', type: 'send_email', params: {} },
  { id: 'n1', type: 'send_email', params: {} },
] as ActionSpec[];

describe('mapIssuesToCanvas', () => {
  it('returns an empty result for no errors', () => {
    const r = mapIssuesToCanvas({}, STEPS, ACTIONS);
    expect(r.count).toBe(0);
    expect(r.byStep).toEqual({});
  });

  it('maps step.<id> keys directly', () => {
    const r = mapIssuesToCanvas({ 'step.c1': ['Configure at least one condition rule with a field'] }, STEPS, ACTIONS);
    expect(r.byStep.c1).toHaveLength(1);
    expect(r.count).toBe(1);
  });

  it('maps flat actions.<i> keys through the flattened action list', () => {
    const r = mapIssuesToCanvas(
      {
        'actions.0.params.to': ['Must be a valid email address or {{template}}'],
        'actions.2.params.title': ['Title is required'],
      },
      STEPS,
      ACTIONS,
    );
    expect(r.byStep.a1).toEqual(['Must be a valid email address or {{template}}']);
    expect(r.byStep.n1).toEqual(['Title is required']);
  });

  it('resolves zod steps paths through condition branches', () => {
    const r = mapIssuesToCanvas(
      {
        'steps.0.action.params.subject': ['Subject required'],
        'steps.1.yes_steps.0.action.params.to': ['Recipient required'],
        'steps.1.no_steps.0.action.id': ['Action ID is required'],
      },
      STEPS,
      ACTIONS,
    );
    expect(r.byStep.a1).toEqual(['Subject required']);
    expect(r.byStep.y1).toEqual(['Recipient required']);
    expect(r.byStep.n1).toEqual(['Action ID is required']);
  });

  it('routes trigger keys to the trigger bucket and the rest to global', () => {
    const r = mapIssuesToCanvas(
      {
        trigger: ['Target stage is required'],
        'trigger.params.to_stage': ['Select a target stage'],
        name: ['Name is required'],
        steps: ['At least one action or condition is required'],
      },
      STEPS,
      ACTIONS,
    );
    expect(r.trigger).toContain('Target stage is required');
    expect(r.trigger).toContain('Select a target stage');
    expect(r.global).toEqual(['Name is required', 'At least one action or condition is required']);
    expect(r.count).toBe(4);
  });

  it('dedupes repeated messages for the same step', () => {
    const r = mapIssuesToCanvas(
      {
        'actions.0.params.to': ['Recipient required'],
        'steps.0.action.params.to': ['Recipient required'],
      },
      STEPS,
      ACTIONS,
    );
    expect(r.byStep.a1).toEqual(['Recipient required']);
    expect(r.count).toBe(1);
  });

  it('sends unresolvable paths to global instead of dropping them', () => {
    const r = mapIssuesToCanvas({ 'steps.9.action.params.to': ['Ghost issue'] }, STEPS, ACTIONS);
    expect(r.byStep).toEqual({});
    expect(r.global).toEqual(['Ghost issue']);
  });
});
