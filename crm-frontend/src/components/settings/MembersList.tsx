import { useState, useEffect } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { getWorkspaceMembers, updateMemberRole, removeMember, suspendMember, reinstateMember, transferOwnership, sendMemberResetLink, listInvitations, resendInvitation, revokeInvitation, getRoleOptions, ReassignmentRequiredError, type WorkspaceMember, type Invitation, type RoleOption } from '../../lib/api';
import { useAuth, usePermissions } from '../../lib/auth';
import { prettyRole } from '../../lib/roles';
import { useConfirm } from '../common/ConfirmDialog';
import Modal from '../common/Modal';
import MemberDrawer from './MemberDrawer';
import { ShieldAlert, ShieldCheck, PauseCircle, PlayCircle, UserMinus, Crown, Shield, KeyRound, CheckCircle2, RotateCw, X, HelpCircle, Search, Check } from 'lucide-react';
import {
  Badge, Button, Input, Select, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';

export default function MembersList() {
  const { user, hasCapability, isOwner, refreshAuth } = useAuth();
  const { can } = usePermissions();
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [roles, setRoles] = useState<RoleOption[]>([]);
  const [loading, setLoading] = useState(true);

  // States for reassign modal
  const [reassignModalUser, setReassignModalUser] = useState<WorkspaceMember | null>(null);
  const [targetOwnerId, setTargetOwnerId] = useState<string>('');
  const [ownedCounts, setOwnedCounts] = useState<{ contacts: number; deals: number; custom: number } | null>(null);
  // Lead sources that were routing new leads to the member. Shown in the dialog
  // before removal and echoed after it, because this is the one consequence that
  // survives the offboarding: their records get a new owner, their lead pipes get
  // none until someone picks one.
  const [routingSources, setRoutingSources] = useState<string[]>([]);
  const [routingNotice, setRoutingNotice] = useState<string[]>([]);
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

  // Free-text search (name/email) + status filter are local component state
  // (transient, unlike the deep-linkable role filter). Drawer target is a
  // user_id or null.
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<'all' | 'active' | 'suspended' | 'invited'>('all');
  const [drawerUserId, setDrawerUserId] = useState<string | null>(null);
  const [transferTarget, setTransferTarget] = useState<WorkspaceMember | null>(null);
  const [transferConfirm, setTransferConfirm] = useState('');
  const [transferBusy, setTransferBusy] = useState(false);

  const canManage = hasCapability('members.manage');
  // Owner role ids (resolved from the dynamic options, P6) so the owner member is
  // shown as a locked badge — you transfer ownership, you don't re-assign it.
  const ownerRoleIds = new Set(roles.filter((r) => r.is_owner).map((r) => r.id));

  // Client-side filters: role (deep-linkable), status, and free-text over name +
  // email. An unknown role id (stale link) simply matches nothing — the
  // zero-result state below offers the "All roles" reset.
  const query = search.trim().toLowerCase();
  const visibleMembers = members.filter((m) => {
    if (roleFilter && m.role_id !== roleFilter) return false;
    if (statusFilter !== 'all' && m.status !== statusFilter) return false;
    if (query && !String(m.full_name ?? '').toLowerCase().includes(query) && !String(m.email ?? '').toLowerCase().includes(query)) return false;
    return true;
  });

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
      const result = await removeMember(userId, input);
      setReassignModalUser(null);
      setOwnedCounts(null);
      setRoutingSources([]);
      // Surfaced AFTER a successful removal too, and that is the point: a member who
      // owns no records never triggers the 409 at all, so a recently-added rep who is
      // on a rotation but has not closed anything yet — the commonest offboarding
      // there is — used to be removed in total silence while their lead sources went
      // ownerless. Nobody found out until the leads stopped being followed up.
      setRoutingNotice(result.routing_sources_cleared);
      fetchMembers();
    } catch (err: any) {
      // Keyed off the typed 409 code, not a message substring: the member still
      // owns records, so open the dialog with the real counts (U0.2).
      if (err instanceof ReassignmentRequiredError) {
        const mem = members.find(m => m.user_id === userId);
        if (mem) {
          setOwnedCounts(err.owned);
          setRoutingSources(err.routingSources);
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

  // Transfer ownership (U4): a type-to-confirm modal that names the consequences,
  // then refreshes the auth context (was a hard window.location.reload) so the
  // caller's demotion to Admin propagates without a full page reload.
  const submitTransfer = async () => {
    if (!transferTarget) return;
    setTransferBusy(true);
    setErrorMsg('');
    try {
      await transferOwnership(transferTarget.user_id);
      await refreshAuth();
      setTransferTarget(null);
      setTransferConfirm('');
      fetchMembers();
    } catch (err: any) {
      setErrorMsg(err.message);
    } finally {
      setTransferBusy(false);
    }
  };

  if (loading) {
    return <SpinnerBlock />;
  }

  return (
    <>
      <div>
        {errorMsg && (
          <div className="mb-4 flex items-center gap-2 rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
            <ShieldAlert className="w-4 h-4" />
            {errorMsg}
          </div>
        )}
        {noticeMsg && (
          <div className="mb-4 flex items-center gap-2 rounded-lg border border-emerald-500/20 bg-emerald-500/10 p-3 text-sm text-emerald-600 dark:text-emerald-400">
            <CheckCircle2 className="w-4 h-4" />
            {noticeMsg}
          </div>
        )}
        {/* Amber, not green: the removal succeeded, but it left lead sources with no
            owner, and that is an action item rather than a confirmation. Dismissible
            because it names sources the admin now has to go and fix — it should not
            vanish on the next re-render before they have read it. */}
        {routingNotice.length > 0 && (
          <div className="mb-4 flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm text-amber-800 dark:text-amber-300">
            <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0" />
            <span className="flex-1">
              They were the lead owner on <strong>{routingNotice.join(', ')}</strong>.
              Those sources now capture leads with no owner — pick a new owner in
              Settings → Integrations so the leads get followed up.
            </span>
            <button
              type="button"
              className="shrink-0 underline"
              onClick={() => setRoutingNotice([])}
            >
              Dismiss
            </button>
          </div>
        )}
        {/* Search + status filter (U4) — free-text over name/email and a status
            pill filter, alongside the deep-linkable role filter below. */}
        <div className="mb-3 flex items-center gap-2 flex-wrap">
          <div className="relative flex-1 min-w-[180px] max-w-xs">
            <Search aria-hidden className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search name or email"
              aria-label="Search members"
              className="pl-8"
            />
          </div>
          <Select
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value as typeof statusFilter)}
            aria-label="Filter by status"
            className="w-auto"
          >
            <option value="all">All statuses</option>
            <option value="active">Active</option>
            <option value="suspended">Suspended</option>
            <option value="invited">Invited</option>
          </Select>
        </div>
        {/* Role filter (U3.3) — the landing target for "N members" links on role
            cards/detail. URL-backed, so those deep links arrive pre-filtered. */}
        {roles.length > 0 && (
          <div className="mb-4 flex items-center gap-2 flex-wrap">
            <label htmlFor="member-role-filter" className="text-xs text-muted-foreground">
              Filter by role
            </label>
            <Select
              id="member-role-filter"
              value={roleFilter}
              onChange={(e) => setRoleFilter(e.target.value)}
              className="w-auto"
            >
              <option value="">All roles</option>
              {roles.map((r) => (
                <option key={r.id} value={r.id}>{prettyRole(r.name)}</option>
              ))}
            </Select>
            {hasCapability('roles.manage') && (
              <Link
                to={roleFilter ? `/settings/roles/${roleFilter}` : '/settings/roles'}
                className="text-xs text-primary hover:underline"
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
            <Badge variant="outline">
              Filtered by role
              <button
                type="button"
                onClick={() => setRoleFilter('')}
                aria-label="Clear role filter"
                className="rounded transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <X className="w-3 h-3" />
              </button>
            </Badge>
          </div>
        )}
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Member</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="hidden md:table-cell">Joined</TableHead>
                <TableHead className="hidden lg:table-cell">Last active</TableHead>
                <TableHead className="hidden sm:table-cell">Verified</TableHead>
                {/* Who has actually enrolled in 2FA (U6.4) — the column you need
                    before turning the workspace policy on. */}
                <TableHead className="hidden sm:table-cell">2FA</TableHead>
                {canManage && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
            {visibleMembers.map(m => (
              <TableRow key={m.user_id} className={m.status === 'suspended' ? 'opacity-60' : undefined}>
                <TableCell>
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
                        {canManage ? (
                          <button type="button" onClick={() => setDrawerUserId(m.user_id)} className="rounded text-left hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                            {m.full_name || `${m.first_name} ${m.last_name}`}
                          </button>
                        ) : (
                          <span>{m.full_name || `${m.first_name} ${m.last_name}`}</span>
                        )}
                        {m.user_id === user?.id && <Badge>You</Badge>}
                      </p>
                      <p className="text-xs text-muted-foreground">{m.email}</p>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  {canManage && !ownerRoleIds.has(m.role_id) && m.user_id !== user?.id && m.status !== 'deleted' ? (
                    <div className="flex items-center gap-1.5">
                      <Select
                        value={m.role_id}
                        onChange={e => handleRoleChange(m.user_id, e.target.value)}
                        className="w-auto"
                      >
                        {roles.map(r => (
                          <option key={r.id} value={r.id} disabled={r.is_owner}>
                            {prettyRole(r.name)}{r.is_owner ? ' — transfer instead' : ''}
                          </option>
                        ))}
                      </Select>
                      {/* Jump into the assigned role's capability list right where
                          it's assigned (U3.3) — replaces the near-invisible
                          title-tooltips on the options (U3.5). */}
                      {can('roles.manage') && (
                        <Link
                          to={`/settings/roles/${m.role_id}`}
                          aria-label="What does this role grant?"
                          className="text-muted-foreground transition-colors hover:text-primary"
                        >
                          <HelpCircle className="w-3.5 h-3.5" />
                        </Link>
                      )}
                    </div>
                  ) : (
                    <Badge variant="secondary">
                      {m.role === 'owner' && <Crown className="w-3 h-3 text-amber-500" />}
                      {m.role === 'admin' && <Shield className="w-3 h-3 text-primary" />}
                      {prettyRole(m.role)}
                    </Badge>
                  )}
                </TableCell>
                <TableCell>
                  <Badge
                    className="uppercase tracking-wider"
                    variant={
                      m.status === 'active' ? 'success'
                      : m.status === 'suspended' ? 'warning'
                      : m.status === 'invited' ? 'default'
                      : 'secondary'
                    }
                  >
                    {m.status}
                  </Badge>
                </TableCell>
                <TableCell className="text-xs text-muted-foreground hidden md:table-cell whitespace-nowrap">
                  {m.joined_at ? new Date(m.joined_at).toLocaleDateString() : '—'}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground hidden lg:table-cell whitespace-nowrap">
                  {m.last_active_at ? new Date(m.last_active_at).toLocaleDateString() : '—'}
                </TableCell>
                <TableCell className="hidden sm:table-cell">
                  {m.email_verified ? (
                    <Check className="w-4 h-4 text-emerald-600 dark:text-emerald-400" aria-label="Email verified" />
                  ) : (
                    <span className="text-xs text-amber-600 dark:text-amber-400" title="Email not verified">Pending</span>
                  )}
                </TableCell>
                <TableCell className="hidden sm:table-cell">
                  {m.two_factor_enabled ? (
                    <ShieldCheck className="w-4 h-4 text-emerald-600 dark:text-emerald-400" aria-label="Two-factor authentication on" />
                  ) : (
                    <span className="text-xs text-muted-foreground" title="Two-factor authentication not set up">Off</span>
                  )}
                </TableCell>
                {canManage && (
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      {m.user_id !== user?.id && m.status !== 'invited' && m.status !== 'deleted' && (
                        <Button variant="ghost" size="icon" onClick={() => handleSendReset(m)} title="Send password reset link" className="h-8 w-8 text-muted-foreground hover:text-foreground">
                          <KeyRound className="w-4 h-4" />
                        </Button>
                      )}
                      {m.user_id !== user?.id && !ownerRoleIds.has(m.role_id) && (
                        <>
                          {isOwner && m.status === 'active' && (
                            <Button variant="ghost" size="icon" onClick={() => { setTransferTarget(m); setTransferConfirm(''); }} title="Transfer Ownership" className="h-8 w-8 text-muted-foreground hover:text-foreground">
                              <Crown className="w-4 h-4" />
                            </Button>
                          )}
                          {m.status === 'active' && (
                            <Button variant="ghost" size="icon" onClick={() => handleSuspend(m.user_id)} title="Suspend Member" className="h-8 w-8 text-muted-foreground hover:text-foreground">
                              <PauseCircle className="w-4 h-4" />
                            </Button>
                          )}
                          {m.status === 'suspended' && (
                            <Button variant="ghost" size="icon" onClick={() => handleReinstate(m.user_id)} title="Reinstate Member" className="h-8 w-8 text-muted-foreground hover:text-foreground">
                              <PlayCircle className="w-4 h-4" />
                            </Button>
                          )}
                          <Button variant="ghost" size="icon" onClick={() => handleRemove(m.user_id)} title="Remove Member" className="h-8 w-8 text-muted-foreground hover:text-destructive">
                            <UserMinus className="w-4 h-4" />
                          </Button>
                        </>
                      )}
                    </div>
                  </TableCell>
                )}
              </TableRow>
            ))}
            </TableBody>
          </Table>
        </TableShell>
        {members.length === 0 ? (
          <p className="text-center text-muted-foreground py-8 text-sm">No members found.</p>
        ) : visibleMembers.length === 0 ? (
          <p className="text-center text-muted-foreground py-8 text-sm">
            No members hold this role.{' '}
            <button type="button" onClick={() => setRoleFilter('')} className="text-primary hover:underline">
              Show all roles
            </button>
          </p>
        ) : null}
      </div>

      {canManage && invitations.length > 0 && (
        <div className="mt-8">
          <h3 className="text-sm font-semibold text-foreground mb-3 flex items-center gap-2">
            Pending invitations
            <Badge>{invitations.length}</Badge>
          </h3>
          <TableShell>
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Email</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {invitations.map(inv => {
                  const expired = inv.status === 'expired';
                  return (
                  <TableRow key={inv.id}>
                    <TableCell className="text-sm text-foreground">
                      <span className="inline-flex items-center gap-2">
                        {inv.email}
                        {expired && <Badge variant="warning">Expired</Badge>}
                      </span>
                    </TableCell>
                    <TableCell>
                      <Badge variant="secondary">{prettyRole(inv.role)}</Badge>
                    </TableCell>
                    <TableCell className={`text-xs ${expired ? 'text-amber-600 dark:text-amber-400 font-medium' : 'text-muted-foreground'}`}>
                      {expired ? 'Expired — resend to renew' : new Date(inv.expires_at).toLocaleDateString()}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button variant="ghost" size="icon" onClick={() => handleResendInvite(inv)} title="Resend invitation" className="h-8 w-8 text-muted-foreground hover:text-foreground">
                          <RotateCw className="w-4 h-4" />
                        </Button>
                        <Button variant="ghost" size="icon" onClick={() => handleRevokeInvite(inv)} title="Revoke invitation" className="h-8 w-8 text-muted-foreground hover:text-destructive">
                          <X className="w-4 h-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </TableShell>
        </div>
      )}

      {/* Shared Radix modal (U7): Escape, focus trap/restore and aria for free. */}
      {reassignModalUser && (
        <Modal
          open
          onClose={() => setReassignModalUser(null)}
          title={`Remove ${reassignModalUser.first_name || reassignModalUser.email}`}
          size="sm"
        >
          <>
            <p className="text-sm text-muted-foreground mb-4">
              This member still owns{' '}
              <strong className="text-foreground">
                {ownedCounts
                  ? [
                      `${ownedCounts.contacts} contact${ownedCounts.contacts === 1 ? '' : 's'}`,
                      `${ownedCounts.deals} deal${ownedCounts.deals === 1 ? '' : 's'}`,
                      // Only when non-zero: naming "0 other records" on every removal
                      // is noise, but omitting a non-zero count under-reports the
                      // impact in the dialog where the admin actually decides.
                      ...(ownedCounts.custom > 0
                        ? [`${ownedCounts.custom} other record${ownedCounts.custom === 1 ? '' : 's'}`]
                        : []),
                    ].join(', ').replace(/, ([^,]*)$/, ' and $1')
                  : 'records'}
              </strong>
              . Choose what happens to them:
            </p>

            {/* Deliberately NOT a fourth strategy option. Who inherits the records
                they already own and who should receive the next lead a form captures
                are different decisions with different right answers; the binding is
                always cleared, and the admin picks a new owner per source afterwards. */}
            {routingSources.length > 0 && (
              <div className="mb-4 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-800 dark:text-amber-300">
                They are also the lead owner on{' '}
                <strong>{routingSources.join(', ')}</strong>. Removing them clears that,
                so those sources will capture leads with no owner until you pick someone
                new in Integrations.
              </div>
            )}
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
                <Select
                  value={targetOwnerId}
                  onChange={e => setTargetOwnerId(e.target.value)}
                >
                  <option value="">-- Select member --</option>
                  {members.filter(m => m.user_id !== reassignModalUser.user_id && m.status === 'active').map(m => (
                    <option key={m.user_id} value={m.user_id}>{m.full_name || m.email}</option>
                  ))}
                </Select>
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
              <Button variant="outline" onClick={() => setReassignModalUser(null)} className="flex-1">
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={removeStrategy === 'transfer' && !targetOwnerId}
                onClick={() => handleRemove(reassignModalUser.user_id,
                  removeStrategy === 'transfer'
                    ? { strategy: 'transfer', reassign_to_user_id: targetOwnerId }
                    : { strategy: 'unassign' })}
                className="flex-1"
              >
                {removeStrategy === 'transfer' ? 'Transfer & remove' : 'Remove member'}
              </Button>
            </div>
          </>
        </Modal>
      )}
      {drawerUserId && (
        <MemberDrawer
          userId={drawerUserId}
          isSelf={drawerUserId === user?.id}
          canManage={canManage}
          onClose={() => setDrawerUserId(null)}
          onChanged={fetchMembers}
        />
      )}

      {/* Transfer ownership — type-to-confirm (U4). Dismissal is blocked mid-transfer. */}
      {transferTarget && (() => {
        const targetName = transferTarget.full_name || transferTarget.email;
        return (
          <Modal
            open
            onClose={() => setTransferTarget(null)}
            title={<span className="flex items-center gap-2"><Crown className="w-5 h-5 text-amber-500" /> Transfer ownership</span>}
            size="md"
            dismissable={!transferBusy}
          >
            <>
              <p className="text-sm text-muted-foreground mb-4">
                <strong className="text-foreground">{targetName}</strong> will become the workspace <strong>Owner</strong> with full control.
                You'll be demoted to <strong>Admin</strong>, and only the new owner can transfer it back.
              </p>
              <label className="block text-xs font-medium mb-1.5">
                Type <strong className="text-foreground">{targetName}</strong> to confirm
              </label>
              {/* No autoFocus: it lands before Modal captures the element to
                  restore focus to on close, which breaks the restore. */}
              <Input
                value={transferConfirm}
                onChange={(e) => setTransferConfirm(e.target.value)}
                aria-label="Type the new owner's name to confirm"
                className="mb-4"
              />
              <div className="flex gap-2 justify-end">
                <Button variant="outline" onClick={() => setTransferTarget(null)} disabled={transferBusy}>
                  Cancel
                </Button>
                <Button onClick={submitTransfer} disabled={transferBusy || transferConfirm !== targetName}>
                  {transferBusy ? 'Transferring…' : 'Transfer ownership'}
                </Button>
              </div>
            </>
          </Modal>
        );
      })()}
      {confirmDialogEl}
    </>
  );
}
