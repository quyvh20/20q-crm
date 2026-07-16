import { useEffect, useRef, useState } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { resetPassword } from '../lib/api';
import { useAuth } from '../lib/auth';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Button, Card, Input, Label } from '@/components/ui';

export default function ResetPasswordPage() {
  useDocumentTitle('Reset Password');
  const [searchParams] = useSearchParams();
  // Capture the token once, then scrub it from the address bar so the raw reset
  // token can't be shoulder-surfed, bookmarked, or leaked via the Referer header
  // (P2). The ref keeps it usable after the URL is rewritten.
  const tokenRef = useRef(searchParams.get('token'));
  const token = tokenRef.current;
  const navigate = useNavigate();
  const { logout } = useAuth();

  useEffect(() => {
    if (token && window.history?.replaceState) {
      window.history.replaceState(null, '', window.location.pathname);
    }
  }, [token]);

  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');

    if (password.length < 8) {
      setError('Password must be at least 8 characters.');
      return;
    }
    if (password !== confirm) {
      setError('Passwords do not match.');
      return;
    }
    if (!token) {
      setError('This reset link is missing its token. Please request a new one.');
      return;
    }

    setLoading(true);
    try {
      await resetPassword(token, password);
      // The resetting user may still be logged in (this route is intentionally
      // reachable while authenticated). Tear down the local session so PublicRoute
      // lets /login render its confirmation instead of bouncing to the dashboard,
      // and so the now-stale access token is dropped immediately.
      await logout();
      navigate('/login?message=password-reset');
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to reset password');
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
          <p className="text-sm text-muted-foreground mt-2">Choose a new password</p>
        </div>

        <Card className="p-8">
          {!token ? (
            <div className="space-y-4">
              <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                This password reset link is invalid or incomplete. Please request a new one.
              </div>
              <a
                href="/forgot-password"
                className="block text-center font-medium text-primary hover:underline"
              >
                Request a new link
              </a>
            </div>
          ) : (
            <>
              {error && (
                <div className="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  {error}
                </div>
              )}

              <form onSubmit={handleSubmit} className="space-y-4">
                <div>
                  <Label htmlFor="reset-password" className="mb-1.5">
                    New password
                  </Label>
                  <Input
                    id="reset-password"
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    required
                    autoComplete="new-password"
                    placeholder="At least 8 characters"
                  />
                </div>

                <div>
                  <Label htmlFor="reset-confirm" className="mb-1.5">
                    Confirm new password
                  </Label>
                  <Input
                    id="reset-confirm"
                    type="password"
                    value={confirm}
                    onChange={(e) => setConfirm(e.target.value)}
                    required
                    autoComplete="new-password"
                    placeholder="Re-enter your new password"
                  />
                </div>

                <Button type="submit" disabled={loading} className="w-full">
                  {loading ? 'Resetting...' : 'Reset password'}
                </Button>
              </form>
            </>
          )}
        </Card>
      </div>
    </div>
  );
}
