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
  type: 'send_email' | 'create_task' | 'assign_user' | 'send_webhook' | 'delay' | 'update_record' | 'log_activity';
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
}

export interface WorkflowStep {
  id: string;
  type: 'action' | 'condition' | 'delay';
  action?: ActionSpec;
  condition?: ConditionGroup;
  delay?: DelayParams;
  yes_steps?: WorkflowStep[];
  no_steps?: WorkflowStep[];
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
  actions: ActionSpec[];
  steps?: WorkflowStep[];
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
  type: 'action' | 'condition' | 'delay';
  status: 'run' | 'skip';
  reason?: string;
  action_type?: string;
  resolved_params?: Record<string, unknown>;
  condition_result?: boolean;
  branch?: 'yes' | 'no';
  delay_sec?: number;
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
  no_activity_days: 'No Activity (Days)',
  webhook_inbound: 'Webhook Inbound',
  schedule: 'Schedule',
  date_field: 'Date Reached',
};

export const ACTION_LABELS: Record<ActionType, string> = {
  send_email: 'Send Email',
  create_task: 'Create Task',
  assign_user: 'Assign User',
  send_webhook: 'Send Webhook',
  delay: 'Delay',
  update_record: 'Update Record',
  log_activity: 'Log Activity',
};

export const ACTION_ICONS: Record<ActionType, string> = {
  send_email: '✉️',
  create_task: '✅',
  assign_user: '👤',
  send_webhook: '🔗',
  delay: '⏱️',
  update_record: '📝',
  log_activity: '📞',
};

export const STATUS_COLORS: Record<string, string> = {
  pending: '#9CA3AF',
  running: '#3B82F6',
  waiting: '#F59E0B',
  completed: '#10B981',
  failed: '#EF4444',
  skipped: '#F59E0B',
};

// NOTE: Operator definitions live ONLY in useSchema.ts → getOperatorsForType().
// Do NOT define operator lists here. Single source of truth.

