import { useCallback, useEffect, useState } from 'react';
import { KeyRound, Loader2, ShieldCheck, ShieldOff, Smartphone } from 'lucide-react';
import {
  getTwoFactorStatus, startTwoFactorSetup, enableTwoFactor, disableTwoFactor, regenerateBackupCodes,
  type TwoFactorSetupInfo, type TwoFactorStatus,
} from '../../lib/api';
import { useConfirm } from '../common/ConfirmDialog';
import SecretReveal from './SecretReveal';

// TwoFactorSetup (U6.4) is the whole personal 2FA lifecycle in one panel: enroll
// (QR → confirm a code → the one-time backup codes), regenerate those codes, and
// turn it off. Every state-changing step needs a live code — holding a session is
// not enough to add, rotate or drop a second factor.
//
// The QR is a server-rendered PNG data URI: the app ships no QR library, and the
// secret never has to be re-encoded client-side.

type Mode = 'idle' | 'enrolling' | 'reveal' | 'regenerating' | 'disabling';

export default function TwoFactorSetup({
  /** Enrollment is being forced by the workspace policy: hide the "not now" exits
   *  and tell the caller when the user finally complies. */
  forced = false,
  onEnrolled,
}: {
  forced?: boolean;
  onEnrolled?: () => void;
}) {
  const [status, setStatus] = useState<TwoFactorStatus | null>(null);
  const [loading, setLoading] = useState(true);
  // Load failures replace the panel; ACTION failures render inline so the user
  // can fix the code and retry (the SecuritySessions split).
  const [loadError, setLoadError] = useState('');
  const [actionError, setActionError] = useState('');
  const [notice, setNotice] = useState('');

  const [mode, setMode] = useState<Mode>('idle');
  const [setup, setSetup] = useState<TwoFactorSetupInfo | null>(null);
  const [showSecret, setShowSecret] = useState(false);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  // The one-time backup codes. Held only until the user acknowledges them.
  const [codes, setCodes] = useState<string[] | null>(null);

  const { confirm, dialog } = useConfirm();

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    try {
      setStatus(await getTwoFactorStatus());
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : 'Failed to load two-factor status');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const resetFlow = () => {
    setMode('idle');
    setSetup(null);
    setCode('');
    setShowSecret(false);
    setActionError('');
  };

  const beginSetup = async () => {
    setBusy(true);
    setActionError('');
    setNotice('');
    try {
      setSetup(await startTwoFactorSetup());
      setCode('');
      setMode('enrolling');
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to start setup');
    } finally {
      setBusy(false);
    }
  };

  const confirmEnable = async () => {
    setBusy(true);
    setActionError('');
    try {
      const fresh = await enableTwoFactor(code.trim());
      setCodes(fresh);
      setMode('reveal');
      setSetup(null);
      setCode('');
      await load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to turn on two-factor authentication');
    } finally {
      setBusy(false);
    }
  };

  const submitRegenerate = async () => {
    if (!(await confirm({
      title: 'Regenerate backup codes',
      body: 'Your existing backup codes stop working immediately. Any you have written down or saved become useless.',
      confirmLabel: 'Regenerate',
      tone: 'danger',
    }))) return;
    setBusy(true);
    setActionError('');
    try {
      const fresh = await regenerateBackupCodes(code.trim());
      setCodes(fresh);
      setMode('reveal');
      setCode('');
      await load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to regenerate backup codes');
    } finally {
      setBusy(false);
    }
  };

  const submitDisable = async () => {
    if (!(await confirm({
      title: 'Turn off two-factor authentication',
      body: 'Your account will be protected by your password alone. Your backup codes are destroyed.',
      confirmLabel: 'Turn it off',
      tone: 'danger',
    }))) return;
    setBusy(true);
    setActionError('');
    try {
      await disableTwoFactor(code.trim());
      setNotice('Two-factor authentication is off.');
      resetFlow();
      await load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to turn off two-factor authentication');
    } finally {
      setBusy(false);
    }
  };

  // A code (TOTP or backup) gates every state change; both forms live in this box.
  const codeInput = (label: string, onSubmit: () => void, submitLabel: string) => (
    <div className="space-y-2">
      <label htmlFor="twofa-code" className="block text-xs font-medium text-muted-foreground">{label}</label>
      <div className="flex flex-wrap gap-2">
        <input
          id="twofa-code"
          type="text"
          inputMode="text"
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="123456 or a backup code"
          className="w-48 px-3 py-2 bg-background border border-border rounded-lg text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary"
        />
        <button
          onClick={onSubmit}
          disabled={busy || code.trim().length < 6}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-lg text-sm font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {busy ? 'Checking…' : submitLabel}
        </button>
        {!(forced && mode === 'enrolling') && (
          <button
            onClick={resetFlow}
            disabled={busy}
            className="px-3 py-2 border border-border rounded-lg text-sm hover:bg-accent transition-colors disabled:opacity-50"
          >
            Cancel
          </button>
        )}
      </div>
    </div>
  );

  if (loading) {
    return (
      <section className="max-w-md">
        <div className="flex items-center justify-center py-10">
          <Loader2 className="w-6 h-6 animate-spin text-muted-foreground" />
        </div>
      </section>
    );
  }

  if (loadError) {
    return (
      <section className="max-w-md space-y-3">
        <h2 className="text-lg font-semibold">Two-factor authentication</h2>
        <div className="rounded-md border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-400">{loadError}</div>
        <button onClick={() => void load()} className="px-3 py-1.5 text-sm rounded-lg border border-border hover:bg-accent transition-colors">
          Try again
        </button>
      </section>
    );
  }

  const enabled = !!status?.enabled;
  const lockedOn = !!status?.required_by_workspace;
  const lowCodes = enabled && (status?.backup_codes_left ?? 0) <= 2;

  return (
    <section className="max-w-md space-y-4">
      <div>
        <h2 className="text-lg font-semibold flex items-center gap-2">
          <ShieldCheck className={`w-5 h-5 ${enabled ? 'text-green-500' : 'text-muted-foreground'}`} />
          Two-factor authentication
        </h2>
        <p className="text-sm text-muted-foreground mt-0.5">
          A code from your authenticator app on top of your password. Even a stolen password isn't enough to sign in.
        </p>
      </div>

      {notice && (
        <div className="rounded-md border border-green-500/40 bg-green-500/10 p-3 text-sm text-green-500">{notice}</div>
      )}
      {actionError && (
        <div className="rounded-md border border-red-500/40 bg-red-500/10 p-3 text-sm text-red-400">{actionError}</div>
      )}

      {/* The one-time reveal — backup codes, fresh or regenerated. */}
      {mode === 'reveal' && codes && (
        <SecretReveal
          title="Your backup codes"
          description="Use one of these if you ever lose your authenticator. Each code works once. This is the only time they'll be shown."
          values={codes}
          acknowledgeLabel="I've saved my backup codes"
          doneLabel={forced ? 'Continue' : 'Done'}
          onDone={() => {
            setCodes(null);
            setMode('idle');
            setNotice('Two-factor authentication is on.');
            if (forced) onEnrolled?.();
          }}
        />
      )}

      {mode !== 'reveal' && (
        <div className="rounded-lg border border-border p-4 space-y-4">
          <div className="flex items-center justify-between gap-3">
            <div>
              <p className="text-sm font-medium text-foreground flex items-center gap-1.5">
                <Smartphone className="w-4 h-4 text-muted-foreground" /> Authenticator app
              </p>
              <p className="text-xs text-muted-foreground mt-0.5">
                {enabled
                  ? `On since ${status?.enabled_at ? new Date(status.enabled_at).toLocaleDateString() : 'recently'} · ${status?.backup_codes_left ?? 0} backup code${(status?.backup_codes_left ?? 0) === 1 ? '' : 's'} left`
                  : 'Not set up'}
              </p>
            </div>
            <span className={`text-[11px] font-semibold px-2 py-0.5 rounded-full ${enabled ? 'bg-green-500/10 text-green-500' : 'bg-muted text-muted-foreground'}`}>
              {enabled ? 'Active' : 'Off'}
            </span>
          </div>

          {lockedOn && (
            <p className="text-xs text-amber-500">
              This workspace requires two-factor authentication, so it can't be turned off.
            </p>
          )}
          {lowCodes && (
            <p className="text-xs text-amber-500">
              You're almost out of backup codes — regenerate a fresh set so you don't get locked out.
            </p>
          )}

          {/* Not enrolled — start, or finish, the enrollment. */}
          {!enabled && mode === 'idle' && (
            <button
              onClick={beginSetup}
              disabled={busy}
              className="px-4 py-2 bg-primary text-primary-foreground rounded-lg text-sm font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {busy ? 'Preparing…' : 'Set up two-factor authentication'}
            </button>
          )}

          {!enabled && mode === 'enrolling' && setup && (
            <div className="space-y-3">
              <p className="text-sm text-foreground">
                Scan this with your authenticator app (1Password, Google Authenticator, Authy…), then enter the 6-digit code it shows.
              </p>
              <img
                src={setup.qr_data_uri}
                alt="QR code for your authenticator app"
                className="w-40 h-40 rounded-lg bg-white p-2"
              />
              <div>
                <button
                  type="button"
                  onClick={() => setShowSecret((v) => !v)}
                  className="text-xs text-blue-500 hover:underline"
                >
                  {showSecret ? 'Hide setup key' : "Can't scan? Enter a setup key instead"}
                </button>
                {showSecret && (
                  <code className="mt-2 block break-all rounded-md border border-border bg-background px-3 py-2 font-mono text-xs text-foreground">
                    {setup.secret}
                  </code>
                )}
              </div>
              {codeInput('Code from your app', confirmEnable, 'Turn on')}
            </div>
          )}

          {/* Enrolled — rotate the codes, or (unless the workspace forbids it) stop. */}
          {enabled && mode === 'idle' && (
            <div className="flex flex-wrap gap-2">
              <button
                onClick={() => { setMode('regenerating'); setCode(''); setActionError(''); setNotice(''); }}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg border border-border hover:bg-accent transition-colors"
              >
                <KeyRound className="w-4 h-4" /> Regenerate backup codes
              </button>
              {!lockedOn && (
                <button
                  onClick={() => { setMode('disabling'); setCode(''); setActionError(''); setNotice(''); }}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg border border-border text-red-400 hover:bg-red-500/10 transition-colors"
                >
                  <ShieldOff className="w-4 h-4" /> Turn off
                </button>
              )}
            </div>
          )}

          {enabled && mode === 'regenerating' &&
            codeInput('Confirm with a code from your app (or a backup code)', submitRegenerate, 'Regenerate')}

          {enabled && mode === 'disabling' &&
            codeInput('Confirm with a code from your app (or a backup code)', submitDisable, 'Turn off')}
        </div>
      )}

      {dialog}
    </section>
  );
}
