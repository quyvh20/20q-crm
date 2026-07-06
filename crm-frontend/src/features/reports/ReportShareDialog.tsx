import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  listReportShares, addReportShare, removeReportShare, updateReport,
  getWorkspaceMembers, getRoles, listGroups,
  type Report, type ReportShareView, type ShareTargetType, type ShareLevel,
  type ReportVisibility, type WorkspaceMember, type RoleDetail, type UserGroup,
} from '../../lib/api';
import { useAuth } from '../../lib/auth';

// ReportShareDialog manages a report's granular share list: grant a user, role,
// or group access at view/comment/edit. Shown only to a caller who can 'manage'
// the report.
const TARGET_TABS: { type: ShareTargetType; label: string }[] = [
  { type: 'user', label: 'People' },
  { type: 'role', label: 'Roles' },
  { type: 'group', label: 'Groups' },
];
const LEVELS: { value: ShareLevel; label: string }[] = [
  { value: 'view', label: 'Can view' },
  { value: 'comment', label: 'Can comment' },
  { value: 'edit', label: 'Can edit' },
];
const TYPE_ICON: Record<ShareTargetType, string> = { user: '👤', role: '🛡️', group: '👥' };

export default function ReportShareDialog({ report, onClose }: { report: Report; onClose: () => void }) {
  const { user } = useAuth();
  const [shares, setShares] = useState<ReportShareView[]>([]);
  const [visibility, setVisibility] = useState<ReportVisibility>(report.visibility);
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [roles, setRoles] = useState<RoleDetail[]>([]);
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [tab, setTab] = useState<ShareTargetType>('user');
  const [selected, setSelected] = useState('');
  const [level, setLevel] = useState<ShareLevel>('view');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const [s, m, r, g] = await Promise.all([listReportShares(report.id), getWorkspaceMembers(), getRoles(), listGroups()]);
      setShares(s);
      setMembers(m.filter((x) => x.status === 'active'));
      setRoles(r);
      setGroups(g);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load sharing');
    } finally {
      setLoading(false);
    }
  }, [report.id]);
  useEffect(() => { load(); }, [load]);

  // General access ("Private" vs "Anyone in the workspace") is stored on the
  // report itself, so changing it re-saves the report through the normal
  // update endpoint (requires ≥edit — the Share button only shows to managers).
  const changeVisibility = async (v: ReportVisibility) => {
    setBusy(true); setError('');
    try {
      await updateReport(report.id, {
        name: report.name, description: report.description,
        object_slug: report.object_slug, visibility: v, config: report.config,
      });
      setVisibility(v);
    } catch (e) { setError(e instanceof Error ? e.message : 'Failed to update access'); }
    finally { setBusy(false); }
  };

  // Candidates for the active tab, excluding already-shared targets. The current
  // user is never a People candidate: the owner/manager already holds full
  // access, so sharing to oneself is meaningless (and rejected by the backend).
  const sharedIds = useMemo(() => new Set(shares.map((s) => s.target_id)), [shares]);
  const candidates = useMemo(() => {
    if (tab === 'user') return members.filter((m) => !sharedIds.has(m.user_id) && m.user_id !== user?.id).map((m) => ({ id: m.user_id, name: m.full_name || m.email }));
    if (tab === 'role') return roles.filter((r) => !sharedIds.has(r.id)).map((r) => ({ id: r.id, name: r.name }));
    return groups.filter((g) => !sharedIds.has(g.id)).map((g) => ({ id: g.id, name: g.name }));
  }, [tab, members, roles, groups, sharedIds, user?.id]);

  const add = async () => {
    if (!selected) return;
    setBusy(true); setError('');
    try {
      await addReportShare(report.id, tab, selected, level);
      setSelected('');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to share');
    } finally { setBusy(false); }
  };

  const changeLevel = async (s: ReportShareView, next: ShareLevel) => {
    setBusy(true); setError('');
    try { await addReportShare(report.id, s.target_type, s.target_id, next); await load(); }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to update'); }
    finally { setBusy(false); }
  };

  const remove = async (shareId: string) => {
    setBusy(true); setError('');
    try { await removeReportShare(report.id, shareId); setShares((c) => c.filter((s) => s.id !== shareId)); }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to remove'); }
    finally { setBusy(false); }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div className="w-full max-w-lg rounded-2xl border bg-card p-5 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Share report</h2>
          <button onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-accent" aria-label="Close">✕</button>
        </div>

        {error && <div className="mb-3 text-sm text-red-600">{error}</div>}

        {/* General access */}
        <div className="mb-4">
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">General access</div>
          <select
            aria-label="General access"
            value={visibility}
            onChange={(e) => changeVisibility(e.target.value as ReportVisibility)}
            disabled={busy}
            className="w-full rounded-md border bg-background px-2 py-2 text-sm"
          >
            <option value="private">🔒 Private — only people added here</option>
            <option value="org">🌐 Anyone in the workspace can view</option>
          </select>
        </div>

        {/* Add a share */}
        <div className="space-y-2 rounded-xl border p-3">
          <div className="flex gap-1">
            {TARGET_TABS.map((t) => (
              <button
                key={t.type}
                onClick={() => { setTab(t.type); setSelected(''); }}
                className={`rounded-md px-3 py-1.5 text-sm ${tab === t.type ? 'bg-primary text-primary-foreground' : 'hover:bg-accent'}`}
              >
                {t.label}
              </button>
            ))}
          </div>
          <div className="flex gap-2">
            <select aria-label="Share target" value={selected} onChange={(e) => setSelected(e.target.value)} className="flex-1 rounded-md border bg-background px-2 py-2 text-sm">
              <option value="">{candidates.length ? `Choose a ${tab}…` : `No ${tab}s to add`}</option>
              {candidates.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
            <select aria-label="Access level" value={level} onChange={(e) => setLevel(e.target.value as ShareLevel)} className="w-28 rounded-md border bg-background px-2 py-2 text-sm">
              {LEVELS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
            </select>
            <button onClick={add} disabled={busy || !selected} className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50">Add</button>
          </div>
        </div>

        {/* Current shares */}
        <div className="mt-4">
          <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Shared with</div>
          {loading ? (
            <div className="h-16 animate-pulse rounded-lg bg-muted/50" />
          ) : shares.length === 0 ? (
            <div className="rounded-lg border border-dashed p-4 text-center text-sm text-muted-foreground">Not shared with anyone yet.</div>
          ) : (
            <div className="max-h-64 space-y-1 overflow-auto">
              {shares.map((s) => (
                <div key={s.id} className="flex items-center gap-2 rounded-md border px-3 py-2 text-sm">
                  <span>{TYPE_ICON[s.target_type]}</span>
                  <span className="flex-1 truncate">{s.target_name}</span>
                  <select
                    aria-label={`Level for ${s.target_name}`}
                    value={s.level}
                    onChange={(e) => changeLevel(s, e.target.value as ShareLevel)}
                    disabled={busy}
                    className="rounded border bg-background px-1.5 py-1 text-xs"
                  >
                    {LEVELS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
                  </select>
                  <button onClick={() => remove(s.id)} disabled={busy} className="rounded px-1.5 py-1 text-muted-foreground hover:bg-accent hover:text-foreground" aria-label={`Remove ${s.target_name}`}>✕</button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
