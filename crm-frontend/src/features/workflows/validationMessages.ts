// Human copy for the backend automation validator (A7.3).
//
// The validator in crm-backend/internal/automation/validator.go speaks to the API:
// its messages name raw trigger/action ids and param keys ("deal_stage_changed
// requires 'to_stage' parameter"). That's the right contract for a machine — and
// Go tests assert on those strings — but it's jargon in the Copilot panel, which is
// the only place we surface them.
//
// So we translate here, in the UI layer, keyed on the error's `field` — a stable
// machine path ("trigger.params.to_stage") that survives message rewording. Anything
// unmapped falls back to the raw message with its identifiers humanized, so a new
// backend error is never worse than it was before this file existed.

/** One backend validation error: `{ field, message }` from AIDraftValidation. */
export interface RawValidationError {
  field: string;
  message: string;
}

export interface HumanIssue {
  /** Where to look on the canvas — "Trigger", "Step 3", "Conditions". Null when unknown. */
  location: string | null;
  /** The human sentence. */
  text: string;
}

/** Machine identifier → the label the UI already uses for it. */
const TERMS: Record<string, string> = {
  // triggers
  deal_stage_changed: 'Deal Stage Changed',
  no_activity_days: 'No Activity',
  date_field: 'Date Field',
  webhook_inbound: 'Inbound Webhook',
  schedule: 'Schedule',
  // actions
  send_email: 'Send Email',
  create_task: 'Create Task',
  assign_user: 'Assign User',
  send_webhook: 'Send Webhook',
  update_contact: 'Update Contact',
  notify_user: 'Notify User',
  create_record: 'Create Record',
  find_records: 'Find Records',
  enroll_records: 'Enroll Records',
  ai_generate: 'AI Generate',
  log_activity: 'Log Activity',
  delay: 'Wait',
  // params
  to_stage: 'To Stage',
  from_stage: 'From Stage',
  duration_sec: 'Duration',
  until_field: 'wait-until field',
  activity_type: 'Activity Type',
  workflow_id: 'Workflow',
  user_id: 'User',
  max_tokens: 'Max Tokens',
  at_time: 'Time',
  offset_days: 'Offset',
  watch_field: 'Watched Field',
  watch_value: 'Watched Value',
  timezone: 'Timezone',
  cron: 'Schedule',
};

// Field path (array indices stripped) → the sentence a user should read.
//
// Keyed on field, not message, because several messages map to one user-visible
// problem: `trigger.params.to_stage` is emitted both as "requires 'to_stage'" (key
// absent) and "'to_stage' must not be empty" (key present but blank). Both mean the
// same thing to a person: pick a stage.
//
// Where a field is shared by several action types (`title`, `object`), the copy stays
// generic — the location prefix ("Step 3") already says which step to open.
const FIELD_HELP: Record<string, string> = {
  // ── Trigger ────────────────────────────────────────────────────────────────
  trigger: 'Add a trigger so this automation knows when to run.',
  'trigger.type': 'Choose a trigger type.',
  'trigger.params': 'This trigger needs a few more details.',
  'trigger.params.to_stage':
    'Deal Stage Changed needs a To Stage — pick the stage a deal must move into for this to fire.',
  'trigger.params.from_stage':
    'From Stage can’t be blank — pick a stage, or set it to Any to match a move from anywhere.',
  'trigger.params.days': 'No Activity needs a number of days of silence before it fires.',
  'trigger.params.entity': 'No Activity needs to watch either Contacts or Deals.',
  'trigger.params.cron': 'Enter a valid schedule for this trigger.',
  'trigger.params.timezone': 'Pick a valid timezone.',
  'trigger.params.object': 'Pick the object this trigger watches.',
  'trigger.params.field': 'Pick the date field this trigger watches.',
  'trigger.params.offset_days': 'Offset must be a number of days.',
  'trigger.params.at_time': 'Time must be in HH:MM, 24-hour format.',
  'trigger.params.watch_field': 'Pick the field to watch for changes.',
  'trigger.params.watch_value': 'Set the field to watch before setting the value to match.',

  // ── Conditions ─────────────────────────────────────────────────────────────
  conditions: 'These conditions are nested too deeply — flatten them with AND/OR groups.',

  // ── Steps / actions ────────────────────────────────────────────────────────
  'steps[].type': 'This step’s type isn’t recognised.',
  'steps[].action': 'This step is missing its action.',
  // NB: `steps[].action.type` / `.params.*` are folded onto `actions[].*` by
  // normalizeField, so they're keyed once, below.
  'steps[].condition': 'This If/Else step is missing its condition.',
  'steps[].delay': 'This Wait step needs a duration, or a date field to wait until.',
  // A Wait step's own params (validateDelayParams is only ever called with
  // `steps[N].delay`, so these never appear under actions[]).
  'steps[].delay.duration_sec': 'This Wait step needs a duration between 1 second and 30 days.',
  'steps[].delay.at_time': 'Wait-until time must be in HH:MM, 24-hour format.',
  'steps[].delay.timezone': 'Pick a valid timezone for this Wait step.',
  actions: 'Add at least one step to this automation.',
  'actions[].type': 'Choose an action for this step.',
  'actions[].params.to': 'Send Email needs a recipient in the To field.',
  'actions[].params.title': 'This step needs a title.',
  'actions[].params.entity': 'Assign User needs to know what to assign — a Contact or a Deal.',
  'actions[].params.strategy': 'Assign User needs an assignment strategy.',
  'actions[].params.user_id': 'Pick the person for this step.',
  'actions[].params.url': 'Send Webhook needs a URL to post to.',
  'actions[].params.updates': 'Update Contact needs at least one field to change.',
  'actions[].params.operation': 'Update Contact needs to know how to change the field.',
  'actions[].params.duration_sec': 'This Wait step needs a duration (up to 30 days).',
  'actions[].params.object': 'Pick the object for this step.',
  'actions[].params.fields': 'Create Record needs at least one field to fill in.',
  'actions[].params.workflow_id': 'Enroll Records needs a workflow to enrol into.',
  'actions[].params.prompt': 'AI Generate needs a prompt.',
  'actions[].params.max_tokens': 'Max Tokens must be a number between 1 and 1024.',
  'actions[].params.activity_type':
    'Log Activity needs a type — call, meeting, note, or email.',
};

