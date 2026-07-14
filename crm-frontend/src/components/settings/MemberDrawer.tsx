import { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { X, Monitor, LogOut, Users2, FileText, Loader2, ShieldCheck, ShieldAlert } from 'lucide-react';
import { getMemberDetail, forceSignOutMember, type MemberDetail } from '../../lib/api';
import { prettyRole } from '../../lib/roles';
import { useConfirm } from '../common/ConfirmDialog';

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

  // Close on Escape — matches the rest of the settings dialogs.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

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

  const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString() : '—');
  const m = detail?.member;

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />
      <div
        role="dialog"
        aria-label="Member details"
        className="relative w-full max-w-md bg-card border-l border-border shadow-2xl h-full overflow-y-auto animate-in slide-in-from-right duration-200"
      >
        <div className="sticky top-0 bg-card border-b border-border px-5 py-4 flex items-center justify-between z-10">
          <h2 className="text-base font-semibold text-foreground">Member details</h2>
          <button onClick={onClose} aria-label="Close" className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="w-5 h-5" />
          </button>
        </div>

        {loading ? (
          <div className="flex justify-center py-20"><Loader2 className="w-7 h-7 animate-spin text-muted-foreground" /></div>
        ) : error && !detail ? (
          <div className="m-5 bg-red-500/10 text-red-500 text-sm rounded-lg px-3 py-2">{error}</div>
        ) : m ? (
          <div className="p-5 space-y-6">
            {error && <div className="bg-red-500/10 text-red-500 text-sm rounded-lg px-3 py-2">{error}</div>}

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
            </dl>

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
      </div>
      {dialog}
    </div>
  );
}
