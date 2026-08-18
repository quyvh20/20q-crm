// ============================================================
// Workflow Automation Types
// ============================================================

export interface TriggerSpec {
  type: string; // e.g. 'contact_created', 'subscription_updated', 'deal_stage_changed'
  params?: Record<string, unknown>;
}

export interface ConditionRule {
  field?: string;
  operator?: string;
  value?: unknown;
  op?: 'AND' | 'OR';
  rules?: ConditionRule[];
}

export interface ConditionGroup {
  op: 'AND' | 'OR';
  rules: ConditionRule[];
}

export interface ActionSpec {
  type: 'send_email' | 'create_task' | 'assign_user' | 'send_webhook' | 'delay' | 'update_record' | 'log_activity' | 'notify_user' | 'create_record' | 'find_records' | 'enroll_records' | 'ai_generate';
  id: string;
  params: Record<string, unknown>;
}

export interface DelayParams {
  duration_sec: number;
  /** Wait-until mode (A4.4): resolve the deadline from a record date field
   *  (dotted path, e.g. "deal.expected_close_at") instead of a fixed duration.
   *  When set, duration_sec is ignored and the 30-day cap does not apply. */
  until_field?: string;
  offset_days?: number;
  at_time?: string;
  timezone?: string;
  /** Wait-for-event mode (A9): park until the run's contact opens or clicks a
   *  campaign email, or until timeout_sec elapses — whichever lands first. The
   *  step ALWAYS completes; it publishes {{actions.<step id>.happened}} so a
   *  following If/Else can branch on which of the two ended the wait.
   *  Takes precedence over until_field, which takes precedence over duration_sec. */
  wait_event?: WaitEventType;
  /** Hard deadline for the wait, in seconds. Required — it is the only thing
   *  that guarantees the run continues if the event never arrives. */
  timeout_sec?: number;
  /** Optional: only this campaign's opens/clicks satisfy the wait. Blank = any. */
  campaign_id?: string;
}

/** The engagement events a Wait step can wait for — the same two the marketing
 *  webhook emits as triggers, so the wait inherits their filtering (campaign
 *  sends only, opt-out clicks and machine opens excluded). */
export type WaitEventType = 'email_opened' | 'email_clicked';
export const WAIT_EVENT_TYPES: readonly WaitEventType[] = ['email_opened', 'email_clicked'];

/** Percentage split (A/B fork): percent_a of runs take the A branch. */
export interface SplitParams {
  percent_a: number; // 1..99
}

export interface WorkflowStep {
  id: string;
  type: 'action' | 'condition' | 'delay' | 'split';
  action?: ActionSpec;
  condition?: ConditionGroup;
  delay?: DelayParams;
  split?: SplitParams;
  yes_steps?: WorkflowStep[];
  no_steps?: WorkflowStep[];
}

/**
 * Fork kinds (condition, split) share the yes_steps/no_steps branch arrays and
 * the no-merge invariant (a fork is always terminal in its sibling list). Every
 * tree walk that used to gate on type === 'condition' gates on this instead —
 * for a split, yes_steps is the A branch and no_steps is B.
 */
export function isForkStep(step: WorkflowStep): boolean {
  return step.type === 'condition' || step.type === 'split';
}

/** Canonical create/update payload (A1: steps-only; server derives flat actions). */
export interface SaveWorkflowPayload {
  name: string;
  description: string;
  trigger: TriggerSpec;
  conditions: ConditionGroup | null;
  steps: WorkflowStep[];
}

export interface Workflow {
  id: string;
  org_id: string;
  name: string;
  description: string;
  is_active: boolean;
  trigger: TriggerSpec;
  conditions: ConditionGroup | null;
  /** Canonical step tree. The deprecated flat `actions` mirror was removed from the
   *  wire in R5 deploy 1 — derive any flat view locally (see store's flattenSteps). */
  steps?: WorkflowStep[];
  /** Server-derived count of executable steps (actions + delays, branches included). */
  action_count: number;
  version: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  last_run_status: string | null;
  last_run_at: string | null;
}

