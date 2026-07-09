import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../../store';
import { buildStep } from '../InsertMenu';
import { stepsToGraph } from '../graph';
import type { TriggerSpec } from '../../types';

const trigger: TriggerSpec = { type: 'contact_created', params: {} };

// These exercise the A3 insert flow against the real store tree ops: an insert
// slot from stepsToGraph, fed to store.addStep, must land the step exactly where
// the "+" was, and the regenerated graph must reflect it.
describe('builder editing', () => {
  beforeEach(() => {
    useBuilderStore.getState().reset();
    useBuilderStore.setState({ trigger, steps: [] });
  });

  it('buildStep produces valid default steps', () => {
    const email = buildStep('send_email');
    expect(email.type).toBe('action');
    expect(email.action?.type).toBe('send_email');
    expect(email.id).toBe(email.action?.id);

    const cond = buildStep('condition');
    expect(cond.type).toBe('condition');
    expect(cond.condition).toEqual({ op: 'AND', rules: [] });
    expect(cond.yes_steps).toEqual([]);
    expect(cond.no_steps).toEqual([]);

    const wait = buildStep('delay');
    expect(wait.type).toBe('delay');
    expect(wait.delay?.duration_sec).toBeGreaterThan(0);
  });

  it('inserts a step at the top-level slot from an empty workflow', () => {
    const store = useBuilderStore.getState();
    // Empty workflow → the end node's edge carries the first insert slot.
    const { nodes, edges } = stepsToGraph(trigger, store.steps);
    const endNode = nodes.find((n) => n.data.kind === 'end')!;
    const slot = edges.find((e) => e.target === endNode.id)!.data!.insert;

    const step = buildStep('send_email');
    store.addStep(step, slot.parentId, slot.branch, slot.index);

    expect(useBuilderStore.getState().steps).toHaveLength(1);
    expect(useBuilderStore.getState().steps[0].id).toBe(step.id);
  });

  it('inserts into a condition branch at the slot the "+" reported', () => {
    const store = useBuilderStore.getState();
    const cond = buildStep('condition');
    store.addStep(cond, null, null, 0);

    // The empty Yes branch exposes an insert slot {parentId: cond, branch: yes, index: 0}.
    const { nodes } = stepsToGraph(trigger, useBuilderStore.getState().steps);
    const endYes = nodes.find(
      (n) => n.data.kind === 'end' && n.data.insert?.parentId === cond.id && n.data.insert?.branch === 'yes',
    );
    expect(endYes).toBeDefined();
    const slot = endYes!.data.insert!;

    const task = buildStep('create_task');
    store.addStep(task, slot.parentId, slot.branch, slot.index);

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.yes_steps).toHaveLength(1);
    expect(updated.yes_steps![0].id).toBe(task.id);
    expect(updated.no_steps).toHaveLength(0);
  });

  it('removes a step by id', () => {
    const store = useBuilderStore.getState();
    const a = buildStep('send_email');
    const b = buildStep('create_task');
    store.addStep(a, null, null, 0);
    store.addStep(b, null, null, 1);
    expect(useBuilderStore.getState().steps).toHaveLength(2);

    store.removeStep(a.id);
    const steps = useBuilderStore.getState().steps;
    expect(steps).toHaveLength(1);
    expect(steps[0].id).toBe(b.id);
  });
});
