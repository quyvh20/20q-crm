import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore, normalizeMergesInList } from '../store';
import { delayParamsSchema } from '../schemas';
import { delayLabel, describeWaitEvent } from '../builder/nodeMeta';
import { waitStepsBefore, waitOutcomeEntity } from '../builder/waitOutcomes';
import type { WorkflowStep } from '../types';

// WaitForEvent.test.ts covers the third Wait mode (A9): park until the run's
// contact opens or clicks a campaign email, or until the timeout — whichever
// lands first. Its load-bearing property is MODE EXCLUSIVITY: a key left behind
// by a mode switch must never change which kind of wait actually runs.

function waitStep(id: string, event: 'email_opened' | 'email_clicked', timeoutSec = 259200, campaignID?: string): WorkflowStep {
  return { id, type: 'delay', delay: { duration_sec: 0, wait_event: event, timeout_sec: timeoutSec, campaign_id: campaignID } };
}

function fixedDelay(id: string, sec: number): WorkflowStep {
  return { id, type: 'delay', delay: { duration_sec: sec } };
}

function condition(id: string, yes: WorkflowStep[] = [], no: WorkflowStep[] = []): WorkflowStep {
  return { id, type: 'condition', condition: { op: 'AND', rules: [] }, yes_steps: yes, no_steps: no };
}

function action(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'create_task', params: { title: 't' } } };
}

beforeEach(() => {
  useBuilderStore.getState().reset();
});

// ═══ what travels to the backend ════════════════════════════════════════════

describe('flattenSteps — mode exclusivity', () => {
  it('emits the event, its timeout and its campaign pin', () => {
    useBuilderStore.getState().addStep(waitStep('w1', 'email_clicked', 3600, 'camp-1'), null, null);

    const params = useBuilderStore.getState().actions[0].params;
    expect(params).toMatchObject({ wait_event: 'email_clicked', timeout_sec: 3600, campaign_id: 'camp-1' });
  });

  it('does not carry an until_field left behind by a mode switch', () => {
    // The backend's precedence is IsWaitEvent → IsWaitUntil → fixed. If a stale
    // until_field travelled alongside wait_event the two layers would still
    // agree today — but the panel reads back from these flattened params, so it
    // would render a date wait for a step that runs as an event wait.
    const stale: WorkflowStep = {
      id: 'w1',
      type: 'delay',
      delay: { duration_sec: 0, wait_event: 'email_opened', timeout_sec: 600, until_field: 'deal.expected_close_at' },
    };
    useBuilderStore.getState().addStep(stale, null, null);

    const params = useBuilderStore.getState().actions[0].params;
    expect(params.wait_event).toBe('email_opened');
    expect(params).not.toHaveProperty('until_field');
  });

  it('leaves the other two modes emitting exactly what they did before', () => {
    useBuilderStore.getState().addStep(fixedDelay('d1', 900), null, null);
    useBuilderStore.getState().addStep(
      { id: 'd2', type: 'delay', delay: { duration_sec: 0, until_field: 'deal.expected_close_at', offset_days: -1 } },
      null,
      null,
    );

    const [fixed, until] = useBuilderStore.getState().actions;
    expect(fixed.params).toEqual({ duration_sec: 900 });
    expect(until.params).not.toHaveProperty('wait_event');
    expect(until.params.until_field).toBe('deal.expected_close_at');
  });
});

// ═══ switching modes in the panel ═══════════════════════════════════════════

describe('updateStep — mode switching', () => {
  it('drops out of event mode when wait_event is cleared', () => {
    useBuilderStore.getState().addStep(waitStep('w1', 'email_opened'), null, null);
    useBuilderStore.getState().updateAction('w1', { params: { wait_event: '', until_field: '', duration_sec: 60 } });

    const step = useBuilderStore.getState().findStep('w1')!;
    expect(step.delay!.wait_event).toBeUndefined();
    expect(useBuilderStore.getState().actions[0].params).toEqual({ duration_sec: 60 });
  });

  it('keeps the timeout across a round trip so toggling back restores it', () => {
    useBuilderStore.getState().addStep(waitStep('w1', 'email_clicked', 7200), null, null);
    useBuilderStore.getState().updateAction('w1', { params: { wait_event: '', duration_sec: 60 } });
    useBuilderStore.getState().updateAction('w1', { params: { wait_event: 'email_clicked', duration_sec: 0 } });

    expect(useBuilderStore.getState().actions[0].params.timeout_sec).toBe(7200);
  });

  it('an event wait wins over an until_field set in the same patch', () => {
    useBuilderStore.getState().addStep(fixedDelay('w1', 60), null, null);
    useBuilderStore.getState().updateAction('w1', {
      params: { wait_event: 'email_opened', timeout_sec: 3600, until_field: 'deal.expected_close_at', duration_sec: 0 },
    });

    expect(useBuilderStore.getState().actions[0].params.wait_event).toBe('email_opened');
    expect(useBuilderStore.getState().actions[0].params).not.toHaveProperty('until_field');
  });
});

