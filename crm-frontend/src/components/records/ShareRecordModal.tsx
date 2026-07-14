import { useCallback, useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  getRecordShares, shareRecord, unshareRecord,
  getWorkspaceMembers, getRoleOptions, listGroups,
  type RecordShareView, type RecordShareLevel, type ShareTargetType,
  type WorkspaceMember, type RoleOption, type UserGroup,
} from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { prettyRole } from '../../lib/roles';

// ShareRecordModal grants ONE record to a user, role or group at view/edit (U6)
// — record sharing at parity with report sharing (ReportShareDialog), minus the
// 'comment' level, which records don't have. It is the escape hatch that lets an
// 'own'/'team'-scoped role reach a record outside its scope.
const TARGET_TABS: { type: ShareTargetType; label: string }[] = [
  { type: 'user', label: 'People' },
  { type: 'role', label: 'Roles' },
  { type: 'group', label: 'Groups' },
];
const LEVELS: { value: RecordShareLevel; label: string }[] = [
  { value: 'view', label: 'Can view' },
  { value: 'edit', label: 'Can edit' },
];
const TYPE_ICON: Record<ShareTargetType, string> = { user: '👤', role: '🛡️', group: '👥' };

export default function ShareRecordModal({
  slug,
  recordId,
  recordName,
  onClose,
}: {
  slug: string;
  recordId: string;
  recordName: string;
  onClose: () => void;
}) {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const [shares, setShares] = useState<RecordShareView[]>([]);
  const [members, setMembers] = useState<WorkspaceMember[]>([]);
  const [roles, setRoles] = useState<RoleOption[]>([]);
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [tab, setTab] = useState<ShareTargetType>('user');
  const [selected, setSelected] = useState('');
  const [level, setLevel] = useState<RecordShareLevel>('view');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    // allSettled: each picker source loads independently, so one 403/failure
    // (e.g. a member who can't list roles) can't blank the whole dialog. The
    // current share list is the only must-have; the rest degrade to empty pickers.
    const [s, m, r, g] = await Promise.allSettled([
      getRecordShares(slug, recordId), getWorkspaceMembers(), getRoleOptions(), listGroups(),
    ]);
    if (s.status === 'fulfilled') setShares(s.value);
    else setError(s.reason instanceof Error ? s.reason.message : 'Failed to load sharing');
    setMembers(m.status === 'fulfilled' ? m.value.filter((x) => x.status === 'active') : []);
    setRoles(r.status === 'fulfilled' ? r.value : []);
    setGroups(g.status === 'fulfilled' ? g.value : []);
    setLoading(false);
  }, [slug, recordId]);
  useEffect(() => { load(); }, [load]);

  // Candidates for the active tab, minus already-shared targets. The current
  // user is never a People candidate: sharing to yourself is meaningless (and
  // the server rejects it, as it does sharing to the record's own owner).
  const sharedIds = useMemo(() => new Set(shares.map((s) => s.target_id)), [shares]);
  const candidates = useMemo(() => {
    if (tab === 'user') {
      return members
        .filter((m) => !sharedIds.has(m.user_id) && m.user_id !== user?.id)
        .map((m) => ({ id: m.user_id, name: m.full_name || `${m.first_name} ${m.last_name}`.trim() || m.email }));
    }
    if (tab === 'role') return roles.filter((r) => !sharedIds.has(r.id)).map((r) => ({ id: r.id, name: prettyRole(r.name) }));
    return groups.filter((g) => !sharedIds.has(g.id)).map((g) => ({ id: g.id, name: g.name }));
  }, [tab, members, roles, groups, sharedIds, user?.id]);

  // The shared-with-me list of every OTHER member changes on a grant/revoke, and
  // this record's own page may re-read its shares — invalidate both.
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['shared-with-me'] });
    queryClient.invalidateQueries({ queryKey: ['record-shares', slug, recordId] });
  };

  const add = async () => {
    if (!selected) return;
    setBusy(true); setError('');
    try {
      await shareRecord(slug, recordId, tab, selected, level);
      setSelected('');
      await load();
      invalidate();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to share');
    } finally { setBusy(false); }
  };

  // Re-sharing the same target upserts its level server-side, so changing a level
  // is just another share call.
  const changeLevel = async (s: RecordShareView, next: RecordShareLevel) => {
    setBusy(true); setError('');
    try { await shareRecord(slug, recordId, s.target_type, s.target_id, next); await load(); invalidate(); }
    catch (e) { setError(e instanceof Error ? e.message : 'Failed to update'); }
    finally { setBusy(false); }
  };

  const remove = async (shareId: string) => {
    setBusy(true); setError('');
    try {
      await unshareRecord(slug, recordId, shareId);
      setShares((cur) => cur.filter((s) => s.id !== shareId));
      invalidate();
    } catch (e) { setError(e instanceof Error ? e.message : 'Failed to remove'); }
    finally { setBusy(false); }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <div className="w-full max-w-lg rounded-2xl border bg-card p-5 text-card-foreground shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="mb-1 flex items-center justify-between gap-3">
          <h2 className="truncate text-lg font-semibold">Share “{recordName}”</h2>
          <button onClick={onClose} className="rounded p-1 text-muted-foreground hover:bg-accent" aria-label="Close">✕</button>
        </div>
        <p className="mb-4 text-sm text-muted-foreground">
          Give specific people, roles or groups access to this record — even when their role only sees their own.
        </p>

        {error && <div className="mb-3 text-sm text-red-600 dark:text-red-400">{error}</div>}

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
            <select
              aria-label="Share target"
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
              disabled={busy || loading}
              className="flex-1 rounded-md border bg-background px-2 py-2 text-sm"
            >
              <option value="">{candidates.length ? `Choose a ${tab}…` : `No ${tab}s to add`}</option>
              {candidates.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
            <select
              aria-label="Access level"
              value={level}
              onChange={(e) => setLevel(e.target.value as RecordShareLevel)}
              className="w-28 rounded-md border bg-background px-2 py-2 text-sm"
            >
              {LEVELS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
            </select>
            <button
              onClick={add}
              disabled={busy || !selected}
              className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
            >
              Add
            </button>
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
                    onChange={(e) => changeLevel(s, e.target.value as RecordShareLevel)}
                    disabled={busy}
                    className="rounded border bg-background px-1.5 py-1 text-xs"
                  >
                    {LEVELS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
                  </select>
                  <button
                    onClick={() => remove(s.id)}
                    disabled={busy}
                    className="rounded px-1.5 py-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                    aria-label={`Remove ${s.target_name}`}
                  >
                    ✕
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

        <div className="mt-4 flex justify-end">
          <button onClick={onClose} className="rounded-md border px-4 py-2 text-sm font-medium hover:bg-accent">Done</button>
        </div>
      </div>
    </div>
  );
}
