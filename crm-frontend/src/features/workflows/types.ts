// ============================================================
// Workflow Automation Types
// ============================================================

export interface TriggerSpec {
  type: 'contact_created' | 'contact_updated' | 'deal_stage_changed' | 'no_activity_days' | 'webhook_inbound';
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
  type: 'send_email' | 'create_task' | 'assign_user' | 'send_webhook' | 'delay';
  id: string;
  params: Record<string, unknown>;
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
  status: 'pending' | 'running' | 'completed' | 'failed' | 'skipped';
  trigger_context: Record<string, unknown>;
  current_action_idx: number;
  completed_actions: number[] | null;
  last_error?: string;
  retry_count: number;
  started_at?: string;
  finished_at?: string;
  created_at: string;
}

export interface ActionLog {
  id: string;
  run_id: string;
  action_idx: number;
  action_type: string;
  status: 'success' | 'failed' | 'retrying';
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

export interface TestRunResponse {
  condition_result: boolean;
  actions: {
    id: string;
    type: string;
    resolved_params: Record<string, unknown>;
  }[];
}

export interface ValidationError {
  field: string;
  message: string;
}

export type TriggerType = TriggerSpec['type'];
export type ActionType = ActionSpec['type'];

export const TRIGGER_LABELS: Record<TriggerType, string> = {
  contact_created: 'Contact Created',
  contact_updated: 'Contact Updated',
  deal_stage_changed: 'Deal Stage Changed',
  no_activity_days: 'No Activity (Days)',
  webhook_inbound: 'Webhook Inbound',
};

export const ACTION_LABELS: Record<ActionType, string> = {
  send_email: 'Send Email',
  create_task: 'Create Task',
  assign_user: 'Assign User',
  send_webhook: 'Send Webhook',
  delay: 'Delay',
};

export const ACTION_ICONS: Record<ActionType, string> = {
  send_email: '✉️',
  create_task: '✅',
  assign_user: '👤',
  send_webhook: '🔗',
  delay: '⏱️',
};

export const STATUS_COLORS: Record<string, string> = {
  pending: '#9CA3AF',
  running: '#3B82F6',
  completed: '#10B981',
  failed: '#EF4444',
  skipped: '#F59E0B',
};

export const CONDITION_OPERATORS = [
  { value: 'eq', label: 'Equals' },
  { value: 'neq', label: 'Not Equals' },
  { value: 'gt', label: 'Greater Than' },
  { value: 'gte', label: 'Greater or Equal' },
  { value: 'lt', label: 'Less Than' },
  { value: 'lte', label: 'Less or Equal' },
  { value: 'contains', label: 'Contains' },
  { value: 'not_contains', label: 'Not Contains' },
  { value: 'in', label: 'In' },
  { value: 'not_in', label: 'Not In' },
  { value: 'is_empty', label: 'Is Empty' },
  { value: 'is_not_empty', label: 'Is Not Empty' },
  { value: 'starts_with', label: 'Starts With' },
  { value: 'ends_with', label: 'Ends With' },
];
