import { useEffect, useState } from 'react';
import { getMe, changePassword, setPassword, unlinkGoogle, type AuthMethods } from '../../lib/api';
import SecuritySessions from '../../components/settings/SecuritySessions';
import TwoFactorSetup from '../../components/settings/TwoFactorSetup';
import { useConfirm } from '../../components/common/ConfirmDialog';

// Security section (U2): in-app password management + connected accounts on
// top of the existing device-session list. Before this, rotating a password
// meant signing out and using the public "forgot password" email flow.

export default function SecuritySection() {
  const [methods, setMethods] = useState<AuthMethods | null>(null);
  const [loadError, setLoadError] = useState('');
  const { confirm, dialog } = useConfirm();

  // Password form
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirmPw, setConfirmPw] = useState('');
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);

  const [googleBusy, setGoogleBusy] = useState(false);
  const [googleMsg, setGoogleMsg] = useState('');

  const load = () => {
    setLoadError('');
    getMe()
      .then((me) => setMethods(me.auth_methods))
      .catch((e) => setLoadError(e instanceof Error ? e.message : 'Failed to load account details'));
  };
  useEffect(load, []);

  // hasPassword is only meaningful once auth_methods has loaded — defaulting it
  // true while null showed OAuth-only users a Change-password form they can't
  // use, so the password section waits for methods (U2 review).
  const hasPassword = methods?.password ?? false;

  const submitPassword = async () => {
    setMsg(null);
    if (next !== confirmPw) {
      setMsg({ ok: false, text: 'New password and confirmation do not match.' });
      return;
    }
    setBusy(true);
    try {
      if (hasPassword) {
        await changePassword(current, next);
      } else {
        await setPassword(next);
      }
      setCurrent(''); setNext(''); setConfirmPw('');
      setMsg({ ok: true, text: hasPassword ? 'Password changed. Every other device has been signed out.' : 'Password set. You can now sign in with email + password too.' });
      load();
    } catch (e) {
      setMsg({ ok: false, text: e instanceof Error ? e.message : 'Failed to update password' });
    } finally {
      setBusy(false);
    }
  };

  const disconnectGoogle = async () => {
    if (!(await confirm({
      title: 'Disconnect Google',
      body: 'You will no longer be able to sign in with Google — only with your email and password.',
      confirmLabel: 'Disconnect',
    }))) return;
    setGoogleBusy(true);
    setGoogleMsg('');
    try {
      await unlinkGoogle();
      setGoogleMsg('Google sign-in disconnected.');
      load();
    } catch (e) {
      setGoogleMsg(e instanceof Error ? e.message : 'Failed to disconnect Google');
    } finally {
      setGoogleBusy(false);
    }
  };

  const inputCls = 'w-full px-3 py-2 bg-background border border-border rounded-lg text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary';

  return (
    <div className="space-y-8">
      {loadError && (
        <div className="rounded-md border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-400">{loadError}</div>
      )}

      {/* Password — wait for methods so we don't flash the wrong form */}
      {methods === null && !loadError ? (
        <div className="max-w-md h-40 rounded-lg bg-muted/50 animate-pulse" />
      ) : (
      <section className="max-w-md space-y-3">
        <div>
          <h2 className="text-lg font-semibold">{hasPassword ? 'Change password' : 'Set a password'}</h2>
          <p className="text-sm text-muted-foreground mt-0.5">
            {hasPassword
              ? 'Changing your password signs out every other device.'
              : 'Your account signs in with Google only. Setting a password adds a second way in — useful if you ever lose Google access.'}
          </p>
        </div>
        {msg && (
          <div className={`rounded-md border p-3 text-sm ${msg.ok ? 'border-green-500/40 bg-green-500/10 text-green-500' : 'border-red-500/40 bg-red-500/10 text-red-400'}`}>
            {msg.text}
          </div>
        )}
        {hasPassword && (
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Current password</label>
            <input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} className={inputCls} />
          </div>
        )}
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">New password</label>
          <input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} className={inputCls} />
        </div>
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">Confirm new password</label>
          <input type="password" autoComplete="new-password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} className={inputCls} />
        </div>
        <button
          onClick={submitPassword}
          disabled={busy || !next || !confirmPw || (hasPassword && !current)}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {busy ? 'Saving…' : hasPassword ? 'Change password' : 'Set password'}
        </button>
      </section>
      )}

      {/* Connected accounts */}
      <section className="max-w-md space-y-3">
        <div>
          <h2 className="text-lg font-semibold">Connected accounts</h2>
          <p className="text-sm text-muted-foreground mt-0.5">Ways you can sign in to this account.</p>
        </div>
        {googleMsg && <div className="text-sm text-muted-foreground">{googleMsg}</div>}
        <div className="rounded-lg border border-border divide-y divide-border">
          <div className="flex items-center justify-between p-3">
            <div>
              <p className="text-sm font-medium text-foreground">Email &amp; password</p>
              <p className="text-xs text-muted-foreground">{hasPassword ? 'Enabled' : 'Not set — use the form above to add one'}</p>
            </div>
            <span className={`text-[11px] font-semibold px-2 py-0.5 rounded-full ${hasPassword ? 'bg-green-500/10 text-green-500' : 'bg-muted text-muted-foreground'}`}>
              {hasPassword ? 'Active' : 'Off'}
            </span>
          </div>
          <div className="flex items-center justify-between p-3">
            <div>
              <p className="text-sm font-medium text-foreground">Google</p>
              <p className="text-xs text-muted-foreground">
                {methods?.google ? 'Connected — you can sign in with Google' : 'Not connected'}
              </p>
            </div>
            {methods?.google ? (
              <button
                onClick={disconnectGoogle}
                disabled={googleBusy || !hasPassword}
                title={!hasPassword ? 'Set a password first so you keep a way to sign in' : undefined}
                className="text-sm text-red-400 border border-border rounded-md px-3 py-1.5 hover:bg-red-500/10 disabled:opacity-50"
              >
                {googleBusy ? 'Disconnecting…' : 'Disconnect'}
              </button>
            ) : (
              <span className="text-[11px] font-semibold px-2 py-0.5 rounded-full bg-muted text-muted-foreground">Off</span>
            )}
          </div>
        </div>
      </section>

      {/* Two-factor authentication (U6.4) — sits between the ways you can sign in
          and the devices that are signed in, because it gates both. */}
      <TwoFactorSetup />

      {/* Devices */}
      <section>
        <SecuritySessions />
      </section>

      {dialog}
    </div>
  );
}
