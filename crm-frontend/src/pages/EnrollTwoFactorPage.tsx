import { ShieldAlert } from 'lucide-react';
import { useAuth } from '../lib/auth';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import TwoFactorSetup from '../components/settings/TwoFactorSetup';
import { Button, Card } from '@/components/ui';

// The forced-enrollment dead-end (U6.4). A workspace can require 2FA of everyone;
// a member who hasn't enrolled still gets a REAL session — they need one to reach
// the enrollment endpoints at all — but the server 403s every other route with
// `two_factor_required`, and apiFetch parks them here.
//
// Deliberately OUTSIDE the app shell: every panel in AppLayout would 403, so
// rendering it would just be a wall of broken widgets.
//
// When enrollment completes we hard-navigate rather than router-navigate: the
// access token in memory still carries the "2fa pending" claim, and only a fresh
// page load (→ refresh from the cookie) mints one without it.
export default function EnrollTwoFactorPage() {
  useDocumentTitle('Set Up Two-Factor Authentication');
  const { user, logout } = useAuth();

  return (
    <div className="min-h-screen bg-muted/30 px-4 py-12">
      <div className="mx-auto w-full max-w-lg">
        <div className="text-center mb-6">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Guerrilla <span className="text-primary">CRM</span>
          </h1>
        </div>

        <Card className="p-6">
          <div className="mb-5 flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3">
            <ShieldAlert className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" />
            <div>
              <p className="text-sm font-semibold text-amber-700 dark:text-amber-300">This workspace requires two-factor authentication</p>
              <p className="mt-0.5 text-xs text-amber-700/90 dark:text-amber-200/90">
                Set it up now to continue{user?.email ? ` as ${user.email}` : ''}. Until you do, the rest of the app stays locked.
              </p>
            </div>
          </div>

          {/* The setup panel is the same one on the Security settings page — it's
              token-based like this card, and there is exactly one enrollment flow
              to maintain. */}
          <TwoFactorSetup forced onEnrolled={() => { window.location.href = '/'; }} />

          <Button
            variant="outline"
            className="mt-5 w-full"
            onClick={() => { void logout(); }}
          >
            Sign out
          </Button>
        </Card>
      </div>
    </div>
  );
}
