// Default params for a freshly-inserted action step, keyed by action type. Relocated
// here from the (deleted) legacy nodes/AddNodeButton in A8; the InsertMenu builds a
// step with these when the user picks an action. Types not listed (e.g. update_record)
// start with an empty param object and are filled in via the config panel.
export function getDefaultParams(type: string): Record<string, unknown> {
  switch (type) {
    case 'send_email':
      return { to: '', subject: '', body_html: '' };
    case 'create_task':
      return { title: '', priority: 'medium', due_in_days: 3 };
    case 'assign_user':
      return { entity: 'contact', strategy: 'round_robin' };
    case 'send_webhook':
      return { url: '', method: 'POST', timeout_sec: 10 };
    case 'delay':
      return { duration_sec: 60 };
    case 'log_activity':
      return { activity_type: 'note', title: '', body: '' };
    case 'notify_user':
      return { recipient: 'owner_field', title: '', body: '' };
    case 'create_record':
      return { object: '', fields: [] };
    case 'find_records':
      return { object: '', filters: [], limit: 100 };
    case 'enroll_records':
      return { workflow_id: '', object: '', filters: [] };
    case 'ai_generate':
      return { prompt: '', max_tokens: 512 };
    default:
      return {};
  }
}
