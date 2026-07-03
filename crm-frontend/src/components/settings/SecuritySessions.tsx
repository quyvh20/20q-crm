import { useCallback, useEffect, useState } from 'react';
import { getSessions, revokeSession, signOutEverywhere, type UserSession } from '../../lib/api';

function relativeTime(iso?: string): string {
  if (!iso) return '—';
  const then = new Date(iso).getTime();
  const diff = Date.now() - then;
  const mins = Math.round(diff / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins} min ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs} hr ago`;
  const days = Math.round(hrs / 24);
  return `${days} day${days === 1 ? '' : 's'} ago`;
}

export default function SecuritySessions() {
  const [sessions, setSessions] = useState<UserSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [signingOut, setSigningOut] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setSessions(await getSessions());
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load sessions');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const handleRevoke = async (id: string) => {
    if (!confirm('Sign this device out? It will need to sign in again.')) return;
    setBusyId(id);
    try {
      await revokeSession(id);
      setSessions((prev) => prev.filter((s) => s.id !== id));
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to revoke session');
    } finally {
      setBusyId(null);
    }
  };

  const handleSignOutAll = async () => {
    if (!confirm('Sign out of every other device? This keeps you signed in here.')) return;
    setSigningOut(true);
    try {
      await signOutEverywhere();
      await load();
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Failed to sign out other devices');
    } finally {
      setSigningOut(false);
    }
  };

  const others = sessions.filter((s) => !s.current);

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">Active Sessions</h2>
        <p className="text-sm text-muted-foreground mt-0.5">
          Devices currently signed in to your account. Revoke any you don't recognize.
        </p>
      </div>

      {error ? (
        <div className="rounded-md border border-red-500/40 bg-red-500/10 p-4 text-sm text-red-400">{error}</div>
      ) : loading ? (
        <div className="flex items-center justify-center py-12">
          <div className="h-6 w-6 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      ) : sessions.length === 0 ? (
        <div className="rounded-md border border-border py-12 text-center text-sm text-muted-foreground">
          No active sessions.
        </div>
      ) : (
        <div className="space-y-2">
          {sessions.map((s) => (
            <div
              key={s.id}
              className="flex items-center justify-between rounded-lg border border-border p-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-foreground">{s.device_label || 'Unknown device'}</span>
                  {s.current && (
                    <span className="rounded bg-emerald-500/15 px-2 py-0.5 text-xs font-medium text-emerald-400">
                      This device
                    </span>
                  )}
                </div>
                <div className="mt-0.5 text-xs text-muted-foreground">
                  {s.ip || 'unknown IP'} · last active {relativeTime(s.last_used_at || s.created_at)}
                </div>
              </div>
              {!s.current && (
                <button
                  onClick={() => handleRevoke(s.id)}
                  disabled={busyId === s.id}
                  className="rounded-md border border-border px-3 py-1.5 text-sm text-red-400 hover:bg-red-500/10 disabled:opacity-50"
                >
                  {busyId === s.id ? 'Revoking…' : 'Revoke'}
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      {others.length > 0 && (
        <div className="pt-2">
          <button
            onClick={handleSignOutAll}
            disabled={signingOut}
            className="rounded-md border border-red-500/50 bg-red-500/10 px-4 py-2 text-sm font-medium text-red-400 hover:bg-red-500/20 disabled:opacity-50"
          >
            {signingOut ? 'Signing out…' : 'Sign out all other devices'}
          </button>
          <p className="mt-1.5 text-xs text-muted-foreground">
            Ends every session except this one and invalidates their access immediately.
          </p>
        </div>
      )}
    </div>
  );
}
