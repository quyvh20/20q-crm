import { useState, useEffect } from 'react';
import { getWorkspaceMembers, updateMemberRole, removeMember, type WorkspaceMember } from '../../lib/api';
import { useAuth } from '../../lib/auth';

const ROLE_OPTIONS = ['admin', 'manager', 'sales', 'viewer'];

export default function MembersList() {
  const { currentRole } = useAuth();
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [loading, setLoading] = useState(true);

  const canManage = currentRole === 'super_admin' || currentRole === 'admin';

  const fetchMembers = () => {
    setLoading(true);
    getWorkspaceMembers()
      .then(setMembers)
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    fetchMembers();
  }, []);

  const handleRoleChange = async (userId: string, newRole: string) => {
    try {
      await updateMemberRole(userId, newRole);
      fetchMembers();
    } catch {
      // silent
    }
  };

  const handleRemove = async (userId: string) => {
    if (!confirm('Remove this member from the workspace?')) return;
    try {
      await removeMember(userId);
      fetchMembers();
    } catch {
      // silent
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="animate-spin h-6 w-6 border-2 border-primary border-t-transparent rounded-full" />
      </div>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left">
        <thead>
          <tr className="border-b border-border">
            <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">Member</th>
            <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">Role</th>
            <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">Status</th>
            {canManage && (
              <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider text-right">Actions</th>
            )}
          </tr>
        </thead>
        <tbody>
          {members.map(m => (
            <tr key={m.user_id} className="border-b border-border/50 hover:bg-accent/30 transition-colors">
              <td className="py-3 pr-4">
                <div className="flex items-center gap-3">
                  {m.avatar_url ? (
                    <img src={m.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" />
                  ) : (
                    <div className="h-8 w-8 rounded-full bg-primary/10 flex items-center justify-center text-xs font-medium text-primary">
                      {m.first_name?.[0]}{m.last_name?.[0]}
                    </div>
                  )}
                  <div>
                    <p className="text-sm font-medium text-foreground">
                      {m.full_name || `${m.first_name} ${m.last_name}`}
                    </p>
                    <p className="text-xs text-muted-foreground">{m.email}</p>
                  </div>
                </div>
              </td>
              <td className="py-3 pr-4">
                {canManage && m.role !== 'super_admin' ? (
                  <select
                    value={m.role}
                    onChange={e => handleRoleChange(m.user_id, e.target.value)}
                    className="px-2 py-1 text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
                  >
                    {ROLE_OPTIONS.map(r => (
                      <option key={r} value={r}>{r.charAt(0).toUpperCase() + r.slice(1)}</option>
                    ))}
                  </select>
                ) : (
                  <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-primary/10 text-primary capitalize">
                    {m.role?.replace('_', ' ')}
                  </span>
                )}
              </td>
              <td className="py-3 pr-4">
                <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium capitalize ${
                  m.status === 'active'
                    ? 'bg-green-500/10 text-green-400'
                    : 'bg-yellow-500/10 text-yellow-400'
                }`}>
                  {m.status}
                </span>
              </td>
              {canManage && (
                <td className="py-3 text-right">
                  {m.role !== 'super_admin' && (
                    <button
                      onClick={() => handleRemove(m.user_id)}
                      className="text-xs text-red-400 hover:text-red-300 font-medium transition-colors"
                    >
                      Remove
                    </button>
                  )}
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      {members.length === 0 && (
        <p className="text-center text-muted-foreground py-8 text-sm">No members found.</p>
      )}
    </div>
  );
}
