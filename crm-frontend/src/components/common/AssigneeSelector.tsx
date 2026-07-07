import { useEffect, useState } from 'react';
import { getUsers, type UserListItem } from '../../lib/api';
import { useAuth } from '../../lib/auth';

interface Props {
  value?: string;
  onChange: (userId: string | undefined) => void;
  disabled?: boolean;
}

export default function AssigneeSelector({ value, onChange, disabled }: Props) {
  const { user, dataScope } = useAuth();
  const [users, setUsers] = useState<UserListItem[]>([]);

  // An 'own'-scoped role only sees records it owns, so it can only assign to
  // itself — generalizes the old hardcoded sales_rep check to any custom role
  // whose data_scope is 'own' (P6).
  const ownScoped = dataScope === 'own';

  useEffect(() => {
    if (ownScoped) {
      if (user) {
        setUsers([{ id: user.id, first_name: user.first_name, last_name: user.last_name, email: user.email }]);
        onChange(user.id);
      }
      return;
    }
    getUsers().then(setUsers).catch(() => setUsers([]));
  }, [ownScoped, user]);

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
