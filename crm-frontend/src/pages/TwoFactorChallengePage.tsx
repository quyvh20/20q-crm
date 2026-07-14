import { useState } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { TwoFactorVerifyError } from '../lib/api';

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
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-white tracking-tight">
            Guerrilla <span className="text-blue-400">CRM</span>
          </h1>
          <p className="text-slate-400 mt-2">Two-factor authentication</p>
        </div>

        <div className="bg-slate-800/50 backdrop-blur-xl border border-slate-700/50 rounded-2xl p-8 shadow-2xl">
          {error && (
            <div className="mb-4 p-3 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm">
              {error}
            </div>
          )}

          {dead ? (
            <div className="text-center space-y-4">
              <p className="text-slate-300 text-sm">
                This sign-in attempt has been cancelled for your safety. Start again from the sign-in page.
              </p>
              <a
                href="/login"
                className="inline-block w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white font-semibold rounded-xl transition-all"
              >
                Back to sign in
              </a>
            </div>
          ) : (
            <>
              <p className="text-slate-400 text-sm mb-5">
                {useBackup
                  ? 'Enter one of the backup codes you saved when you turned on two-factor authentication. Each code works once.'
                  : 'Open your authenticator app and enter the 6-digit code for Guerrilla CRM.'}
              </p>

              <form onSubmit={handleSubmit} className="space-y-4">
                <div>
                  <label htmlFor="totp-code" className="block text-sm font-medium text-slate-300 mb-1.5">
                    {useBackup ? 'Backup code' : 'Authentication code'}
                  </label>
                  <input
                    id="totp-code"
                    type="text"
                    inputMode={useBackup ? 'text' : 'numeric'}
                    autoComplete="one-time-code"
                    autoFocus
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    placeholder={useBackup ? 'XXXXX-XXXXX' : '123456'}
                    maxLength={useBackup ? 24 : 6}
                    className="w-full px-4 py-3 bg-slate-900/50 border border-slate-700 rounded-xl text-white tracking-[0.3em] text-center text-lg placeholder-slate-600 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 transition-all"
                  />
                </div>

                <button
                  type="submit"
                  disabled={!canSubmit}
                  className="w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white font-semibold rounded-xl transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {loading ? 'Verifying…' : 'Verify'}
                </button>
              </form>

              <button
                type="button"
                onClick={switchMode}
                className="w-full text-center text-sm text-blue-400 hover:text-blue-300 font-medium transition-colors mt-5"
              >
                {useBackup ? 'Use your authenticator app instead' : 'Use a backup code instead'}
              </button>
            </>
          )}

          <p className="text-center text-sm text-slate-500 mt-6">
            <a href="/login" className="hover:text-slate-300 transition-colors">
              Cancel and sign in as someone else
            </a>
          </p>
        </div>
      </div>
    </div>
  );
}
