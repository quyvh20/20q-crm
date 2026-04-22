import { z } from 'zod';

// ============================================================
// Zod schemas mirroring backend §3 exactly
// ============================================================

export const triggerSpecSchema = z.object({
  type: z.enum(['contact_created', 'contact_updated', 'deal_stage_changed', 'no_activity_days', 'webhook_inbound']),
  params: z.record(z.unknown()).optional(),
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
      op: z.enum(['AND', 'OR']),
      rules: z.array(conditionRuleSchema).min(1),
    }),
  ])
);

export const conditionGroupSchema = z.object({
  op: z.enum(['AND', 'OR']),
  rules: z.array(conditionRuleSchema).min(1, 'At least one rule is required'),
}).nullable().optional();

export const actionSpecSchema = z.object({
  type: z.enum(['send_email', 'create_task', 'assign_user', 'send_webhook', 'delay']),
  id: z.string().min(1, 'Action ID is required'),
  params: z.record(z.unknown()),
});

export const workflowSchema = z.object({
  name: z.string().min(1, 'Name is required').max(200),
  description: z.string().max(1000).optional().default(''),
  trigger: triggerSpecSchema,
  conditions: conditionGroupSchema,
  actions: z.array(actionSpecSchema).min(1, 'At least one action is required'),
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
