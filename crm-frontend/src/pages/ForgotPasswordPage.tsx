import { useState } from 'react';
import { forgotPassword } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';

export default function ForgotPasswordPage() {
  useDocumentTitle('Forgot Password');
  const [email, setEmail] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [debugToken, setDebugToken] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      const res = await forgotPassword(email);
      // In non-production the backend returns a debug token to ease local testing.
      setDebugToken(res.debug_token ?? null);
      setSubmitted(true);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Something went wrong');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-white tracking-tight">
            Guerrilla <span className="text-blue-400">CRM</span>
          </h1>
          <p className="text-slate-400 mt-2">Reset your password</p>
        </div>

        <div className="bg-slate-800/50 backdrop-blur-xl border border-slate-700/50 rounded-2xl p-8 shadow-2xl">
          {submitted ? (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-green-500/10 border border-green-500/20 text-green-300 text-sm">
                If an account exists for <span className="font-medium">{email}</span>, we've sent a
                password reset link. Check your inbox — the link expires in 1 hour.
              </div>
              {debugToken && (
                <div className="p-3 rounded-xl bg-amber-500/10 border border-amber-500/20 text-amber-300 text-xs break-all">
                  <span className="font-semibold">Dev only:</span>{' '}
                  <a
                    href={`/reset-password?token=${debugToken}`}
                    className="underline hover:text-amber-200"
                  >
                    Open reset link
                  </a>
                </div>
              )}
              <a
                href="/login"
                className="block text-center text-blue-400 hover:text-blue-300 font-medium transition-colors"
              >
                Back to sign in
              </a>
            </div>
          ) : (
            <>
              <p className="text-slate-400 text-sm mb-6">
                Enter your account email and we'll send you a link to reset your password.
              </p>

              {error && (
                <div className="mb-4 p-3 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm">
                  {error}
                </div>
              )}

              <form onSubmit={handleSubmit} className="space-y-4">
                <div>
                  <label htmlFor="forgot-email" className="block text-sm font-medium text-slate-300 mb-1.5">
                    Email
                  </label>
                  <input
                    id="forgot-email"
                    type="email"
                    autoComplete="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                    className="w-full px-4 py-3 bg-slate-900/50 border border-slate-700 rounded-xl text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500 transition-all"
                    placeholder="you@company.com"
                  />
                </div>

                <button
                  type="submit"
                  disabled={loading}
                  className="w-full py-3 px-4 bg-gradient-to-r from-blue-600 to-blue-500 hover:from-blue-500 hover:to-blue-400 text-white font-semibold rounded-xl transition-all duration-200 hover:shadow-lg hover:shadow-blue-500/25 hover:scale-[1.01] active:scale-[0.99] disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:scale-100"
                >
                  {loading ? 'Sending...' : 'Send reset link'}
                </button>
              </form>

              <p className="text-center text-sm text-slate-400 mt-6">
                Remembered it?{' '}
                <a href="/login" className="text-blue-400 hover:text-blue-300 font-medium transition-colors">
                  Back to sign in
                </a>
              </p>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