// ═══ validation ═════════════════════════════════════════════════════════════

function validateWith(trigger: string, step: WorkflowStep): Record<string, string[]> {
  const s = useBuilderStore.getState();
  s.setTrigger({ type: trigger, params: {} });
  s.addStep(step, null, null);
  useBuilderStore.getState().validate();
  return useBuilderStore.getState().errors;
}

describe('validate — wait for event', () => {
  it('accepts a well-formed wait on a contact-bearing trigger', () => {
    const errors = validateWith('contact_created', waitStep('w1', 'email_opened'));
    expect(Object.keys(errors).filter((k) => k.startsWith('actions.0'))).toEqual([]);
  });

  it('accepts it on a deal trigger — the run hydrates the deal’s contact', () => {
    const errors = validateWith('deal_stage_changed', waitStep('w1', 'email_clicked'));
    expect(errors['actions.0.params.wait_event']).toBeUndefined();
  });

  it('rejects it on a trigger with no contact to watch', () => {
    // A schedule run has no record at all, so registerEventWait would fail and
    // the step would silently degrade to a plain timeout.
    const errors = validateWith('schedule', waitStep('w1', 'email_opened'));
    expect(errors['actions.0.params.wait_event']?.[0]).toMatch(/no contact/i);
  });

  it('requires a timeout — it is the only thing that guarantees the run continues', () => {
    const errors = validateWith('contact_created', waitStep('w1', 'email_opened', 0));
    expect(errors['actions.0.params.timeout_sec']).toBeDefined();
  });

  it('caps the timeout at 30 days', () => {
    const errors = validateWith('contact_created', waitStep('w1', 'email_opened', 2592001));
    expect(errors['actions.0.params.timeout_sec']?.[0]).toMatch(/30 days/);
  });

  it('rejects an event it cannot wait for', () => {
    const bad: WorkflowStep = {
      id: 'w1',
      type: 'delay',
      delay: { duration_sec: 0, wait_event: 'contact_created' as never, timeout_sec: 60 },
    };
    const errors = validateWith('contact_created', bad);
    expect(errors['actions.0.params.wait_event']).toBeDefined();
  });

  it('does not apply the duration rules to an event wait', () => {
    // duration_sec is 0 on an event wait; the fixed-delay branch would reject it.
    const errors = validateWith('contact_created', waitStep('w1', 'email_opened'));
    expect(errors['actions.0.params.duration_sec']).toBeUndefined();
  });
});

describe('delayParamsSchema', () => {
  it('accepts an event wait whose duration_sec is 0', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: 0, wait_event: 'email_opened', timeout_sec: 60 }).success).toBe(true);
  });

  it('rejects an event wait with no timeout', () => {
    const res = delayParamsSchema.safeParse({ duration_sec: 0, wait_event: 'email_opened' });
    expect(res.success).toBe(false);
  });

  it('still accepts the two older modes', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: 60 }).success).toBe(true);
    expect(delayParamsSchema.safeParse({ duration_sec: 0, until_field: 'deal.expected_close_at' }).success).toBe(true);
    expect(delayParamsSchema.safeParse({ duration_sec: 0 }).success).toBe(false);
  });
});

// ═══ the node label ═════════════════════════════════════════════════════════

describe('delayLabel', () => {
  it('names the event and the cap', () => {
    expect(describeWaitEvent({ duration_sec: 0, wait_event: 'email_clicked', timeout_sec: 259200 })).toBe(
      'Wait for a link click, up to 3d',
    );
    expect(delayLabel({ duration_sec: 0, wait_event: 'email_opened', timeout_sec: 3600 })).toBe(
      'Wait for an email open, up to 1h',
    );
  });

  it('event mode beats a stale until_field', () => {
    const label = delayLabel({ duration_sec: 0, wait_event: 'email_opened', timeout_sec: 60, until_field: 'deal.expected_close_at' });
    expect(label).toMatch(/^Wait for an email open/);
  });

  it('leaves the other modes alone', () => {
    expect(delayLabel({ duration_sec: 3600 })).toBe('Wait 1h');
  });
});

// ═══ the outcomes an If/Else can branch on ══════════════════════════════════

