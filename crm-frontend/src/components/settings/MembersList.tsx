import { useState, useEffect } from 'react';
import { getWorkspaceMembers, updateMemberRole, removeMember, suspendMember, reinstateMember, transferOwnership, type WorkspaceMember } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { ShieldAlert, PauseCircle, PlayCircle, UserMinus, Crown, Shield } from 'lucide-react';

const ROLE_OPTIONS = ['admin', 'manager', 'sales', 'viewer'];

export default function MembersList() {
  const { currentRole, user } = useAuth();
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [loading, setLoading] = useState(true);

  // States for reassign modal
  const [reassignModalUser, setReassignModalUser] = useState<WorkspaceMember | null>(null);
  const [targetOwnerId, setTargetOwnerId] = useState<string>('');
  const [errorMsg, setErrorMsg] = useState('');

  const canManage = currentRole === 'owner' || currentRole === 'admin';

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

  const handleRemove = async (userId: string, input?: { strategy: 'transfer' | 'unassign'; reassign_to_user_id?: string }) => {
    if (!input && !confirm('Remove this member from the workspace?')) return;
    setErrorMsg('');
    try {
      await removeMember(userId, input);
      setReassignModalUser(null);
      fetchMembers();
    } catch (err: any) {
      if (err.message.includes('reassign_to_user_id')) {
        const mem = members.find(m => m.user_id === userId);
        if (mem) setReassignModalUser(mem);
      } else {
        setErrorMsg(err.message || 'Failed to remove member');
      }
    }
  };

  const handleSuspend = async (userId: string) => {
    if (!confirm('Suspend this member? They will lose access immediately.')) return;
    try {
      await suspendMember(userId);
      fetchMembers();
    } catch (err: any) {
      setErrorMsg(err.message);
    }
  };

  const handleReinstate = async (userId: string) => {
    try {
      await reinstateMember(userId);
      fetchMembers();
    } catch (err: any) {
      setErrorMsg(err.message);
    }
  };

  const handleTransfer = async (userId: string) => {
    if (!confirm('Transfer ownership? You will lose Owner privileges.')) return;
    try {
      await transferOwnership(userId);
      window.location.reload(); // Hard reload to update auth context completely
    } catch (err: any) {
      setErrorMsg(err.message);
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
    <>
      <div className="overflow-x-auto">
        {errorMsg && (
          <div className="mb-4 p-3 bg-red-500/10 border border-red-500/20 text-red-500 text-sm rounded-lg flex items-center gap-2">
            <ShieldAlert className="w-4 h-4" />
            {errorMsg}
          </div>
        )}
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
              <tr key={m.user_id} className={`border-b border-border/50 hover:bg-accent/30 transition-colors ${m.status === 'suspended' ? 'opacity-60' : ''}`}>
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
                      <p className="text-sm font-medium text-foreground flex items-center gap-2">
                        {m.full_name || `${m.first_name} ${m.last_name}`}
                        {m.user_id === user?.id && <span className="text-[10px] bg-primary/20 text-primary px-1.5 py-0.5 rounded-sm">You</span>}
                      </p>
                      <p className="text-xs text-muted-foreground">{m.email}</p>
                    </div>
                  </div>
                </td>
                <td className="py-3 pr-4">
                  {canManage && m.role !== 'owner' && m.user_id !== user?.id && m.status !== 'deleted' ? (
                    <select
                      value={m.role}
                      onChange={e => handleRoleChange(m.user_id, e.target.value)}
                      className="px-2 py-1 flex items-center text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
                    >
                      {ROLE_OPTIONS.map(r => (
                        <option key={r} value={r}>{r.charAt(0).toUpperCase() + r.slice(1)}</option>
                      ))}
                    </select>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-neutral-800 text-neutral-300 capitalize border border-neutral-700">
                      {m.role === 'owner' && <Crown className="w-3 h-3 text-yellow-500" />}
                      {m.role === 'admin' && <Shield className="w-3 h-3 text-blue-400" />}
                      {m.role?.replace('_', ' ')}
                    </span>
                  )}
                </td>
                <td className="py-3 pr-4">
                  <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-bold uppercase tracking-wider border ${
                    m.status === 'active'
                      ? 'bg-green-500/10 text-green-400 border-green-500/20'
                      : m.status === 'suspended'
                      ? 'bg-orange-500/10 text-orange-400 border-orange-500/20'
                      : m.status === 'invited'
                      ? 'bg-blue-500/10 text-blue-400 border-blue-500/20'
                      : 'bg-neutral-500/10 text-neutral-400 border-neutral-500/20'
                  }`}>
                    {m.status}
                  </span>
                </td>
                {canManage && (
                  <td className="py-3 text-right">
                    <div className="flex items-center justify-end gap-3 opacity-0 group-hover:opacity-100 transition-opacity">
                      {/* We make it visible always for simplicity on touch, or use visibility tricks. Using visible here. */}
                    </div>
                    <div className="flex items-center justify-end gap-3">
                      {m.user_id !== user?.id && m.role !== 'owner' && (
                        <>
                          {currentRole === 'owner' && m.status === 'active' && (
                            <button onClick={() => handleTransfer(m.user_id)} title="Transfer Ownership" className="text-muted-foreground hover:text-purple-400 transition-colors">
                              <Crown className="w-4 h-4" />
                            </button>
                          )}
                          {m.status === 'active' && (
                            <button onClick={() => handleSuspend(m.user_id)} title="Suspend Member" className="text-muted-foreground hover:text-orange-400 transition-colors">
                              <PauseCircle className="w-4 h-4" />
                            </button>
                          )}
                          {m.status === 'suspended' && (
                            <button onClick={() => handleReinstate(m.user_id)} title="Reinstate Member" className="text-muted-foreground hover:text-green-400 transition-colors">
                              <PlayCircle className="w-4 h-4" />
                            </button>
                          )}
                          <button onClick={() => handleRemove(m.user_id)} title="Remove Member" className="text-muted-foreground hover:text-red-400 transition-colors">
                            <UserMinus className="w-4 h-4" />
                          </button>
                        </>
                      )}
                    </div>
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

      {reassignModalUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={() => setReassignModalUser(null)} />
          <div className="relative bg-card border border-border rounded-2xl shadow-xl w-full max-w-sm p-6">
            <h3 className="text-lg font-bold text-foreground mb-2">Reassign Data</h3>
            <p className="text-sm text-muted-foreground mb-4">
              <strong>{reassignModalUser.first_name}</strong> owns active accounts or contacts. Select a member to take ownership before deletion.
            </p>
            <div className="mb-6">
              <label className="block text-xs font-semibold uppercase tracking-wider mb-2 text-muted-foreground">New Owner</label>
              <select
                className="w-full px-3 py-2 bg-background border border-border rounded-lg text-foreground focus:outline-none focus:border-primary"
                value={targetOwnerId}
                onChange={e => setTargetOwnerId(e.target.value)}
              >
                <option value="">-- Select Member --</option>
                {members.filter(m => m.user_id !== reassignModalUser.user_id && m.status === 'active').map(m => (
                  <option key={m.user_id} value={m.user_id}>{m.full_name || m.email}</option>
                ))}
              </select>
            </div>
            <div className="flex gap-2">
              <button 
                onClick={() => setReassignModalUser(null)} 
                className="flex-1 px-4 py-2 border border-border rounded-xl text-sm font-medium hover:bg-accent transition"
              >
                Cancel
              </button>
              <button 
                disabled={!targetOwnerId}
                onClick={() => handleRemove(reassignModalUser.user_id, { strategy: 'transfer', reassign_to_user_id: targetOwnerId })}
                className="flex-1 px-4 py-2 bg-red-500/20 text-red-500 border border-red-500/50 rounded-xl text-sm font-bold hover:bg-red-500/30 transition disabled:opacity-50"
              >
                Reassign & Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
