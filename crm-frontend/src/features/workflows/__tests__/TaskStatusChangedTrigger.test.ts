import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import { resolvableObjectsForTrigger, triggerPrimaryObject } from '../dateField';
import { entityKindForTrigger } from '../RunNowModal';
import { buildTriggerItems, findTriggerItem } from '../builder/catalog';
import { triggerTitle, triggerDescription, triggerLabel, triggerMeta } from '../builder/nodeMeta';
import { TRIGGER_LABELS } from '../types';
import type { WorkflowStep } from '../types';

// task_status_changed (A9.x): a bespoke trigger, like deal_stage_changed, built
// after discovering watch_field has NO frontend UI anywhere in the product — so
// "task_updated + watch_field=status" would have been unreachable through the
// builder. Every assertion here maps 1:1 to a defect an adversarial review found
// in the first cut: extractObjectSlug (store.ts) had no case, so validate()
// unconditionally rejected every task_status_changed workflow with "Select a
// source object" and Save was a silent no-op; hasValidEvent (store.ts) had no
// case either, so even after fixing extractObjectSlug the save would have failed
// a SECOND way with "Select a fires-on event"; nodeMeta.tsx's three label
// functions and its icon switch had no case, so the canvas node showed the raw
// string "task_status_changed" instead of a title, and a generic Zap icon
// instead of the task check-square.

const TRIGGER = { type: 'task_status_changed', params: { to_status: 'completed' } };

function mkAction(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'create_task', params: { title: 't' } } };
}

beforeEach(() => {
  useBuilderStore.getState().reset();
});

describe('task_status_changed — object resolution surfaces', () => {
  it('resolves task + contact + deal + company (mirrors backend buildEvalContext)', () => {
    const set = resolvableObjectsForTrigger(TRIGGER);
    expect(set.has('task')).toBe(true);
    expect(set.has('contact')).toBe(true);
    expect(set.has('deal')).toBe(true);
    expect(set.has('company')).toBe(true);
  });

  it('has task as the primary object', () => {
    expect(triggerPrimaryObject(TRIGGER)).toBe('task');
  });

  it('is unsupported for Run Now (documented behavior, not a regression)', () => {
    expect(entityKindForTrigger('task_status_changed')).toBeNull();
  });
});

describe('task_status_changed — validate()', () => {
  function seed(params: Record<string, unknown>) {
    useBuilderStore.setState({
      name: 'wf',
      trigger: { type: 'task_status_changed', params },
    });
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
  }

  it('saves cleanly with a valid to_status — this is THE regression: extractObjectSlug/hasValidEvent had no case for this type, so validate() unconditionally failed with "Select a source object" (and, once that was patched alone, "Select a fires-on event") no matter how the trigger was configured', () => {
    seed({ to_status: 'completed' });
    const ok = useBuilderStore.getState().validate();
    expect(ok, JSON.stringify(useBuilderStore.getState().errors)).toBe(true);
    expect(useBuilderStore.getState().errors['trigger.object']).toBeUndefined();
    expect(useBuilderStore.getState().errors['trigger.firesOn']).toBeUndefined();
  });

  it('accepts a from_status alongside to_status, including the wildcard', () => {
    seed({ to_status: 'completed', from_status: 'in_progress' });
    expect(useBuilderStore.getState().validate()).toBe(true);

    useBuilderStore.getState().reset();
    seed({ to_status: 'completed', from_status: '*' });
    expect(useBuilderStore.getState().validate()).toBe(true);
  });

  it('rejects an empty to_status — the exact shape the palette entry seeds', () => {
    // catalog.ts's "Task status changed" item builds { to_status: '' }, the shape
    // dragging the trigger onto the canvas without opening the config panel
    // produces. Left unvalidated, this would have reached the backend save.
    seed({ to_status: '' });
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['trigger.params.to_status']).toBeDefined();
  });

  it('rejects a trigger with no params object at all', () => {
    seed({});
    expect(useBuilderStore.getState().validate()).toBe(false);
    expect(useBuilderStore.getState().errors['trigger.params.to_status']).toBeDefined();
  });
});

describe('task_status_changed — catalog + labels', () => {
  it('is a palette trigger item in the Tasks category', () => {
    const item = findTriggerItem('task_status_changed', null);
    expect(item).toBeDefined();
    expect(item!.category).toBe('Tasks');
    expect(item!.build()).toEqual({ type: 'task_status_changed', params: { to_status: '' } });
  });

  it('plain task_created is also offered, in the Tasks category', () => {
    const item = findTriggerItem('task_created', null);
    expect(item).toBeDefined();
    expect(item!.category).toBe('Tasks');

    const ids = buildTriggerItems(null).map((i) => i.id);
    expect(ids).toContain('task_created');
    expect(ids).toContain('task_status_changed');
  });

  it('renders human labels everywhere a raw trigger-type string could otherwise leak through', () => {
    expect(TRIGGER_LABELS.task_status_changed).toBe('Task Status Changed');
    expect(triggerLabel(TRIGGER)).toBe('Task Status Changed');
    expect(triggerTitle(TRIGGER)).toBe('Task status changed');
    expect(triggerDescription(TRIGGER)).toMatch(/any.*completed/);
    expect(triggerDescription({ type: 'task_status_changed', params: { to_status: 'completed', from_status: 'in_progress' } }))
      .toMatch(/in_progress.*completed/);
    // None of the four functions above may ever fall through to `return type`
    // for this trigger — that fallthrough is exactly what shipped the raw
    // "task_status_changed" string onto the canvas node.
    expect(triggerLabel(TRIGGER)).not.toBe('task_status_changed');
    expect(triggerTitle(TRIGGER)).not.toBe('task_status_changed');
    expect(triggerDescription(TRIGGER)).not.toBe('task_status_changed');
  });

  it('gets its own icon, not the generic trigger fallback', () => {
    const meta = triggerMeta('task_status_changed');
    const fallback = triggerMeta('some_unrecognized_type');
    expect(meta.icon).not.toBe(fallback.icon);
  });
});
