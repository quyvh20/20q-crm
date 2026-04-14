import type { CommandEvent } from '../../lib/api';

interface Props {
  event: CommandEvent;
}

const TOOL_ICONS: Record<string, string> = {
  search_contacts: '👤',
  search_deals: '💼',
  create_task: '✅',
  compose_email: '📧',
  update_deal: '📊',
  log_activity: '📝',
  get_analytics: '📈',
};

const TOOL_LABELS: Record<string, string> = {
  search_contacts: 'Search Contacts',
  search_deals: 'Search Deals',
  create_task: 'Create Task',
  compose_email: 'Compose Email',
  update_deal: 'Update Deal',
  log_activity: 'Log Activity',
  get_analytics: 'Analytics',
};

function summarizeToolResult(tool: string, data: unknown): string {
  const d = data as Record<string, unknown>;
  switch (tool) {
    case 'search_contacts':
      return `Found ${d.count || 0} contacts`;
    case 'search_deals':
      return `Found ${d.count || 0} deals`;
    case 'create_task':
      return d.created ? `✅ Created: ${d.title}` : `❌ Failed`;
    case 'compose_email':
      return `📧 Draft ready`;
    case 'update_deal':
      return d.updated ? `Updated deal: ${d.title || d.deal_id}` : `❌ Failed`;
    case 'log_activity':
      return d.logged ? `Logged ${d.type}: ${d.title}` : `❌ Failed`;
    case 'get_analytics':
      if (d.total_value !== undefined) {
        return `Pipeline: $${Number(d.total_value).toLocaleString()} · ${d.active_deals} active · ${d.won_deals} won`;
      }
      return `Analytics data loaded`;
    default:
      return 'Completed';
  }
}

export default function CommandEventCard({ event }: Props) {
  const tool = event.tool || '';
  const icon = TOOL_ICONS[tool] || '⚡';
  const label = TOOL_LABELS[tool] || tool;

  if (event.type === 'thinking') {
    return (
      <div className="cc-event cc-thinking">
        <div className="cc-event-icon">
          <span className="cc-pulse">🤔</span>
        </div>
        <span className="cc-event-text">{event.message}</span>
      </div>
    );
  }

  if (event.type === 'planning') {
    return (
      <div className="cc-event cc-planning">
        <div className="cc-event-icon">
          <span className="cc-pulse">⚡</span>
        </div>
        <span className="cc-event-text">{event.message}</span>
      </div>
    );
  }

  if (event.type === 'tool_result') {
    return (
      <div className="cc-event cc-tool-result">
        <div className="cc-event-icon">{icon}</div>
        <div className="cc-tool-info">
          <span className="cc-tool-label">{label}</span>
          <span className="cc-tool-summary">{summarizeToolResult(tool, event.data)}</span>
        </div>
      </div>
    );
  }

  if (event.type === 'error') {
    return (
      <div className="cc-event cc-error">
        <div className="cc-event-icon">⚠️</div>
        <span className="cc-event-text">{event.message}</span>
      </div>
    );
  }

  if (event.type === 'response') {
    return (
      <div className="cc-event cc-response">
        <div className="cc-event-icon">🤖</div>
        <div className="cc-response-content">{event.message}</div>
      </div>
    );
  }

  return null;
}
