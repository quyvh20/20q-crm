import { useState, useEffect, useCallback } from 'react';
import {
  getRecordShares,
  shareRecord,
  unshareRecord,
  getUsers,
  type RecordShareView,
  type UserListItem,
} from '../../lib/api';

// ShareRecordModal grants a specific record to individual members — the escape
// hatch (P3, I2) that lets an 'own'-scoped role reach a record it doesn't own.
// The backend enforces that the sharer can see the record (own/all scope) and the
// grantee is an active member, so this UI just lists + edits the grants.
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
  const [shares, setShares] = useState<RecordShareView[]>([]);
  const [users, setUsers] = useState<UserListItem[]>([]);
  const [selected, setSelected] = useState('');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      setLoading(true);
      const [s, u] = await Promise.all([getRecordShares(slug, recordId), getUsers()]);
      setShares(s);
      setUsers(u);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load shares');
    } finally {
      setLoading(false);
    }
  }, [slug, recordId]);

  useEffect(() => { load(); }, [load]);

  const sharedIds = new Set(shares.map((s) => s.grantee_user_id));
  const candidates = users.filter((u) => !sharedIds.has(u.id));

  const add = async () => {
    if (!selected) return;
    setBusy(true);
    setError('');
    try {
      await shareRecord(slug, recordId, selected);
      setSelected('');
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to share');
    } finally {
      setBusy(false);
    }
  };

  const remove = async (shareId: string) => {
    setBusy(true);
    setError('');
    try {
      await unshareRecord(slug, recordId, shareId);
      setShares((cur) => cur.filter((s) => s.id !== shareId));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to revoke');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', zIndex: 60, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div style={{ background: '#fff', borderRadius: 12, width: 460, maxWidth: '90vw', overflow: 'hidden' }}>
        <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0' }}>
          <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>Share “{recordName}”</h3>
          <p style={{ margin: '4px 0 0', fontSize: 13, color: '#64748b' }}>
            Grant specific members access to this record even when their role only sees their own.
          </p>
        </div>

        <div style={{ padding: 24 }}>
          {error && (
            <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 16, fontSize: 13 }}>{error}</div>
          )}

          {/* Add a grantee */}
          <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
            <select
              value={selected}
              onChange={(e) => setSelected(e.target.value)}
              disabled={busy || loading}
              style={{ flex: 1, padding: '8px 10px', border: '1px solid #e2e8f0', borderRadius: 6, fontSize: 14 }}
            >
              <option value="">Select a member…</option>
              {candidates.map((u) => (
                <option key={u.id} value={u.id}>
                  {[u.first_name, u.last_name].filter(Boolean).join(' ') || u.email}
                </option>
              ))}
            </select>
            <button
              onClick={add}
              disabled={!selected || busy}
              style={{ padding: '8px 16px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600, fontSize: 14, opacity: !selected || busy ? 0.5 : 1 }}
            >
              Share
            </button>
          </div>

          {/* Current shares */}
          {loading ? (
            <div style={{ fontSize: 13, color: '#94a3b8' }}>Loading…</div>
          ) : shares.length === 0 ? (
            <div style={{ fontSize: 13, color: '#94a3b8' }}>Not shared with anyone yet.</div>
          ) : (
            <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 6 }}>
              {shares.map((s) => (
                <li key={s.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '8px 10px', border: '1px solid #e2e8f0', borderRadius: 6 }}>
                  <span style={{ fontSize: 14 }}>{s.grantee_name || s.grantee_user_id}</span>
                  <button onClick={() => remove(s.id)} disabled={busy} style={{ fontSize: 12, color: '#dc2626', background: 'none', border: 'none', cursor: 'pointer' }}>
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={{ padding: '8px 16px', background: '#f1f5f9', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Done</button>
        </div>
      </div>
    </div>
  );
}
