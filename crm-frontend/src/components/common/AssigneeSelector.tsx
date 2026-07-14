import { useEffect, useState } from 'react';
import { getUsers, getTeammates, type UserListItem } from '../../lib/api';
import { useAuth } from '../../lib/auth';

interface Props {
  value?: string;
  onChange: (userId: string | undefined) => void;
  disabled?: boolean;
}

export default function AssigneeSelector({ value, onChange, disabled }: Props) {
  const { user, dataScope } = useAuth();
  const [users, setUsers] = useState<UserListItem[]>([]);

  // Row scope decides who this member may hand work to (U6, tri-state):
  //  - 'own'  → only themselves: they can't see anyone else's records, so the
  //             select is locked to self (generalizes the old sales_rep check).
  //  - 'team' → their teammates (members of a user group they belong to) — they
  //             CAN see those records, so they must be able to assign to them.
  //  - 'all'  → everyone in the workspace.
  const ownScoped = dataScope === 'own';
  const teamScoped = dataScope === 'team';

  useEffect(() => {
    let cancelled = false;
    if (ownScoped) {
      if (user) {
        setUsers([{ id: user.id, first_name: user.first_name, last_name: user.last_name, email: user.email }]);
        onChange(user.id);
      }
      return;
    }
    const load = teamScoped
      ? getTeammates().then((members) =>
          members
            .filter((m) => m.status === 'active')
            .map((m) => ({ id: m.user_id, first_name: m.first_name, last_name: m.last_name, email: m.email })),
        )
      : getUsers();
    load
      .then((list) => {
        if (cancelled) return;
        // A team-scoped member can always assign to themselves, even if they
        // belong to no group yet (an empty teammate list would otherwise leave
        // them unable to assign anything at all).
        const withSelf =
          user && !list.some((u) => u.id === user.id)
            ? [{ id: user.id, first_name: user.first_name, last_name: user.last_name, email: user.email }, ...list]
            : list;
        setUsers(teamScoped ? withSelf : list);
      })
      .catch(() => { if (!cancelled) setUsers([]); });
    return () => { cancelled = true; };
    // onChange is intentionally omitted: call sites pass a fresh closure each
    // render, and re-running this effect would re-fire the self-assign.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ownScoped, teamScoped, user]);

  return (
    <select
      value={value || ''}
      onChange={e => onChange(e.target.value || undefined)}
      disabled={disabled || ownScoped}
      className="w-full px-3 py-2 bg-background border border-border rounded-lg text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary transition-all disabled:opacity-60 disabled:cursor-not-allowed"
    >
      <option value="">Unassigned</option>
      {users.map(u => (
        <option key={u.id} value={u.id}>
          {u.first_name} {u.last_name} ({u.email})
        </option>
      ))}
    </select>
  );
}
