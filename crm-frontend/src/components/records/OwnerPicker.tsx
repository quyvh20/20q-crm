import { useEffect, useState } from 'react';
import { getWorkspaceMembers, type WorkspaceMember } from '../../lib/api';

interface Props {
  /** Current owner's user id; '' / null / undefined = unassigned. */
  value?: string | null;
  /** Emits a user id, or null to unassign (what the API expects to clear it). */
  onChange: (userId: string | null) => void;
  disabled?: boolean;
  id?: string;
}

const memberName = (m: WorkspaceMember) =>
  m.full_name || `${m.first_name} ${m.last_name}`.trim() || m.email;

// OwnerPicker is the dedicated control for a record's owner (U6). Owner is not a
// registry field — it never appears in schema.fields — so every form renders it
// from schema.has_owner and writes it as `owner_user_id` inside the fields map
// ('' → null, which unassigns).
export default function OwnerPicker({ value, onChange, disabled, id }: Props) {
  const [members, setMembers] = useState<WorkspaceMember[]>([]);

  useEffect(() => {
    let cancelled = false;
    getWorkspaceMembers()
      .then((m) => { if (!cancelled) setMembers(m.filter((x) => x.status === 'active')); })
      .catch(() => { if (!cancelled) setMembers([]); });
    return () => { cancelled = true; };
  }, []);

  // A member list that hasn't loaded (or a member the caller can't see) must not
  // silently drop the existing owner from the select — keep the current value as
  // an option so a save doesn't hand the record away by accident.
  const knownOwner = !!value && members.some((m) => m.user_id === value);

  return (
    <select
      id={id}
      aria-label="Owner"
      value={value || ''}
      onChange={(e) => onChange(e.target.value || null)}
      disabled={disabled}
      className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground transition-all focus:border-primary focus:outline-none focus:ring-2 focus:ring-primary/50 disabled:cursor-not-allowed disabled:opacity-60"
    >
      <option value="">Unassigned</option>
      {!knownOwner && value && <option value={value}>Current owner</option>}
      {members.map((m) => (
        <option key={m.user_id} value={m.user_id}>{memberName(m)}</option>
      ))}
    </select>
  );
}