/**
 * Canonicalize a field path so one map entry covers every shape the validator emits.
 *
 * Two normalizations:
 *  1. Array indices are dropped — `actions[2].params.to` keys the same entry as `[0]`.
 *  2. Action params arrive under BOTH `actions[N].params.x` (flat actions) and
 *     `steps[N].action.params.x` (the steps tree the current builder emits), because
 *     validateActionParams is called from both paths. Fold the steps form onto the
 *     actions form so a single key serves both — missing this would send every
 *     steps-based error, i.e. most of them, down the fallback path.
 */
function normalizeField(field: string): string {
  const noIndex = field.replace(/\[\d+\]/g, '[]');
  return noIndex.replace(/^steps\[\]\.action\./, 'actions[].');
}

/** "Trigger" / "Step 3" / "Conditions" — where on the canvas to look. */
function locationOf(field: string): string | null {
  if (field.startsWith('trigger')) return 'Trigger';
  if (field.startsWith('conditions')) return 'Conditions';
  const m = /^(?:steps|actions)\[(\d+)\]/.exec(field);
  if (m) return `Step ${Number(m[1]) + 1}`;
  if (field === 'steps' || field === 'actions') return null;
  return null;
}

/**
 * Last resort for a field we haven't mapped: swap machine identifiers for their
 * labels so the raw message at least reads like English.
 * "deal_stage_changed requires 'to_stage' parameter" → "Deal Stage Changed requires To Stage"
 */
function humanizeTerms(message: string): string {
  let out = message;
  // Quoted identifiers first ('to_stage' → To Stage), so the quotes come off too.
  out = out.replace(/'([a-z0-9_]+)'/g, (full, id: string) => TERMS[id] ?? full);
  // Then any bare snake_case identifier left over.
  out = out.replace(/\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b/g, (id) => TERMS[id] ?? id);
  // "… parameter" / "… property" is implementation noise once the name is humanised.
  out = out.replace(/\s+(parameter|property)\b/g, '');
  return out.charAt(0).toUpperCase() + out.slice(1);
}

/** Translate one backend validation error into copy a user can act on. */
export function humanizeValidationError(err: RawValidationError): HumanIssue {
  const help = FIELD_HELP[normalizeField(err.field ?? '')];
  return {
    location: locationOf(err.field ?? ''),
    text: help ?? humanizeTerms(err.message ?? ''),
  };
}

/**
 * Translate a batch, dropping duplicates. The validator can report the same
 * user-visible problem twice (e.g. `trigger.params` and `trigger.params.to_stage`
 * both fire when the params object is missing entirely) — one line is enough.
 */
export function humanizeValidationErrors(errors: RawValidationError[]): HumanIssue[] {
  const seen = new Set<string>();
  const out: HumanIssue[] = [];
  for (const err of errors) {
    const issue = humanizeValidationError(err);
    const key = `${issue.location ?? ''}|${issue.text}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(issue);
  }
  return out;
}
