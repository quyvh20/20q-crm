import { useEffect, useState } from 'react';
import { getMe, changePassword, setPassword, unlinkGoogle, type AuthMethods } from '../../lib/api';
import SecuritySessions from '../../components/settings/SecuritySessions';
import TwoFactorSetup from '../../components/settings/TwoFactorSetup';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Badge, Button, Input, Label, Skeleton } from '@/components/ui';

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

  return (
    <div className="space-y-8">
      {loadError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{loadError}</div>
      )}

      {/* Password — wait for methods so we don't flash the wrong form */}
      {methods === null && !loadError ? (
        <Skeleton className="max-w-md h-40 rounded-lg" />
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
          <div className={`rounded-lg border p-3 text-sm ${msg.ok ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'border-destructive/40 bg-destructive/10 text-destructive'}`}>
            {msg.text}
          </div>
        )}
        {hasPassword && (
          <div>
            <Label htmlFor="current-password" className="mb-1 block text-xs text-muted-foreground">Current password</Label>
            <Input id="current-password" type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          </div>
        )}
        <div>
          <Label htmlFor="new-password" className="mb-1 block text-xs text-muted-foreground">New password</Label>
          <Input id="new-password" type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </div>
        <div>
          <Label htmlFor="confirm-password" className="mb-1 block text-xs text-muted-foreground">Confirm new password</Label>
          <Input id="confirm-password" type="password" autoComplete="new-password" value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
        </div>
        <Button onClick={submitPassword} disabled={busy || !next || !confirmPw || (hasPassword && !current)}>
          {busy ? 'Saving…' : hasPassword ? 'Change password' : 'Set password'}
        </Button>
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
            <Badge variant={hasPassword ? 'success' : 'secondary'}>{hasPassword ? 'Active' : 'Off'}</Badge>
          </div>
          <div className="flex items-center justify-between p-3">
            <div>
              <p className="text-sm font-medium text-foreground">Google</p>
              <p className="text-xs text-muted-foreground">
                {methods?.google ? 'Connected — you can sign in with Google' : 'Not connected'}
              </p>
            </div>
            {methods?.google ? (
              <Button
                variant="outline"
                size="sm"
                onClick={disconnectGoogle}
                disabled={googleBusy || !hasPassword}
                title={!hasPassword ? 'Set a password first so you keep a way to sign in' : undefined}
                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
              >
                {googleBusy ? 'Disconnecting…' : 'Disconnect'}
              </Button>
            ) : (
              <Badge variant="secondary">Off</Badge>
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
