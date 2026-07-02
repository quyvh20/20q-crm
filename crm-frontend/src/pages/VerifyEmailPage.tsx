import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { verifyEmail } from '../lib/api';

export default function VerifyEmailPage() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get('token');
  const [status, setStatus] = useState<'loading' | 'success' | 'error'>('loading');
  const [message, setMessage] = useState('');
  const ran = useRef(false);

  useEffect(() => {
    // Guard against React 18 StrictMode double-invoke consuming the token twice.
    if (ran.current) return;
    ran.current = true;

    if (!token) {
      setStatus('error');
      setMessage('This verification link is missing its token.');
      return;
    }
    verifyEmail(token)
      .then(() => setStatus('success'))
      .catch((err: unknown) => {
        setStatus('error');
        setMessage(err instanceof Error ? err.message : 'Verification failed');
      });
  }, [token]);

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-3xl font-bold text-white tracking-tight">
            Guerrilla <span className="text-blue-400">CRM</span>
          </h1>
          <p className="text-slate-400 mt-2">Email verification</p>
        </div>

        <div className="bg-slate-800/50 backdrop-blur-xl border border-slate-700/50 rounded-2xl p-8 shadow-2xl text-center">
          {status === 'loading' && (
            <div className="flex flex-col items-center gap-4 py-4">
              <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full" />
              <p className="text-slate-400 text-sm">Verifying your email…</p>
            </div>
          )}

          {status === 'success' && (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-green-500/10 border border-green-500/20 text-green-300 text-sm">
                Your email address has been verified. Thanks!
              </div>
              <a
                href="/"
                className="block text-center text-blue-400 hover:text-blue-300 font-medium transition-colors"
              >
                Continue to Guerrilla CRM
              </a>
            </div>
          )}

          {status === 'error' && (
            <div className="space-y-4">
              <div className="p-4 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm">
                {message || 'This verification link is invalid or has expired.'}
              </div>
              <p className="text-slate-400 text-sm">
                Sign in and use the banner to request a fresh verification email.
              </p>
              <a
                href="/login"
                className="block text-center text-blue-400 hover:text-blue-300 font-medium transition-colors"
              >
                Go to sign in
              </a>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
