import { useEffect, useState } from 'react';
import {
  useNotificationPreferences,
  useUpdateNotificationPreferences,
} from '../../features/notifications/queries';
import type { NotificationTypePref } from '../../features/notifications/api';

// Notification preferences (U5): a member controls, per event type, whether it
// reaches the in-app bell and/or their email — plus a mute-all switch and an
// email-digest mode. The server applies these at notification-create time and in
// the daily digest job; the UI here is just the control surface.

// A small, accessible on/off switch styled with the settings tokens.
function Toggle({ on, onChange, disabled, label }: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!on)}
      className={`relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors disabled:opacity-40 ${on ? 'bg-primary' : 'bg-muted'}`}
    >
      <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${on ? 'translate-x-4' : 'translate-x-0.5'}`} />
    </button>
  );
}

export default function NotificationPreferencesSection() {
  const { data, isLoading, isError, error } = useNotificationPreferences();
  const update = useUpdateNotificationPreferences();

  const [muteAll, setMuteAll] = useState(false);
  const [digest, setDigest] = useState<'off' | 'daily'>('off');
  const [types, setTypes] = useState<NotificationTypePref[]>([]);
  const [saveMsg, setSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);

  // Seed local state once the server prefs load (and on any external refresh).
  useEffect(() => {
    if (data) {
      setMuteAll(data.mute_all);
      setDigest(data.email_digest);
      setTypes(data.types);
    }
  }, [data]);

  const setType = (key: string, patch: Partial<Pick<NotificationTypePref, 'in_app' | 'email'>>) => {
    setTypes((prev) => prev.map((t) => (t.key === key ? { ...t, ...patch } : t)));
  };

  const save = async () => {
    setSaveMsg(null);
    try {
      await update.mutateAsync({
        mute_all: muteAll,
        email_digest: digest,
        types: types.map((t) => ({ key: t.key, in_app: t.in_app, email: t.email })),
      });
      setSaveMsg({ ok: true, text: 'Notification preferences saved.' });
    } catch (e) {
      setSaveMsg({ ok: false, text: e instanceof Error ? e.message : 'Failed to save' });
    }
  };

  if (isLoading) {
    return <div className="space-y-3">{[...Array(3)].map((_, i) => <div key={i} className="h-12 rounded-lg bg-muted/50 animate-pulse" />)}</div>;
  }
  if (isError) {
    return <div className="rounded-md border border-red-500/40 bg-red-500/10 p-4 text-sm text-red-400">{error instanceof Error ? error.message : 'Failed to load your notification preferences'}</div>;
  }

  return (
    <div className="space-y-6 max-w-2xl">
      <div>
        <h2 className="text-lg font-semibold">Notifications</h2>
        <p className="text-sm text-muted-foreground mt-0.5">Choose how you're notified. Email is off by default.</p>
      </div>

      {saveMsg && (
        <div className={`rounded-md border p-3 text-sm ${saveMsg.ok ? 'border-green-500/40 bg-green-500/10 text-green-500' : 'border-red-500/40 bg-red-500/10 text-red-400'}`}>
          {saveMsg.text}
        </div>
      )}

      {/* Mute all */}
      <div className="flex items-center justify-between rounded-lg border border-border p-4">
        <div>
          <div className="text-sm font-medium">Pause all notifications</div>
          <p className="text-xs text-muted-foreground mt-0.5">Silences the bell and all emails. Your per-type choices below are kept for when you turn it back on.</p>
        </div>
        <Toggle on={muteAll} onChange={setMuteAll} label="Pause all notifications" />
      </div>

      {/* Per-type channel grid */}
      <div className={`rounded-lg border border-border overflow-hidden ${muteAll ? 'opacity-50' : ''}`}>
        <div className="grid grid-cols-[1fr_auto_auto] items-center gap-x-6 px-4 py-2 border-b border-border bg-muted/30 text-xs font-medium text-muted-foreground">
          <div>Notification type</div>
          <div className="w-12 text-center">In-app</div>
          <div className="w-12 text-center">Email</div>
        </div>
        {types.map((t) => (
          <div key={t.key} className="grid grid-cols-[1fr_auto_auto] items-center gap-x-6 px-4 py-3 border-b border-border last:border-b-0">
            <div>
              <div className="text-sm font-medium">{t.label}</div>
              {t.description && <p className="text-xs text-muted-foreground mt-0.5">{t.description}</p>}
            </div>
            <div className="w-12 flex justify-center">
              <Toggle on={t.in_app} onChange={(v) => setType(t.key, { in_app: v })} disabled={muteAll} label={`${t.label} in-app`} />
            </div>
            <div className="w-12 flex justify-center">
              <Toggle on={t.email} onChange={(v) => setType(t.key, { email: v })} disabled={muteAll} label={`${t.label} email`} />
            </div>
          </div>
        ))}
      </div>

      {/* Email digest */}
      <div className={muteAll ? 'opacity-50' : ''}>
        <label className="block text-sm font-medium mb-1.5">Email delivery</label>
        <div className="inline-flex rounded-lg border border-border overflow-hidden">
          {([['off', 'Send immediately'], ['daily', 'Daily digest']] as const).map(([val, lbl]) => (
            <button
              key={val}
              type="button"
              disabled={muteAll}
              onClick={() => setDigest(val)}
              className={`px-4 py-1.5 text-sm transition-colors disabled:opacity-60 ${digest === val ? 'bg-accent text-accent-foreground font-medium' : 'text-muted-foreground hover:bg-accent/50'}`}
            >
              {lbl}
            </button>
          ))}
        </div>
        <p className="text-xs text-muted-foreground mt-1">
          {digest === 'daily'
            ? "Email-enabled notifications are batched into one email a day instead of sent one at a time."
            : "Email-enabled notifications are emailed to you as they happen."}
        </p>
      </div>

      <div>
        <button
          onClick={save}
          disabled={update.isPending}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {update.isPending ? 'Saving…' : 'Save preferences'}
        </button>
      </div>
    </div>
  );
}