export interface WorkflowRun {
  id: string;
  workflow_id: string;
  workflow_version: number;
  org_id: string;
  status: 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'skipped';
  trigger_context: Record<string, unknown>;
  current_action_idx: number;
  completed_actions: (number | string)[] | null;
  last_error?: string;
  retry_count: number;
  /** Absolute resume time while status is 'waiting' (parked on a delay step). */
  wake_at?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
}

export interface ActionLog {
  id: string;
  run_id: string;
  action_idx: number;
  action_path?: string;
  action_type: string;
  status: 'success' | 'failed' | 'retrying' | 'running' | 'waiting';
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  error?: string;
  attempt_no: number;
  duration_ms: number;
  created_at: string;
}

export interface WorkflowListResponse {
  workflows: Workflow[];
  total: number;
  page: number;
  size: number;
}

export interface RunDetailResponse {
  run: WorkflowRun;
  action_logs: ActionLog[];
}

/** Per-step dry-run outcome (A3.5), keyed by step id so the builder can overlay it. */
export interface TestRunStep {
  step_id: string;
  type: 'action' | 'condition' | 'delay' | 'split';
  status: 'run' | 'skip';
  reason?: string;
  action_type?: string;
  resolved_params?: Record<string, unknown>;
  condition_result?: boolean;
  branch?: 'yes' | 'no';
  /** For a wait-for-event step this is the timeout, not a fixed pause. */
  delay_sec?: number;
  wait_event?: string;
}

export interface TestRunResponse {
  condition_result: boolean;
  steps: TestRunStep[];
}

export interface ValidationError {
  field: string;
  message: string;
}

export type TriggerType = TriggerSpec['type'];
export type ActionType = ActionSpec['type'];

export const TRIGGER_LABELS: Record<string, string> = {
  contact_created: 'Contact Created',
  contact_updated: 'Contact Updated',
  deal_stage_changed: 'Deal Stage Changed',
  task_status_changed: 'Task Status Changed',
  no_activity_days: 'No Activity (Days)',
  webhook_inbound: 'Webhook Inbound',
  schedule: 'Schedule',
  date_field: 'Date Reached',
  email_opened: 'Email Opened',
  email_clicked: 'Link Clicked',
};

export const ACTION_LABELS: Record<ActionType, string> = {
  send_email: 'Send Email',
  create_task: 'Create Task',
  assign_user: 'Assign User',
  send_webhook: 'Send Webhook',
  delay: 'Delay',
  update_record: 'Update Record',
  log_activity: 'Log Activity',
  notify_user: 'Notify User',
  create_record: 'Create Record',
  find_records: 'Find Records',
  enroll_records: 'Enroll Records',
  ai_generate: 'Generate with AI',
};

export const ACTION_ICONS: Record<ActionType, string> = {
  send_email: '✉️',
  create_task: '✅',
  assign_user: '👤',
  send_webhook: '🔗',
  delay: '⏱️',
  update_record: '📝',
  log_activity: '📞',
  notify_user: '🔔',
  create_record: '🆕',
  find_records: '🔍',
  enroll_records: '↪️',
  ai_generate: '✨',
};

export const STATUS_COLORS: Record<string, string> = {
  pending: '#9CA3AF',
  running: '#3B82F6',
  waiting: '#F59E0B',
  completed: '#10B981',
  failed: '#EF4444',
  skipped: '#F59E0B',
};

// Run + action-log statuses mapped to the shared Badge variants, so status
// chrome renders through tokens (light/dark safe) instead of hardcoded hex.
// Covers both WorkflowRun.status and ActionLog.status keys.
export type StatusBadgeVariant =
  | 'default'
  | 'secondary'
  | 'success'
  | 'warning'
  | 'destructive'
  | 'outline';

export const STATUS_BADGE_VARIANT: Record<string, StatusBadgeVariant> = {
  pending: 'secondary',
  running: 'default',
  waiting: 'warning',
  completed: 'success',
  failed: 'destructive',
  skipped: 'warning',
  // ActionLog-only statuses
  success: 'success',
  retrying: 'warning',
};

// NOTE: Operator definitions live ONLY in useSchema.ts → getOperatorsForType().
// Do NOT define operator lists here. Single source of truth.

