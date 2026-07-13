import { useState, useEffect } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { getWorkspaceMembers, updateMemberRole, removeMember, suspendMember, reinstateMember, transferOwnership, sendMemberResetLink, listInvitations, resendInvitation, revokeInvitation, getRoleOptions, ReassignmentRequiredError, type WorkspaceMember, type Invitation, type RoleOption } from '../../lib/api';
import { useAuth, usePermissions } from '../../lib/auth';
import { prettyRole } from '../../lib/roles';
import { useConfirm } from '../common/ConfirmDialog';
import { ShieldAlert, PauseCircle, PlayCircle, UserMinus, Crown, Shield, KeyRound, CheckCircle2, RotateCw, X, HelpCircle } from 'lucide-react';

export default function MembersList() {
  const { user, hasCapability, isOwner } = useAuth();
  const { can } = usePermissions();
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [roles, setRoles] = useState<RoleOption[]>([]);
  const [loading, setLoading] = useState(true);

  // States for reassign modal
  const [reassignModalUser, setReassignModalUser] = useState<WorkspaceMember | null>(null);
  const [targetOwnerId, setTargetOwnerId] = useState<string>('');
  const [ownedCounts, setOwnedCounts] = useState<{ contacts: number; deals: number } | null>(null);
  const [removeStrategy, setRemoveStrategy] = useState<'transfer' | 'unassign'>('transfer');
  const [errorMsg, setErrorMsg] = useState('');
  const [noticeMsg, setNoticeMsg] = useState('');
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

  // Role filter lives in the URL (?role=<id>), not component state: MembersSection
  // remounts MembersList after an invite (key bump), and URL state survives that.
  const [searchParams, setSearchParams] = useSearchParams();
  const roleFilter = searchParams.get('role') ?? '';
  const setRoleFilter = (id: string) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (id) next.set('role', id);
      else next.delete('role');
      return next;
    }, { replace: true });
  };

  const canManage = hasCapability('members.manage');
  // Owner role ids (resolved from the dynamic options, P6) so the owner member is
  // shown as a locked badge — you transfer ownership, you don't re-assign it.
  const ownerRoleIds = new Set(roles.filter((r) => r.is_owner).map((r) => r.id));

  // Client-side filter keyed by role_id. An unknown id (stale link) simply matches
  // nothing — the zero-result state below offers the "All roles" reset.
  const visibleMembers = roleFilter ? members.filter((m) => m.role_id === roleFilter) : members;

  const fetchMembers = () => {
    setLoading(true);
    getWorkspaceMembers()
      .then(setMembers)
      .catch((e) => setErrorMsg(e instanceof Error ? e.message : 'Failed to load members')) // a load failure used to render as "No members found"
      .finally(() => setLoading(false));
  };

  const fetchInvitations = () => {
    if (!canManage) return;
    listInvitations().then(setInvitations).catch(() => setInvitations([]));
  };

  useEffect(() => {
    fetchMembers();
    fetchInvitations();
    // Role options are an any-member read (P6) — always fetched, since the role
    // filter above the table needs the labels even without members.manage.
    getRoleOptions().then(setRoles).catch(() => setRoles([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSendReset = async (m: WorkspaceMember) => {
    if (!(await confirmDialog({
      title: 'Send password reset link',
      body: `Email ${m.full_name || m.email} a password reset link? You won't see or set their password — their account may span workspaces.`,
      confirmLabel: 'Send link', tone: 'default',
    }))) return;
    setErrorMsg(''); setNoticeMsg('');
    try {
      await sendMemberResetLink(m.user_id);
      setNoticeMsg(`A password reset link was emailed to ${m.email}.`);
    } catch (err: any) {
      setErrorMsg(err.message || 'Failed to send reset link');
    }
  };

  const handleResendInvite = async (inv: Invitation) => {
    setErrorMsg(''); setNoticeMsg('');
    try {
      await resendInvitation(inv.id);
      setNoticeMsg(`Invitation resent to ${inv.email}.`);
      fetchInvitations();
    } catch (err: any) {
      setErrorMsg(err.message || 'Failed to resend invitation');
    }
  };

  const handleRevokeInvite = async (inv: Invitation) => {
    if (!(await confirmDialog({
      title: 'Revoke invitation',
      body: `Revoke the invitation for ${inv.email}? Their link will stop working.`,
      confirmLabel: 'Revoke',
    }))) return;
    setErrorMsg(''); setNoticeMsg('');
    try {
      await revokeInvitation(inv.id);
      fetchInvitations();
    } catch (err: any) {
      setErrorMsg(err.message || 'Failed to revoke invitation');
    }
  };

  const handleRoleChange = async (userId: string, newRoleId: string) => {
    // Optimistic UI update (keyed by role_id, P6; also update the display name).
    const newName = roles.find(r => r.id === newRoleId)?.name ?? '';
    setMembers(prev => prev.map(m => m.user_id === userId ? { ...m, role_id: newRoleId, role: newName } : m));
    setErrorMsg('');
    try {
      await updateMemberRole(userId, newRoleId);
      getWorkspaceMembers().then(setMembers);
    } catch (err: any) {
      setErrorMsg(err.message || 'Failed to update role');
      // Revert if failed
      getWorkspaceMembers().then(setMembers);
    }
  };

  const handleRemove = async (userId: string, input?: { strategy: 'transfer' | 'unassign'; reassign_to_user_id?: string }) => {
    if (!input && !(await confirmDialog({
      title: 'Remove member',
      body: 'Remove this member from the workspace? They lose access immediately; their account is untouched and they can be re-invited later.',
      confirmLabel: 'Remove',
    }))) return;
    setErrorMsg('');
    try {
      await removeMember(userId, input);
      setReassignModalUser(null);
      setOwnedCounts(null);
      fetchMembers();
    } catch (err: any) {
      // Keyed off the typed 409 code, not a message substring: the member still
      // owns records, so open the dialog with the real counts (U0.2).
      if (err instanceof ReassignmentRequiredError) {
        const mem = members.find(m => m.user_id === userId);
        if (mem) {
          setOwnedCounts(err.owned);
          setRemoveStrategy('transfer');
          setTargetOwnerId('');
          setReassignModalUser(mem);
        }
      } else {
        setErrorMsg(err.message || 'Failed to remove member');
      }
    }
  };

  const handleSuspend = async (userId: string) => {
    if (!(await confirmDialog({
      title: 'Suspend member',
      body: 'Suspend this member? They lose access immediately. You can reinstate them at any time.',
      confirmLabel: 'Suspend',
    }))) return;
    // Optimistic update
    setMembers(prev => prev.map(m => m.user_id === userId ? { ...m, status: 'suspended' } : m));
    try {
      await suspendMember(userId);
      getWorkspaceMembers().then(setMembers);
    } catch (err: any) {
      setErrorMsg(err.message);
      getWorkspaceMembers().then(setMembers);
    }
  };

  const handleReinstate = async (userId: string) => {
    // Optimistic update
    setMembers(prev => prev.map(m => m.user_id === userId ? { ...m, status: 'active' } : m));
    try {
      await reinstateMember(userId);
      getWorkspaceMembers().then(setMembers);
    } catch (err: any) {
      setErrorMsg(err.message);
      getWorkspaceMembers().then(setMembers);
    }
  };

  const handleTransfer = async (userId: string) => {
    if (!(await confirmDialog({
      title: 'Transfer ownership',
      body: 'Transfer ownership of this workspace? You will lose Owner privileges and become an Admin — only the new owner can transfer it back.',
      confirmLabel: 'Transfer ownership',
    }))) return;
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
        {noticeMsg && (
          <div className="mb-4 p-3 bg-green-500/10 border border-green-500/20 text-green-500 text-sm rounded-lg flex items-center gap-2">
            <CheckCircle2 className="w-4 h-4" />
            {noticeMsg}
          </div>
        )}
        {/* Role filter (U3.3) — the landing target for "N members" links on role
            cards/detail. URL-backed, so those deep links arrive pre-filtered. */}
        {roles.length > 0 && (
          <div className="mb-4 flex items-center gap-2 flex-wrap">
            <label htmlFor="member-role-filter" className="text-xs text-muted-foreground">
              Filter by role
            </label>
            <select
              id="member-role-filter"
              value={roleFilter}
              onChange={(e) => setRoleFilter(e.target.value)}
              className="px-2 py-1 text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
            >
              <option value="">All roles</option>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>{prettyRole(r.name)}</option>
              ))}
            </select>
            {hasCapability('roles.manage') && (
              <Link
                to={roleFilter ? `/settings/roles/${roleFilter}` : '/settings/roles'}
                className="text-xs text-blue-500 hover:underline"
              >
                What does {roleFilter ? 'this role' : 'each role'} grant?
              </Link>
            )}
          </div>
        )}
        {/* Options failed/empty but a ?role= deep link is still filtering the
            table — keyed off the param itself so the filter stays visible and
            escapable even without the select above. */}
        {roles.length === 0 && roleFilter && (
          <div className="mb-4">
            <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium bg-muted text-muted-foreground border border-border">
              Filtered by role
              <button
                onClick={() => setRoleFilter('')}
                aria-label="Clear role filter"
                className="hover:text-foreground transition-colors"
              >
                <X className="w-3 h-3" />
              </button>
            </span>
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
            {visibleMembers.map(m => (
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
                  {canManage && !ownerRoleIds.has(m.role_id) && m.user_id !== user?.id && m.status !== 'deleted' ? (
                    <div className="flex items-center gap-1.5">
                      <select
                        value={m.role_id}
                        onChange={e => handleRoleChange(m.user_id, e.target.value)}
                        className="px-2 py-1 flex items-center text-sm bg-background border border-border rounded-lg text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
                      >
                        {roles.map(r => (
                          <option key={r.id} value={r.id} disabled={r.is_owner}>
                            {prettyRole(r.name)}{r.is_owner ? ' — transfer instead' : ''}
                          </option>
                        ))}
                      </select>
                      {/* Jump into the assigned role's capability list right where
                          it's assigned (U3.3) — replaces the near-invisible
                          title-tooltips on the options (U3.5). */}
                      {can('roles.manage') && (
                        <Link
                          to={`/settings/roles/${m.role_id}`}
                          aria-label="What does this role grant?"
                          className="text-muted-foreground hover:text-blue-500 transition-colors"
                        >
                          <HelpCircle className="w-3.5 h-3.5" />
                        </Link>
                      )}
                    </div>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-muted text-muted-foreground border border-border">
                      {m.role === 'owner' && <Crown className="w-3 h-3 text-yellow-500" />}
                      {m.role === 'admin' && <Shield className="w-3 h-3 text-blue-400" />}
                      {prettyRole(m.role)}
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
                    <div className="flex items-center justify-end gap-3">
                      {m.user_id !== user?.id && m.status !== 'invited' && m.status !== 'deleted' && (
                        <button onClick={() => handleSendReset(m)} title="Send password reset link" className="text-muted-foreground hover:text-blue-400 transition-colors">
                          <KeyRound className="w-4 h-4" />
                        </button>
                      )}
                      {m.user_id !== user?.id && !ownerRoleIds.has(m.role_id) && (
                        <>
                          {isOwner && m.status === 'active' && (
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
        {members.length === 0 ? (
          <p className="text-center text-muted-foreground py-8 text-sm">No members found.</p>
        ) : visibleMembers.length === 0 ? (
          <p className="text-center text-muted-foreground py-8 text-sm">
            No members hold this role.{' '}
            <button onClick={() => setRoleFilter('')} className="text-blue-500 hover:underline">
              Show all roles
            </button>
          </p>
        ) : null}
      </div>

      {canManage && invitations.length > 0 && (
        <div className="mt-8">
          <h3 className="text-sm font-semibold text-foreground mb-3 flex items-center gap-2">
            Pending invitations
            <span className="text-[11px] font-medium bg-blue-500/10 text-blue-400 border border-blue-500/20 px-1.5 py-0.5 rounded-full">{invitations.length}</span>
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-left">
              <thead>
                <tr className="border-b border-border">
                  <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">Email</th>
                  <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">Role</th>
                  <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">Expires</th>
                  <th className="pb-3 text-xs font-medium text-muted-foreground uppercase tracking-wider text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {invitations.map(inv => {
                  const expired = inv.status === 'expired';
                  return (
                  <tr key={inv.id} className="border-b border-border/50 hover:bg-accent/30 transition-colors">
                    <td className="py-3 pr-4 text-sm text-foreground">
                      <span className="inline-flex items-center gap-2">
                        {inv.email}
                        {expired && (
                          <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-amber-500/15 text-amber-600 border border-amber-500/30">
                            Expired
                          </span>
                        )}
                      </span>
                    </td>
                    <td className="py-3 pr-4">
                      <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-muted text-muted-foreground border border-border">
                        {prettyRole(inv.role)}
                      </span>
                    </td>
                    <td className={`py-3 pr-4 text-xs ${expired ? 'text-amber-600 font-medium' : 'text-muted-foreground'}`}>
                      {expired ? 'Expired — resend to renew' : new Date(inv.expires_at).toLocaleDateString()}
                    </td>
                    <td className="py-3 text-right">
                      <div className="flex items-center justify-end gap-3">
                        <button onClick={() => handleResendInvite(inv)} title="Resend invitation" className="text-muted-foreground hover:text-blue-400 transition-colors">
                          <RotateCw className="w-4 h-4" />
                        </button>
                        <button onClick={() => handleRevokeInvite(inv)} title="Revoke invitation" className="text-muted-foreground hover:text-red-400 transition-colors">
                          <X className="w-4 h-4" />
                        </button>
                      </div>
                    </td>
                  </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {reassignModalUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
          <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={() => setReassignModalUser(null)} />
          <div className="relative bg-card border border-border rounded-2xl shadow-xl w-full max-w-sm p-6">
            <h3 className="text-lg font-bold text-foreground mb-2">Remove {reassignModalUser.first_name || reassignModalUser.email}</h3>
            <p className="text-sm text-muted-foreground mb-4">
              This member still owns{' '}
              <strong className="text-foreground">
                {ownedCounts ? `${ownedCounts.contacts} contact${ownedCounts.contacts === 1 ? '' : 's'} and ${ownedCounts.deals} deal${ownedCounts.deals === 1 ? '' : 's'}` : 'records'}
              </strong>
              . Choose what happens to them:
            </p>
            <div className="mb-6 space-y-3">
              <label className="flex items-start gap-2 text-sm text-foreground cursor-pointer">
                <input
                  type="radio"
                  name="remove-strategy"
                  className="mt-0.5"
                  checked={removeStrategy === 'transfer'}
                  onChange={() => setRemoveStrategy('transfer')}
                />
                <span>Transfer them to another member</span>
              </label>
              {removeStrategy === 'transfer' && (
                <select
                  className="w-full px-3 py-2 bg-background border border-border rounded-lg text-foreground focus:outline-none focus:border-primary"
                  value={targetOwnerId}
                  onChange={e => setTargetOwnerId(e.target.value)}
                >
                  <option value="">-- Select member --</option>
                  {members.filter(m => m.user_id !== reassignModalUser.user_id && m.status === 'active').map(m => (
                    <option key={m.user_id} value={m.user_id}>{m.full_name || m.email}</option>
                  ))}
                </select>
              )}
              <label className="flex items-start gap-2 text-sm text-foreground cursor-pointer">
                <input
                  type="radio"
                  name="remove-strategy"
                  className="mt-0.5"
                  checked={removeStrategy === 'unassign'}
                  onChange={() => setRemoveStrategy('unassign')}
                />
                <span>
                  Leave them unassigned
                  <span className="block text-xs text-muted-foreground">The records stay in the workspace with no owner until someone picks them up.</span>
                </span>
              </label>
            </div>
            <div className="flex gap-2">
              <button
                onClick={() => setReassignModalUser(null)}
                className="flex-1 px-4 py-2 border border-border rounded-xl text-sm font-medium hover:bg-accent transition"
              >
                Cancel
              </button>
              <button
                disabled={removeStrategy === 'transfer' && !targetOwnerId}
                onClick={() => handleRemove(reassignModalUser.user_id,
                  removeStrategy === 'transfer'
                    ? { strategy: 'transfer', reassign_to_user_id: targetOwnerId }
                    : { strategy: 'unassign' })}
                className="flex-1 px-4 py-2 bg-red-500/20 text-red-500 border border-red-500/50 rounded-xl text-sm font-bold hover:bg-red-500/30 transition disabled:opacity-50"
              >
                {removeStrategy === 'transfer' ? 'Transfer & remove' : 'Remove member'}
              </button>
            </div>
          </div>
        </div>
      )}
      {confirmDialogEl}
    </>
  );
}
