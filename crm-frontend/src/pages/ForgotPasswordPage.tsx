import { useState } from 'react';
import { forgotPassword } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Button, Card, Input, Label } from '@/components/ui';

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
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Guerrilla <span className="text-primary">CRM</span>
          </h1>
          <p className="text-sm text-muted-foreground mt-2">Reset your password</p>
        </div>

        <Card className="p-8">
          {submitted ? (
            <div className="space-y-4">
              <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">
                If an account exists for <span className="font-medium">{email}</span>, we've sent a
                password reset link. Check your inbox — the link expires in 1 hour.
              </div>
              {debugToken && (
                <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-600 dark:text-amber-400 break-all">
                  <span className="font-semibold">Dev only:</span>{' '}
                  <a
                    href={`/reset-password?token=${debugToken}`}
                    className="underline hover:no-underline"
                  >
                    Open reset link
                  </a>
                </div>
              )}
              <a
                href="/login"
                className="block text-center font-medium text-primary hover:underline"
              >
                Back to sign in
              </a>
            </div>
          ) : (
            <>
              <p className="text-sm text-muted-foreground mb-6">
                Enter your account email and we'll send you a link to reset your password.
              </p>

              {error && (
                <div className="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {error}
                </div>
              )}

              <form onSubmit={handleSubmit} className="space-y-4">
                <div>
                  <Label htmlFor="forgot-email" className="mb-1.5">
                    Email
                  </Label>
                  <Input
                    id="forgot-email"
                    type="email"
                    autoComplete="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    required
                    placeholder="you@company.com"
                  />
                </div>

                <Button type="submit" disabled={loading} className="w-full">
                  {loading ? 'Sending...' : 'Send reset link'}
                </Button>
              </form>

              <p className="text-center text-sm text-muted-foreground mt-6">
                Remembered it?{' '}
                <a href="/login" className="font-medium text-primary hover:underline">
                  Back to sign in
                </a>
              </p>
            </>
          )}
        </Card>
      </div>
    </div>
  );
}
