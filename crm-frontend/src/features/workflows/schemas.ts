import { z } from 'zod';

// ============================================================
// Zod schemas mirroring backend §3 exactly
// ============================================================

const groupOps = ['AND', 'OR'] as const;
const actionTypes = ['send_email', 'create_task', 'assign_user', 'send_webhook', 'delay', 'update_record', 'log_activity', 'notify_user', 'create_record', 'find_records', 'enroll_records', 'ai_generate'] as const;

export const triggerSpecSchema = z.object({
  type: z.string().min(1, 'Trigger type is required'),
  params: z.record(z.string(), z.unknown()).optional(),
});

const conditionRuleSchema: z.ZodType<any> = z.lazy(() =>
  z.union([
    // Leaf rule
    z.object({
      field: z.string().min(1, 'Field is required'),
      operator: z.string().min(1, 'Operator is required'),
      value: z.unknown(),
    }),
    // Nested group
    z.object({
      op: z.enum(groupOps),
      rules: z.array(conditionRuleSchema).min(1, 'Add at least one condition rule'),
    }),
  ])
);

export const conditionGroupSchema = z.object({
  op: z.enum(groupOps),
  rules: z.array(conditionRuleSchema).min(1, 'At least one rule is required'),
}).nullable().optional();

export const actionSpecSchema = z.object({
  type: z.enum(actionTypes),
  id: z.string().min(1, 'Action ID is required'),
  params: z.record(z.string(), z.unknown()),
});

// A delay is either a fixed duration (duration_sec > 0) or a wait-until (A4.4:
// until_field set → resolve the deadline from a record date field; duration_sec is
// ignored and may be 0). Mirrors the backend DelayParams.IsWaitUntil discriminator
// (validateDelayParams) so the canonical steps path accepts both shapes — otherwise
// a wait-until delay (duration_sec 0) would fail .positive() and block every save.
export const delayParamsSchema = z
  .object({
    duration_sec: z.number().int().nonnegative().optional(),
    until_field: z.string().optional(),
    offset_days: z.number().int().optional(),
    at_time: z.string().optional(),
    timezone: z.string().optional(),
  })
  .refine(
    (d) => (!!d.until_field && d.until_field.length > 0) || (typeof d.duration_sec === 'number' && d.duration_sec > 0),
    { message: 'Delay duration must be positive', path: ['duration_sec'] },
  );

const stepTypes = ['action', 'condition', 'delay', 'split'] as const;

// Percentage split (A/B fork): percent_a of runs take the A branch (yes_steps).
export const splitParamsSchema = z.object({
  percent_a: z
    .number()
    .int()
    .min(1, 'Split percentage must be between 1 and 99')
    .max(99, 'Split percentage must be between 1 and 99'),
});

/**
 * Recursive StepSpec schema mirroring backend StepSpec exactly:
 *   type: "action" | "condition" | "delay" | "split"
 *   id: string
 *   action?: ActionSpec      (when type = "action")
 *   condition?: ConditionGroup (when type = "condition")
 *   delay?: DelayParams      (when type = "delay")
 *   split?: SplitParams      (when type = "split")
 *   yes_steps?: StepSpec[]   (fork branches: condition Yes/No, split A/B)
 *   no_steps?: StepSpec[]
 */
export const stepSpecSchema: z.ZodType<any> = z.lazy(() =>
  z.object({
    type: z.enum(stepTypes),
    id: z.string().min(1, 'Step ID is required'),
    action: actionSpecSchema.optional(),
    condition: z.object({
      op: z.enum(groupOps),
      rules: z.array(conditionRuleSchema).min(1, 'Add at least one condition rule'),
    }).optional(),
    delay: delayParamsSchema.optional(),
    split: splitParamsSchema.optional(),
    yes_steps: z.array(stepSpecSchema).optional(),
    no_steps: z.array(stepSpecSchema).optional(),
  })
);

export const workflowSchema = z.object({
  name: z.string().min(1, 'Name is required').max(200),
  description: z.string().max(1000).optional().default(''),
  trigger: triggerSpecSchema,
  conditions: conditionGroupSchema,
  actions: z.array(actionSpecSchema).min(1, 'At least one action is required'),
  steps: z.array(stepSpecSchema).optional(),
});

// Validate no duplicate action IDs
export function validateActionIds(actions: z.infer<typeof actionSpecSchema>[]): string[] {
  const errors: string[] = [];
  const seen = new Set<string>();
  for (const action of actions) {
    if (seen.has(action.id)) {
      errors.push(`Duplicate action ID: ${action.id}`);
    }
    seen.add(action.id);
  }
  return errors;
}

// Validate condition depth ≤ 3
export function validateConditionDepth(group: any, depth = 1): string[] {
  if (!group || !group.op) return [];
  if (depth > 3) return ['Condition nesting exceeds maximum depth of 3'];
  const errors: string[] = [];
  if (group.rules) {
    for (const rule of group.rules) {
      if (rule.op) {
        errors.push(...validateConditionDepth(rule, depth + 1));
      }
    }
  }
  return errors;
}

export type WorkflowFormData = z.infer<typeof workflowSchema>;
