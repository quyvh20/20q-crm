import { ShieldAlert } from 'lucide-react';
import { useAuth } from '../lib/auth';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import TwoFactorSetup from '../components/settings/TwoFactorSetup';

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
    <div className="min-h-screen bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900 py-12 px-4">
      <div className="mx-auto w-full max-w-lg">
        <div className="text-center mb-6">
          <h1 className="text-2xl font-bold text-white tracking-tight">
            Guerrilla <span className="text-blue-400">CRM</span>
          </h1>
        </div>

        <div className="rounded-2xl border border-slate-700/50 bg-slate-800/50 p-6 shadow-2xl backdrop-blur-xl">
          <div className="mb-5 flex items-start gap-3 rounded-xl border border-amber-500/30 bg-amber-500/10 p-3">
            <ShieldAlert className="mt-0.5 h-5 w-5 shrink-0 text-amber-400" />
            <div>
              <p className="text-sm font-semibold text-amber-200">This workspace requires two-factor authentication</p>
              <p className="mt-0.5 text-xs text-amber-200/80">
                Set it up now to continue{user?.email ? ` as ${user.email}` : ''}. Until you do, the rest of the app stays locked.
              </p>
            </div>
          </div>

          {/* The setup panel is the same one on the Security settings page — the
              light theme tokens read fine on the card, and there is exactly one
              enrollment flow to maintain. */}
          <div className="rounded-xl bg-background p-4 text-foreground">
            <TwoFactorSetup forced onEnrolled={() => { window.location.href = '/'; }} />
          </div>

          <button
            onClick={() => { void logout(); }}
            className="mt-5 w-full rounded-xl border border-slate-700 px-4 py-2 text-sm text-slate-300 transition-colors hover:bg-slate-700/40"
          >
            Sign out
          </button>
        </div>
      </div>
    </div>
  );
}
