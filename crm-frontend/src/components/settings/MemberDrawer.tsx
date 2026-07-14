import { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { Monitor, LogOut, Users2, FileText, Loader2, ShieldCheck, ShieldAlert, ShieldOff } from 'lucide-react';
import { getMemberDetail, forceSignOutMember, resetMemberTwoFactor, type MemberDetail } from '../../lib/api';
import { prettyRole } from '../../lib/roles';
import { useConfirm } from '../common/ConfirmDialog';
import Modal from '../common/Modal';

// MemberDrawer is the per-member detail slide-over (U4): role, groups, the
// records they own (the offboarding preview), and their live sessions with an
// admin force-sign-out. Opened from a row in the members table.
export default function MemberDrawer({
  userId,
  isSelf,
  canManage,
  onClose,
  onChanged,
}: {
  userId: string;
  isSelf: boolean;
  canManage: boolean;
  onClose: () => void;
  // Called after a force-sign-out so the list can refetch (status/last-active).
  onChanged?: () => void;
}) {
  const [detail, setDetail] = useState<MemberDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [signingOut, setSigningOut] = useState(false);
  const [resetting2FA, setResetting2FA] = useState(false);
  const [notice, setNotice] = useState('');
  const { confirm, dialog } = useConfirm();

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      setDetail(await getMemberDetail(userId));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load member');
    } finally {
      setLoading(false);
    }
  }, [userId]);

  useEffect(() => { load(); }, [load]);

  const handleForceSignOut = async () => {
    if (!detail) return;
    const name = detail.member.full_name || detail.member.email;
    if (!(await confirm({
      title: 'Sign out everywhere?',
      body: `${name} will be signed out of every device and app immediately and will need to sign in again. This does not remove them from the workspace.`,
      confirmLabel: 'Sign out everywhere',
      tone: 'danger',
    }))) return;
    setSigningOut(true);
    setError('');
    try {
      await forceSignOutMember(userId);
      await load();
      onChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to sign out member');
    } finally {
      setSigningOut(false);
    }
  };

  // Admin break-glass (U6.4): reset a member's 2FA when they've lost BOTH their
  // authenticator and their backup codes. Without it, a workspace 2FA policy is a
  // one-way door with no recovery path — so it's deliberately loud.
  const handleReset2FA = async () => {
    if (!detail) return;
    const name = detail.member.full_name || detail.member.email;
    if (!(await confirm({
      title: 'Reset two-factor authentication?',
      body: `${name}'s authenticator and backup codes are wiped, and they sign in with their password alone until they set it up again. Only do this once you're sure it's really them asking.`,
      confirmLabel: 'Reset it',
      tone: 'danger',
    }))) return;
    setResetting2FA(true);
    setError('');
    setNotice('');
    try {
      await resetMemberTwoFactor(userId);
      setNotice(`Two-factor authentication reset for ${name}.`);
      await load();
      onChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to reset the member's two-factor authentication");
    } finally {
      setResetting2FA(false);
    }
  };

  const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '—');
  const m = detail?.member;

  return (
    // Shared Radix modal (U7), drawer variant: Escape, focus trap/restore and
    // aria for free. The body owns its own p-5/m-5 spacing, hence padded={false}.
    // Dismissal is blocked mid-mutation, but NOT during the initial load — you
    // could always close a loading drawer, and taking that away is a regression.
    <>
      <Modal
        open
        onClose={onClose}
        title="Member details"
        variant="drawer"
        size="md"
        padded={false}
        dismissable={!signingOut && !resetting2FA}
      >
        {loading ? (
          <div className="flex justify-center py-20"><Loader2 className="w-7 h-7 animate-spin text-muted-foreground" /></div>
        ) : error && !detail ? (
          <div className="m-5 bg-red-500/10 text-red-500 text-sm rounded-lg px-3 py-2">{error}</div>
        ) : m ? (
          <div className="p-5 space-y-6">
            {error && <div className="bg-red-500/10 text-red-500 text-sm rounded-lg px-3 py-2">{error}</div>}
            {notice && <div className="bg-green-500/10 text-green-500 text-sm rounded-lg px-3 py-2">{notice}</div>}

            {/* Identity */}
            <div className="flex items-center gap-3">
              {m.avatar_url ? (
                <img src={m.avatar_url} alt="" className="h-12 w-12 rounded-full object-cover" />
              ) : (
                <div className="h-12 w-12 rounded-full bg-primary/10 flex items-center justify-center text-sm font-semibold text-primary">
                  {m.first_name?.[0]}{m.last_name?.[0]}
                </div>
              )}
              <div className="min-w-0">
                <p className="text-sm font-semibold text-foreground truncate">{m.full_name || `${m.first_name} ${m.last_name}`}</p>
                <p className="text-xs text-muted-foreground truncate">{m.email}</p>
              </div>
            </div>

            {/* Facts */}
            <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
              <div>
                <dt className="text-xs text-muted-foreground">Role</dt>
                <dd className="text-foreground">
                  <Link to={`/settings/roles/${m.role_id}`} className="text-blue-500 hover:underline">{prettyRole(m.role)}</Link>
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Status</dt>
                <dd className="text-foreground capitalize">{m.status}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Joined</dt>
                <dd className="text-foreground">{m.joined_at ? new Date(m.joined_at).toLocaleDateString() : '—'}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Email</dt>
                <dd className="flex items-center gap-1.5">
                  {m.email_verified ? (
                    <span className="inline-flex items-center gap-1 text-green-500"><ShieldCheck className="w-3.5 h-3.5" /> Verified</span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-amber-500"><ShieldAlert className="w-3.5 h-3.5" /> Unverified</span>
                  )}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Two-factor</dt>
                <dd className="flex items-center gap-1.5">
                  {m.two_factor_enabled ? (
                    <span className="inline-flex items-center gap-1 text-green-500"><ShieldCheck className="w-3.5 h-3.5" /> On</span>
                  ) : (
                    <span className="inline-flex items-center gap-1 text-muted-foreground"><ShieldOff className="w-3.5 h-3.5" /> Off</span>
                  )}
                </dd>
              </div>
            </dl>

            {/* 2FA break-glass (U6.4) — only meaningful when they actually have it on. */}
            {canManage && !isSelf && m.two_factor_enabled && (
              <div className="rounded-lg border border-border p-3 flex items-start justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-foreground">Lost their authenticator?</p>
                  <p className="text-xs text-muted-foreground mt-0.5">
                    Reset their two-factor authentication so they can sign in and set it up again.
                  </p>
                </div>
                <button
                  onClick={handleReset2FA}
                  disabled={resetting2FA}
                  className="shrink-0 inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-red-500 hover:bg-red-500/10 disabled:opacity-50 transition-colors"
                >
                  <ShieldOff className="w-3.5 h-3.5" /> {resetting2FA ? 'Resetting…' : 'Reset 2FA'}
                </button>
              </div>
            )}

            {/* Groups */}
            <div>
              <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2 flex items-center gap-1.5">
                <Users2 className="w-3.5 h-3.5" /> Groups
              </h3>
              {detail.groups.length === 0 ? (
                <p className="text-sm text-muted-foreground">Not in any groups.</p>
              ) : (
                <div className="flex flex-wrap gap-1.5">
                  {detail.groups.map(g => (
                    <span key={g.id} className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-muted text-muted-foreground border border-border">
                      {g.name}
                    </span>
                  ))}
                </div>
              )}
            </div>

            {/* Owned records */}
            <div>
              <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2 flex items-center gap-1.5">
                <FileText className="w-3.5 h-3.5" /> Owns
              </h3>
              <p className="text-sm text-foreground">
                {detail.owned_contacts} contact{detail.owned_contacts === 1 ? '' : 's'} · {detail.owned_deals} deal{detail.owned_deals === 1 ? '' : 's'}
              </p>
            </div>

            {/* Sessions */}
            <div>
              <div className="flex items-center justify-between mb-2">
                <h3 className="text-xs font-medium text-muted-foreground uppercase tracking-wider flex items-center gap-1.5">
                  <Monitor className="w-3.5 h-3.5" /> Sessions
                </h3>
                {canManage && !isSelf && detail.sessions.length > 0 && (
                  <button
                    onClick={handleForceSignOut}
                    disabled={signingOut}
                    className="inline-flex items-center gap-1 text-xs font-medium text-red-500 hover:text-red-600 disabled:opacity-50 transition-colors"
                  >
                    <LogOut className="w-3.5 h-3.5" /> Sign out everywhere
                  </button>
                )}
              </div>
              {detail.sessions.length === 0 ? (
                <p className="text-sm text-muted-foreground">No active sessions.</p>
              ) : (
                <ul className="space-y-2">
                  {detail.sessions.map(s => (
                    <li key={s.id} className="text-xs text-muted-foreground bg-muted/40 rounded-lg px-3 py-2">
                      <div className="text-foreground font-medium">{s.device_label || 'Unknown device'}</div>
                      <div>{s.ip || 'unknown IP'} · last active {fmt(s.last_used_at || s.created_at)}</div>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            {/* Audit trail link */}
            <Link
              to={`/settings/audit?user=${userId}`}
              className="inline-flex items-center gap-1.5 text-sm text-blue-500 hover:underline"
            >
              <FileText className="w-4 h-4" /> View activity in the audit log
            </Link>
          </div>
        ) : null}
      </Modal>
      {/* Sibling, not a child: the confirm is its own Radix layer, and Radix's
          layer stack keeps Escape/outside-click aimed at whichever is on top. */}
      {dialog}
    </>
  );
}