describe('waitStepsBefore', () => {
  it('offers a wait that precedes the target in the same list', () => {
    const steps = [waitStep('w1', 'email_opened'), condition('c1')];
    expect(waitStepsBefore(steps, 'c1').map((s) => s.id)).toEqual(['w1']);
  });

  it('does not offer a wait that comes after the target', () => {
    const steps = [condition('c1'), waitStep('w1', 'email_opened')];
    expect(waitStepsBefore(steps, 'c1')).toEqual([]);
  });

  it('carries waits from above a fork into the branch below it', () => {
    const steps = [waitStep('w1', 'email_clicked'), condition('c1', [action('a1'), condition('c2')], [])];
    expect(waitStepsBefore(steps, 'c2').map((s) => s.id)).toEqual(['w1']);
  });

  it('does not leak a wait from the sibling branch', () => {
    // A wait in the A branch has no log when a condition in the B branch runs —
    // the rule would fail closed and read as a broken condition.
    const steps = [condition('c1', [waitStep('w1', 'email_opened')], [condition('c2')])];
    expect(waitStepsBefore(steps, 'c2')).toEqual([]);
  });

  it('ignores the other two delay modes', () => {
    const steps = [fixedDelay('d1', 60), condition('c1')];
    expect(waitStepsBefore(steps, 'd1')).toEqual([]);
    expect(waitStepsBefore(steps, 'c1')).toEqual([]);
  });

  it('yields nothing for an id that is not in the tree', () => {
    // The workflow-level conditions panel passes the sentinel id 'conditions';
    // those conditions run before any step, so no wait has an outcome yet.
    const steps = [waitStep('w1', 'email_opened'), waitStep('w2', 'email_clicked')];
    expect(waitStepsBefore(steps, 'conditions')).toEqual([]);
  });
});

describe('waitOutcomeEntity', () => {
  it('builds one boolean field per reachable wait, on the actions path', () => {
    const steps = [waitStep('w1', 'email_opened'), waitStep('w2', 'email_clicked'), condition('c1')];
    const entity = waitOutcomeEntity(steps, 'c1')!;

    expect(entity.label).toBe('Wait outcomes');
    expect(entity.fields.map((f) => f.path)).toEqual(['actions.w1.happened', 'actions.w2.happened']);
    expect(entity.fields.every((f) => f.type === 'boolean')).toBe(true);
    expect(entity.fields.map((f) => f.label)).toEqual(['Opened', 'Clicked (step 2)']);
  });

  it('is null when there is nothing to offer', () => {
    expect(waitOutcomeEntity([condition('c1')], 'c1')).toBeNull();
    expect(waitOutcomeEntity([waitStep('w1', 'email_opened')], null)).toBeNull();
  });
});

// ═══ the outcome reference must survive a merge auto-split ══════════════════

describe('normalizeMergesInList — wait-outcome references', () => {
  it('rewrites a BARE actions.<id> condition field when the step is cloned', () => {
    // A condition rule's `field` is a bare path, not a {{template}} — the only
    // reference form the clone's remapper used to handle. Left unrewritten, the
    // copied condition points at a step id that no longer exists, resolves to
    // nil, fails closed, and every run silently takes the No branch.
    const post: WorkflowStep[] = [
      waitStep('w1', 'email_opened'),
      { id: 'c2', type: 'condition', condition: { op: 'AND', rules: [{ field: 'actions.w1.happened', operator: 'is_true', value: '' }] }, yes_steps: [], no_steps: [] },
    ];
    const { steps, changed } = normalizeMergesInList([condition('c1'), ...post]);
    expect(changed).toBe(true);

    for (const branch of [steps[0].yes_steps!, steps[0].no_steps!]) {
      const clonedWaitId = branch[0].id;
      const rule = branch[1].condition!.rules[0];
      expect(clonedWaitId).not.toBe('w1');
      expect(rule.field).toBe(`actions.${clonedWaitId}.happened`);
    }
  });

  it('still rewrites the {{template}} form', () => {
    const post: WorkflowStep[] = [
      waitStep('w1', 'email_opened'),
      { id: 'a1', type: 'action', action: { id: 'a1', type: 'create_task', params: { title: 'Opened: {{actions.w1.happened}}' } } },
    ];
    const { steps } = normalizeMergesInList([condition('c1'), ...post]);
    const branch = steps[0].yes_steps!;
    expect(branch[1].action!.params.title).toBe(`Opened: {{actions.${branch[0].id}.happened}}`);
  });

  it('leaves an unrelated string alone', () => {
    const post: WorkflowStep[] = [
      waitStep('w1', 'email_opened'),
      { id: 'a1', type: 'action', action: { id: 'a1', type: 'create_task', params: { title: 'actions.someone_else.happened' } } },
    ];
    const { steps } = normalizeMergesInList([condition('c1'), ...post]);
    expect(steps[0].yes_steps![1].action!.params.title).toBe('actions.someone_else.happened');
  });
});
