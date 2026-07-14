import { useState, useEffect } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { ENROLL_TWO_FACTOR_PATH } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import LegalConsent from '../components/auth/LegalConsent';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

const GOOGLE_ERROR_MESSAGES: Record<string, string> = {
  access_denied: 'Google sign-in was cancelled',
  missing_code: 'Google sign-in failed: missing authorization code',
  google_login_failed: 'Google sign-in failed. Please try again.',
  invalid_oauth_state: 'Google sign-in could not be verified. Please start again from this page.',
};

const NOTICE_MESSAGES: Record<string, string> = {
  'password-reset': 'Your password has been reset. Please sign in with your new password.',
  'invitation-accepted': 'Invitation accepted. Please sign in to continue.',
};

export default function LoginPage() {
  useDocumentTitle('Sign In');
  const { login } = useAuth();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [loading, setLoading] = useState(false);

  // Show Google OAuth errors and post-action notices from redirect
  useEffect(() => {
    const errCode = searchParams.get('error');
    if (errCode) {
      const detail = searchParams.get('detail');
      const baseMsg = GOOGLE_ERROR_MESSAGES[errCode] || `Sign-in error: ${errCode}`;
      setError(detail ? `${baseMsg} (${detail})` : baseMsg);
    }
    const msg = searchParams.get('message');
    if (msg) {
      setNotice(NOTICE_MESSAGES[msg] || '');
    }
    // Session-expiry notice (U2): the app redirects here with ?expired=1&next=…
    // instead of a silent boot; PublicRoute honors next after sign-in.
    if (searchParams.get('expired')) {
      setNotice('Your session expired — sign in to pick up where you left off.');
    }
  }, [searchParams]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const result = await login(email, password);
      // 2FA (U6.4): a correct password may buy a CHALLENGE rather than a session.
      // The challenge rides in router state (history.state, so it survives a
      // reload) and never in the URL — it's a bearer credential for the next step.
      if (result.twoFactorRequired) {
        const next = searchParams.get('next');
        navigate('/login/2fa', { state: { challengeToken: result.challengeToken, next }, replace: true });
        return;
      }
      // A real session, but the workspace demands a factor this user hasn't set
      // up: they're signed in and confined to enrolling until they comply.
      if (result.enrollRequired) {
        navigate(ENROLL_TWO_FACTOR_PATH, { replace: true });
        return;
      }
      // Otherwise the auth context is populated and PublicRoute redirects us out.
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Login failed');
    } finally {
      setLoading(false);
    }
  };

  const handleGoogleLogin = () => {
    window.location.href = `${API_URL}/api/auth/google`;
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
      <div className="w-full max-w-md">
        {/* Logo */}
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-white tracking-tight">
            Guerrilla <span className="text-blue-400">CRM</span>
          </h1>
          <p className="text-slate-400 mt-2">Sign in to your account</p>
        </div>

        {/* Card */}
        <div className="bg-slate-800/50 backdrop-blur-xl border border-slate-700/50 rounded-2xl p-8 shadow-2xl">
          {/* Google Button */}
          <button
            type="button"
            onClick={handleGoogleLogin}
            className="w-full flex items-center justify-center gap-3 px-4 py-3 bg-white hover:bg-gray-50 text-gray-800 font-medium rounded-xl transition-all duration-200 hover:shadow-lg hover:scale-[1.01] active:scale-[0.99]"
          >
            <svg className="w-5 h-5" viewBox="0 0 24 24">
              <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4" />
              <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
              <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05" />
              <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
            </svg>
            Continue with Google
          </button>

          {/* Divider */}
          <div className="flex items-center my-6">
            <div className="flex-1 h-px bg-slate-700"></div>
            <span className="px-4 text-sm text-slate-500">or continue with email</span>
            <div className="flex-1 h-px bg-slate-700"></div>
          </div>

          {/* Notice (e.g. after a successful password reset). role="status" — it's
              informational, so it's announced politely rather than interrupting. */}
          {notice && (
            <div role="status" className="mb-4 p-3 rounded-xl bg-green-500/10 border border-green-500/20 text-green-300 text-sm">
              {notice}
            </div>
          )}

          {/* Error. role="alert" so a failed sign-in is announced instead of
              silently repainting the form. */}
          {error && (
            <div role="alert" className="mb-4 p-3 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm">
              {error}
              {/* Stranded-passwordless hint (P2): an invited user, or one who
                  usually signs in with Google, may never have set a password.
                  Enumeration-safe — this is generic client text, shown on any
                  failed sign-in, never a server "no password" signal. */}
              <p className="mt-2 text-red-300/80 text-xs">
                Invited to a workspace, or usually sign in with Google? You may not have a password yet —{' '}
                <Link to="/forgot-password" className="underline hover:text-red-200">send yourself a reset link</Link> to set one.
              </p>
            </div>
          )}

          {/* Form */}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label htmlFor="login-email" className="block text-sm font-medium text-slate-300 mb-1.5">
                Email
              </label>
              <input
                id="login-email"
                type="email"
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                className="w-full px-4 py-3 bg-slate-900/50 border border-slate-700 rounded-xl text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 transition-all"
                placeholder="you@company.com"
              />
            </div>

            <div>
              <div className="flex items-center justify-between mb-1.5">
                <label htmlFor="login-password" className="block text-sm font-medium text-slate-300">
                  Password
                </label>
                <Link to="/forgot-password" className="text-sm text-blue-400 hover:text-blue-300 font-medium transition-colors">
                  Forgot password?
                </Link>
              </div>
              <input
                id="login-password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                className="w-full px-4 py-3 bg-slate-900/50 border border-slate-700 rounded-xl text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 transition-all"
                placeholder="••••••••"
              />
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white font-semibold rounded-xl transition-all duration-200 hover:shadow-lg hover:shadow-blue-500/25 hover:scale-[1.01] active:scale-[0.99] disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:scale-100"
            >
              {loading ? 'Signing in...' : 'Sign In'}
            </button>
          </form>

          {/* Consent (U7.6). This is a SIGN-IN page, but "Continue with Google"
              above SIGNS UP a Google user who has no account yet — so the consent
              line belongs here too, not only on /register. */}
          <LegalConsent className="mt-6" />

          {/* Register link */}
          <p className="text-center text-sm text-slate-400 mt-4">
            Don't have an account?{' '}
            <Link to="/register" className="text-blue-400 hover:text-blue-300 font-medium transition-colors">
              Create one
            </Link>
          </p>
        </div>

        <p className="text-center text-xs text-slate-600 mt-6">
          © 2026 Guerrilla CRM. All rights reserved.
        </p>
      </div>
    </div>
  );
}
