import { useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { TwoFactorVerifyError } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Button, Card, Input, Label, buttonVariants } from '@/components/ui';
import { cn } from '@/lib/utils';

// The second step of sign-in (U6.4). The password (or Google) proved the identity;
// this proves possession of the second factor and exchanges the short-lived
// challenge for a real session.
//
// Two ways to get here:
//  • password login — LoginPage hands the challenge over in router state (history
//    state, so it survives a reload) rather than the URL, which would leak a bearer
//    credential into history/logs;
//  • Google — the server parks the challenge in an httpOnly cookie and redirects
//    straight to /login/2fa, so there is no token in state at all. verifyTwoFactor
//    sends '' and the server falls back to the cookie.

interface ChallengeState {
  challengeToken?: string;
  next?: string | null;
}

export default function TwoFactorChallengePage() {
  useDocumentTitle('Two-Factor Authentication');
  const { verifyTwoFactor } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const state = (location.state ?? {}) as ChallengeState;
  // Absent in the Google flow — the challenge is in the httpOnly cookie instead.
  const challengeToken = state.challengeToken ?? '';

  const [code, setCode] = useState('');
  const [useBackup, setUseBackup] = useState(false);
  const [error, setError] = useState('');
  // A dead challenge (five wrong codes, or expired) can't be retried — the form is
  // gone and the only way forward is a fresh sign-in.
  const [dead, setDead] = useState(false);
  const [loading, setLoading] = useState(false);

  const trimmed = code.trim();
  const canSubmit = trimmed.length >= (useBackup ? 10 : 6) && !loading && !dead;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setError('');
    setLoading(true);
    try {
      await verifyTwoFactor(challengeToken, trimmed);
      const next = state.next;
      const safeNext =
        next && next.startsWith('/') && !next.startsWith('//') && !next.includes('\\') ? next : '/';
      navigate(safeNext, { replace: true });
    } catch (err: unknown) {
      if (err instanceof TwoFactorVerifyError && (err.status === 429 || err.status === 400)) {
        // 429: too many incorrect codes — the challenge is destroyed.
        // 400: no/expired challenge (e.g. this page was opened cold).
        setDead(true);
      }
      setError(err instanceof Error ? err.message : 'That code isn’t right');
      setLoading(false);
      return;
    }
    setLoading(false);
  };

  const switchMode = () => {
    setUseBackup((v) => !v);
    setCode('');
    setError('');
  };

  return (
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Guerrilla <span className="text-primary">CRM</span>
          </h1>
          <p className="text-sm text-muted-foreground mt-2">Two-factor authentication</p>
        </div>

        <Card className="p-8">
          {error && (
            <div className="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          {dead ? (
            <div className="text-center space-y-4">
              <p className="text-sm text-muted-foreground">
                This sign-in attempt has been cancelled for your safety. Start again from the sign-in page.
              </p>
              <a href="/login" className={cn(buttonVariants(), 'w-full')}>
                Back to sign in
              </a>
            </div>
          ) : (
            <>
              <p className="text-sm text-muted-foreground mb-5">
                {useBackup
                  ? 'Enter one of the backup codes you saved when you turned on two-factor authentication. Each code works once.'
                  : 'Open your authenticator app and enter the 6-digit code for Guerrilla CRM.'}
              </p>

              <form onSubmit={handleSubmit} className="space-y-4">
                <div>
                  <Label htmlFor="totp-code" className="mb-1.5">
                    {useBackup ? 'Backup code' : 'Authentication code'}
                  </Label>
                  <Input
                    id="totp-code"
                    type="text"
                    inputMode={useBackup ? 'text' : 'numeric'}
                    autoComplete="one-time-code"
                    autoFocus
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    placeholder={useBackup ? 'XXXXX-XXXXX' : '123456'}
                    maxLength={useBackup ? 24 : 6}
                    className="h-11 text-center text-lg tracking-[0.3em]"
                  />
                </div>

                <Button type="submit" disabled={!canSubmit} className="w-full">
                  {loading ? 'Verifying…' : 'Verify'}
                </Button>
              </form>

              <button
                type="button"
                onClick={switchMode}
                className="w-full text-center text-sm font-medium text-primary hover:underline mt-5"
              >
                {useBackup ? 'Use your authenticator app instead' : 'Use a backup code instead'}
              </button>
            </>
          )}

          <p className="text-center text-sm text-muted-foreground mt-6">
            <a href="/login" className="transition-colors hover:text-foreground">
              Cancel and sign in as someone else
            </a>
          </p>
        </Card>
      </div>
    </div>
  );
}
