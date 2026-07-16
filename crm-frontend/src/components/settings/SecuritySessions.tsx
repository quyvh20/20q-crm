import { useCallback, useEffect, useState } from 'react';
import { Monitor } from 'lucide-react';
import { getSessions, revokeSession, signOutEverywhere, type UserSession } from '../../lib/api';
import { useConfirm } from '../common/ConfirmDialog';
import { Badge, Button, EmptyState, SpinnerBlock } from '@/components/ui';

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
  // Load failures replace the list (nothing to show); ACTION failures render
  // above it so the list stays visible and retryable.
  const [actionError, setActionError] = useState<string | null>(null);
  const { confirm, dialog } = useConfirm();

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
    if (!(await confirm({ title: 'Revoke session', body: 'Sign this device out? It will need to sign in again.', confirmLabel: 'Sign it out' }))) return;
    setBusyId(id);
    setActionError(null);
    try {
      await revokeSession(id);
      setSessions((prev) => prev.filter((s) => s.id !== id));
    } catch (e) {
      // Action failures go to their own banner — routing them into `error`
      // replaced the whole session list with the message, killing retry.
      setActionError(e instanceof Error ? e.message : 'Failed to revoke session');
    } finally {
      setBusyId(null);
    }
  };

  const handleSignOutAll = async () => {
    if (!(await confirm({ title: 'Sign out other devices', body: 'Sign out of every other device? This keeps you signed in here.', confirmLabel: 'Sign them out' }))) return;
    setSigningOut(true);
    setActionError(null);
    try {
      await signOutEverywhere();
      await load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to sign out other devices');
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

      {actionError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{actionError}</div>
      )}

      {error ? (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">{error}</div>
      ) : loading ? (
        <SpinnerBlock />
      ) : sessions.length === 0 ? (
        <EmptyState icon={Monitor} title="No active sessions." />
      ) : (
        <div className="space-y-2">
          {sessions.map((s) => (
            <div
              key={s.id}
              className="flex items-center justify-between rounded-xl border border-border p-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium text-foreground">{s.device_label || 'Unknown device'}</span>
                  {s.current && <Badge variant="success">This device</Badge>}
                </div>
                <div className="mt-0.5 text-xs text-muted-foreground">
                  {s.ip || 'unknown IP'} · last active {relativeTime(s.last_used_at || s.created_at)}
                </div>
              </div>
              {!s.current && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => handleRevoke(s.id)}
                  disabled={busyId === s.id}
                  className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                >
                  {busyId === s.id ? 'Revoking…' : 'Revoke'}
                </Button>
              )}
            </div>
          ))}
        </div>
      )}

      {others.length > 0 && (
        <div className="pt-2">
          <Button
            onClick={handleSignOutAll}
            disabled={signingOut}
            className="bg-destructive/10 text-destructive shadow-none hover:bg-destructive/20"
          >
            {signingOut ? 'Signing out…' : 'Sign out all other devices'}
          </Button>
          <p className="mt-1.5 text-xs text-muted-foreground">
            Ends every session except this one and invalidates their access immediately.
          </p>
        </div>
      )}
      {dialog}
    </div>
  );
}
