import { describe, it, expect } from 'vitest';
import {
  stepSpecSchema,
  delayParamsSchema,
  actionSpecSchema,
  workflowSchema,
} from '../schemas';

// ── Helpers ──────────────────────────────────────────────────────────

function validAction(id = 'a1') {
  return {
    type: 'action',
    id,
    action: { type: 'send_email', id, params: { to: 'x@y.com' } },
  };
}

function validDelay(id = 'd1', sec = 60) {
  return {
    type: 'delay',
    id,
    delay: { duration_sec: sec },
  };
}

function validCondition(id = 'c1', yes: any[] = [], no: any[] = []) {
  return {
    type: 'condition',
    id,
    condition: { op: 'AND', rules: [{ field: 'f', operator: 'eq', value: 'x' }] },
    yes_steps: yes,
    no_steps: no,
  };
}

// ═════════════════════════════════════════════════════════════════════
// delayParamsSchema
// ═════════════════════════════════════════════════════════════════════
describe('delayParamsSchema', () => {
  it('accepts valid duration', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: 60 }).success).toBe(true);
  });

  it('accepts large duration', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: 2592000 }).success).toBe(true);
  });

  it('rejects zero duration', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: 0 }).success).toBe(false);
  });

  it('rejects negative duration', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: -1 }).success).toBe(false);
  });

  it('rejects non-integer duration', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: 1.5 }).success).toBe(false);
  });

  it('rejects missing duration_sec', () => {
    expect(delayParamsSchema.safeParse({}).success).toBe(false);
  });

  it('rejects string duration', () => {
    expect(delayParamsSchema.safeParse({ duration_sec: '60' }).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// stepSpecSchema — action type
// ═════════════════════════════════════════════════════════════════════
describe('stepSpecSchema — action step', () => {
  it('accepts valid action step', () => {
    const result = stepSpecSchema.safeParse(validAction());
    expect(result.success).toBe(true);
  });

  it('accepts action step with all action types', () => {
    for (const type of ['send_email', 'create_task', 'assign_user', 'send_webhook', 'delay', 'update_record']) {
      const step = {
        type: 'action',
        id: 'a1',
        action: { type, id: 'a1', params: {} },
      };
      expect(stepSpecSchema.safeParse(step).success).toBe(true);
    }
  });

  it('accepts action step without optional fields', () => {
    const step = { type: 'action', id: 'a1' }; // action is optional in schema
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('rejects action step with invalid action type', () => {
    const step = {
      type: 'action',
      id: 'a1',
      action: { type: 'invalid_type', id: 'a1', params: {} },
    };
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });

  it('rejects action step with missing action id', () => {
    const step = {
      type: 'action',
      id: 'a1',
      action: { type: 'send_email', id: '', params: {} },
    };
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// stepSpecSchema — delay type
// ═════════════════════════════════════════════════════════════════════
describe('stepSpecSchema — delay step', () => {
  it('accepts valid delay step', () => {
    expect(stepSpecSchema.safeParse(validDelay()).success).toBe(true);
  });

  it('accepts delay step without delay params (optional)', () => {
    const step = { type: 'delay', id: 'd1' };
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('rejects delay step with invalid duration', () => {
    const step = { type: 'delay', id: 'd1', delay: { duration_sec: -1 } };
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });

  it('rejects delay step with non-integer duration', () => {
    const step = { type: 'delay', id: 'd1', delay: { duration_sec: 1.5 } };
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// stepSpecSchema — condition type
// ═════════════════════════════════════════════════════════════════════
describe('stepSpecSchema — condition step', () => {
  it('accepts valid condition step with yes/no branches', () => {
    const step = validCondition('c1', [validAction('y1')], [validAction('n1')]);
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('accepts condition step with empty branches', () => {
    const step = validCondition('c1');
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('accepts condition step without condition field (optional)', () => {
    const step = { type: 'condition', id: 'c1' };
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('accepts condition with OR group', () => {
    const step = {
      type: 'condition',
      id: 'c1',
      condition: { op: 'OR', rules: [{ field: 'f', operator: 'eq', value: 'x' }] },
    };
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('rejects condition with invalid op', () => {
    const step = {
      type: 'condition',
      id: 'c1',
      condition: { op: 'XOR', rules: [{ field: 'f', operator: 'eq', value: 'x' }] },
    };
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });

  it('rejects condition with empty rules', () => {
    const step = {
      type: 'condition',
      id: 'c1',
      condition: { op: 'AND', rules: [] },
    };
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// stepSpecSchema — recursive (nested conditions)
// ═════════════════════════════════════════════════════════════════════
describe('stepSpecSchema — recursive nesting', () => {
  it('accepts depth 2: condition → yes → action', () => {
    const step = validCondition('outer', [validAction('y1')]);
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('accepts depth 2: condition → yes → condition → yes → action', () => {
    const inner = validCondition('inner', [validAction('deep')]);
    const outer = validCondition('outer', [inner]);
    expect(stepSpecSchema.safeParse(outer).success).toBe(true);
  });

  it('accepts depth 3: triple-nested conditions', () => {
    const l3 = validCondition('l3', [validAction('leaf')]);
    const l2 = validCondition('l2', [l3]);
    const l1 = validCondition('l1', [l2]);
    expect(stepSpecSchema.safeParse(l1).success).toBe(true);
  });

  it('accepts mixed types in branches', () => {
    const step = validCondition('c1', [validAction('a1'), validDelay('d1')], [validAction('n1')]);
    expect(stepSpecSchema.safeParse(step).success).toBe(true);
  });

  it('rejects invalid step nested in yes_steps', () => {
    const invalid = { type: 'bogus', id: 'x' };
    const step = validCondition('c1', [invalid]);
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });

  it('rejects invalid step nested in no_steps', () => {
    const invalid = { type: 'action', id: '' }; // empty id
    const step = validCondition('c1', [], [invalid]);
    expect(stepSpecSchema.safeParse(step).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// stepSpecSchema — common validation
// ═════════════════════════════════════════════════════════════════════
describe('stepSpecSchema — common validation', () => {
  it('rejects missing type', () => {
    expect(stepSpecSchema.safeParse({ id: 'a1' }).success).toBe(false);
  });

  it('rejects invalid type', () => {
    expect(stepSpecSchema.safeParse({ type: 'invalid', id: 'a1' }).success).toBe(false);
  });

  it('rejects missing id', () => {
    expect(stepSpecSchema.safeParse({ type: 'action' }).success).toBe(false);
  });

  it('rejects empty id', () => {
    expect(stepSpecSchema.safeParse({ type: 'action', id: '' }).success).toBe(false);
  });

  it('rejects null', () => {
    expect(stepSpecSchema.safeParse(null).success).toBe(false);
  });

  it('rejects empty object', () => {
    expect(stepSpecSchema.safeParse({}).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// workflowSchema — steps field
// ═════════════════════════════════════════════════════════════════════
describe('workflowSchema — steps field', () => {
  const validBase = {
    name: 'Test',
    trigger: { type: 'contact_created' },
    conditions: null,
    actions: [{ type: 'send_email', id: 'a1', params: { to: 'x@y.com' } }],
  };

  it('accepts workflow without steps (backward compat)', () => {
    expect(workflowSchema.safeParse(validBase).success).toBe(true);
  });

  it('accepts workflow with empty steps array', () => {
    expect(workflowSchema.safeParse({ ...validBase, steps: [] }).success).toBe(true);
  });

  it('accepts workflow with valid steps tree', () => {
    const steps = [
      validAction('a1'),
      validCondition('c1', [validAction('y1')], [validDelay('d1')]),
    ];
    expect(workflowSchema.safeParse({ ...validBase, steps }).success).toBe(true);
  });

  it('rejects workflow with invalid step in steps array', () => {
    const steps = [{ type: 'bogus', id: 'x' }];
    expect(workflowSchema.safeParse({ ...validBase, steps }).success).toBe(false);
  });

  it('rejects workflow with step missing id', () => {
    const steps = [{ type: 'action' }];
    expect(workflowSchema.safeParse({ ...validBase, steps }).success).toBe(false);
  });
});

// ═════════════════════════════════════════════════════════════════════
// Backend round-trip: JSON matching backend StepSpec exactly
// ═════════════════════════════════════════════════════════════════════
describe('stepSpecSchema — backend JSON round-trip', () => {
  it('validates a full backend-shaped payload', () => {
    // Exact JSON shape the Go backend would produce
    const backendJson = {
      type: 'condition',
      id: 'cond-1',
      condition: {
        op: 'AND',
        rules: [
          { field: 'deal.stage', operator: 'eq', value: 'qualified' },
        ],
      },
      yes_steps: [
        {
          type: 'action',
          id: 'act-1',
          action: {
            type: 'send_email',
            id: 'act-1',
            params: { to: '{{contact.email}}', subject: 'Welcome' },
          },
        },
        {
          type: 'delay',
          id: 'delay-1',
          delay: { duration_sec: 3600 },
        },
      ],
      no_steps: [
        {
          type: 'action',
          id: 'act-2',
          action: {
            type: 'create_task',
            id: 'act-2',
            params: { title: 'Follow up' },
          },
        },
      ],
    };
    expect(stepSpecSchema.safeParse(backendJson).success).toBe(true);
  });

  it('validates a nested condition JSON payload', () => {
    const nestedJson = {
      type: 'condition',
      id: 'outer',
      condition: { op: 'OR', rules: [{ field: 'x', operator: 'eq', value: 1 }] },
      yes_steps: [
        {
          type: 'condition',
          id: 'inner',
          condition: { op: 'AND', rules: [{ field: 'y', operator: 'gt', value: 10 }] },
          yes_steps: [
            { type: 'action', id: 'leaf', action: { type: 'send_webhook', id: 'leaf', params: { url: 'https://example.com' } } },
          ],
          no_steps: [],
        },
      ],
      no_steps: [],
    };
    expect(stepSpecSchema.safeParse(nestedJson).success).toBe(true);
  });
});
