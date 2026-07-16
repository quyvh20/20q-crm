import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { verifyEmail } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Card, Spinner } from '@/components/ui';

export default function VerifyEmailPage() {
  useDocumentTitle('Verify Email');
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
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Guerrilla <span className="text-primary">CRM</span>
          </h1>
          <p className="text-sm text-muted-foreground mt-2">Email verification</p>
        </div>

        <Card className="p-8 text-center">
          {status === 'loading' && (
            <div className="flex flex-col items-center gap-4 py-4">
              <Spinner size="lg" />
              <p className="text-sm text-muted-foreground">Verifying your email…</p>
            </div>
          )}

          {status === 'success' && (
            <div className="space-y-4">
              <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">
                Your email address has been verified. Thanks!
              </div>
              <a
                href="/"
                className="block text-center font-medium text-primary hover:underline"
              >
                Continue to Guerrilla CRM
              </a>
            </div>
          )}

          {status === 'error' && (
            <div className="space-y-4">
              <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                {message || 'This verification link is invalid or has expired.'}
              </div>
              <p className="text-sm text-muted-foreground">
                Sign in and use the banner to request a fresh verification email.
              </p>
              <a
                href="/login"
                className="block text-center font-medium text-primary hover:underline"
              >
                Go to sign in
              </a>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
