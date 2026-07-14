import { useState, useEffect } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { getInvitationPreview, type InvitationPreview } from '../lib/api';
import { useAuth } from '../lib/auth';
import { prettyRole } from '../lib/roles';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import LegalConsent from '../components/auth/LegalConsent';
import { Mail, CheckCircle2, XCircle, ArrowRight, Loader2, Clock, Ban } from 'lucide-react';

// AcceptInvitePage (U4): reads the invite's public metadata first so the invitee
// sees "Join Acme as Sales Rep" (and their email) before committing, handles the
// dead states (expired / revoked / already-accepted / invalid) with tailored
// copy, and — on a successful accept — rides the server's auto-login straight
// into the app instead of bouncing to /login. Existing accounts skip the
// password fields (control of the invite link is the auth factor).
export default function AcceptInvitePage() {
  useDocumentTitle('Accept Invitation');
  const [searchParams] = useSearchParams();
  const token = searchParams.get('token');
  const navigate = useNavigate();
  const { acceptInvitation } = useAuth();

  const [preview, setPreview] = useState<InvitationPreview | null>(null);
  const [previewLoading, setPreviewLoading] = useState(true);
  const [status, setStatus] = useState<'idle' | 'loading' | 'success'>('idle');
  const [loggedIn, setLoggedIn] = useState(false);
  const [errorMessage, setErrorMessage] = useState('');

  // New non-OAuth invitees set a password here so they're no longer created
  // passwordless with no way in (P2). Leaving it blank + "Continue with Google"
  // joins without a password for accounts that will sign in via Google.
  const [firstName, setFirstName] = useState('');
  const [lastName, setLastName] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');

  useEffect(() => {
    if (!token) { setPreviewLoading(false); return; }
    let cancelled = false;
    getInvitationPreview(token)
      .then((p) => { if (!cancelled) setPreview(p); })
      .catch(() => { if (!cancelled) setPreview({ email: '', org_name: '', role_name: '', status: 'invalid', has_account: false }); })
      .finally(() => { if (!cancelled) setPreviewLoading(false); });
    return () => { cancelled = true; };
  }, [token]);

  const hasAccount = preview?.has_account ?? false;

  const submit = async (withPassword: boolean) => {
    if (!token) return;
    setErrorMessage('');
    if (withPassword && !hasAccount) {
      if (password.length < 8) {
        setErrorMessage('Password must be at least 8 characters.');
        return;
      }
      if (password !== confirm) {
        setErrorMessage('Passwords do not match.');
        return;
      }
    }
    setStatus('loading');
    try {
      const didLogIn = await acceptInvitation({
        token,
        password: withPassword && !hasAccount ? password : undefined,
        first_name: firstName || undefined,
        last_name: lastName || undefined,
      });
      setLoggedIn(didLogIn);
      setStatus('success');
      // Brand-new accounts are auto-logged-in → straight into the app; existing
      // accounts add the workspace and sign in normally → /login.
      setTimeout(() => navigate(didLogIn ? '/' : '/login?message=invitation-accepted'), didLogIn ? 900 : 2500);
    } catch (err: any) {
      setStatus('idle');
      setErrorMessage(err.message || 'Failed to accept invitation.');
    }
  };

  const inputCls =
    'w-full px-4 py-3 bg-neutral-800/50 border border-neutral-700 rounded-xl text-white placeholder-neutral-500 focus:outline-none focus:border-blue-500 transition-colors';
  const labelCls = 'block text-sm font-medium text-neutral-300 mb-1.5';

  // One shared shell so every state renders inside the same card.
  const shell = (children: React.ReactNode) => (
    <div className="min-h-screen bg-neutral-950 flex flex-col items-center justify-center p-4">
      <div className="absolute inset-0 overflow-hidden pointer-events-none">
        <div className="absolute -top-[30%] -left-[10%] w-[70%] h-[70%] rounded-full bg-purple-900/20 blur-[120px]" />
        <div className="absolute -bottom-[30%] -right-[10%] w-[70%] h-[70%] rounded-full bg-blue-900/20 blur-[120px]" />
      </div>
      <div className="relative z-10 w-full max-w-md animate-in fade-in slide-in-from-bottom-8 duration-700">
        <div className="bg-neutral-900/80 backdrop-blur-xl border border-neutral-800/50 rounded-3xl p-8 shadow-2xl overflow-hidden relative">
          {children}
        </div>
      </div>
    </div>
  );

  if (previewLoading) {
    return shell(
      <div className="flex flex-col items-center py-8">
        <Loader2 className="w-8 h-8 text-blue-400 animate-spin" />
        <p className="text-neutral-400 mt-4">Loading your invitation…</p>
      </div>,
    );
  }

  // Dead states: no token, or the link is invalid/expired/revoked/accepted.
  const deadStatus = !token ? 'invalid' : preview?.status;
  if (deadStatus && deadStatus !== 'valid') {
    const config: Record<string, { icon: React.ReactNode; title: string; body: string; action?: () => void; actionLabel?: string }> = {
      invalid: {
        icon: <XCircle className="w-8 h-8 text-red-400" />,
        title: 'Invalid invitation',
        body: 'This link is broken or missing its invitation token. Ask whoever invited you to send a fresh link.',
      },
      expired: {
        icon: <Clock className="w-8 h-8 text-amber-400" />,
        title: 'This invitation has expired',
        body: `The invite${preview?.org_name ? ` to ${preview.org_name}` : ''} is no longer valid. Ask an admin to resend it — they can do that from their Members settings.`,
      },
      revoked: {
        icon: <Ban className="w-8 h-8 text-red-400" />,
        title: 'This invitation was revoked',
        body: `This invite${preview?.org_name ? ` to ${preview.org_name}` : ''} is no longer active. If you think this is a mistake, ask an admin to invite you again.`,
      },
      accepted: {
        icon: <CheckCircle2 className="w-8 h-8 text-green-400" />,
        title: 'Already accepted',
        body: 'This invitation has already been used. You can sign in with your account.',
        action: () => navigate('/login'),
        actionLabel: 'Go to sign in',
      },
    };
    const c = config[deadStatus] ?? config.invalid;
    return shell(
      <div className="flex flex-col items-center text-center">
        <div className="w-16 h-16 bg-neutral-800/60 rounded-2xl flex items-center justify-center mb-5">{c.icon}</div>
        <h1 className="text-2xl font-bold text-white mb-2">{c.title}</h1>
        <p className="text-neutral-400 mb-6">{c.body}</p>
        <button
          onClick={c.action ?? (() => navigate('/login'))}
          className="flex items-center gap-2 text-blue-400 hover:text-blue-300 transition-colors font-medium"
        >
          {c.actionLabel ?? 'Go to sign in'} <ArrowRight className="w-4 h-4" />
        </button>
      </div>,
    );
  }

  if (status === 'success') {
    return shell(
      <div className="animate-in fade-in zoom-in duration-500 flex flex-col items-center">
        <div className="w-16 h-16 bg-green-500/20 rounded-full flex items-center justify-center mb-4 shadow-[0_0_30px_rgba(34,197,94,0.2)]">
          <CheckCircle2 className="w-8 h-8 text-green-400" />
        </div>
        <h3 className="text-xl font-semibold text-white mb-2">
          {loggedIn ? `Welcome${preview?.org_name ? ` to ${preview.org_name}` : ''}!` : "You're in!"}
        </h3>
        <p className="text-neutral-400 text-center">
          {loggedIn ? 'Taking you in…' : 'Redirecting you to sign in…'}
        </p>
      </div>,
    );
  }

  // Valid invite → the join form. Header shows the workspace + role + email.
  return shell(
    <>
      <div className="flex justify-center mb-6">
        <div className="w-16 h-16 bg-gradient-to-tr from-purple-500 to-blue-500 rounded-2xl flex items-center justify-center shadow-lg transform rotate-3 hover:rotate-6 transition-transform">
          <Mail className="w-8 h-8 text-white -rotate-3" />
        </div>
      </div>

      <h1 className="text-2xl font-bold text-center text-white mb-2 tracking-tight">
        Join <span className="text-transparent bg-clip-text bg-gradient-to-r from-purple-300 to-blue-300">{preview?.org_name || 'the workspace'}</span>
      </h1>
      <p className="text-neutral-400 text-center mb-1">
        You've been invited as <span className="font-semibold text-neutral-200">{prettyRole(preview?.role_name) || 'a member'}</span>.
      </p>
      {/* An existing account gets no form fields, so the invited address is shown
          here. A NEW invitee sees it as a real (read-only) Email field inside the
          form below instead — see the password-manager note there. */}
      {hasAccount && preview?.email && (
        <p className="text-neutral-500 text-center text-sm mb-6">{preview.email}</p>
      )}

      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => { e.preventDefault(); submit(true); }}
      >
        {/* role="alert" so a rejected password (too short, mismatched) is
            announced rather than only appearing visually. */}
        {errorMessage && (
          <div role="alert" className="bg-red-500/10 border border-red-500/20 rounded-xl p-3 flex gap-2 text-red-400 text-sm items-center animate-in slide-in-from-top-2">
            <XCircle className="w-4 h-4 shrink-0" />
            <span>{errorMessage}</span>
          </div>
        )}

        {hasAccount ? (
          // Existing account: no password needed — accepting adds the workspace
          // and signs them in (control of the invite link is the auth factor).
          <p className="text-sm text-neutral-400 text-center bg-neutral-800/40 rounded-xl p-3">
            You already have an account with this email. Accepting will add this workspace and sign you in.
          </p>
        ) : (
          <>
            {/* The account identifier. It was previously absent entirely — this
                page CREATES an account with a password, and a new-password field
                with no username field beside it gives a password manager nothing
                to bind the credential to, so it either declines to save or saves
                it against the wrong site. Read-only because the invite token, not
                the user, decides which address is being claimed. */}
            <div>
              <label htmlFor="invite-email" className={labelCls}>Email</label>
              <input
                id="invite-email"
                name="email"
                type="email"
                autoComplete="username"
                value={preview?.email ?? ''}
                readOnly
                className={`${inputCls} cursor-not-allowed opacity-80`}
              />
            </div>

            {/* Every field below was placeholder-only: no <label>, no id, no
                aria-label. A placeholder is not an accessible name (it vanishes on
                input and is skipped by many screen readers), so the form read as
                four anonymous boxes. */}
            <div className="flex gap-3">
              <div className="flex-1">
                <label htmlFor="invite-first-name" className={labelCls}>First name</label>
                <input id="invite-first-name" name="first_name" className={inputCls} placeholder="Jane" value={firstName} onChange={e => setFirstName(e.target.value)} autoComplete="given-name" />
              </div>
              <div className="flex-1">
                <label htmlFor="invite-last-name" className={labelCls}>Last name</label>
                <input id="invite-last-name" name="last_name" className={inputCls} placeholder="Doe" value={lastName} onChange={e => setLastName(e.target.value)} autoComplete="family-name" />
              </div>
            </div>
            <div>
              <label htmlFor="invite-password" className={labelCls}>Create a password</label>
              <input id="invite-password" name="new-password" className={inputCls} type="password" placeholder="Min. 8 characters" value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" minLength={8} />
            </div>
            <div>
              <label htmlFor="invite-confirm-password" className={labelCls}>Confirm password</label>
              <input id="invite-confirm-password" name="confirm-password" className={inputCls} type="password" placeholder="Re-enter your password" value={confirm} onChange={e => setConfirm(e.target.value)} autoComplete="new-password" />
            </div>
          </>
        )}

        <button
          type="submit"
          disabled={status === 'loading'}
          className="w-full relative group overflow-hidden rounded-xl bg-white text-neutral-950 font-semibold py-4 px-6 transition-all hover:scale-[1.02] active:scale-95 disabled:opacity-70 disabled:hover:scale-100 mt-1"
        >
          <div className="absolute inset-0 bg-gradient-to-r from-purple-200/50 to-blue-200/50 opacity-0 group-hover:opacity-100 transition-opacity" />
          <span className="relative flex items-center justify-center gap-2">
            {status === 'loading' ? (
              <><Loader2 className="w-5 h-5 animate-spin" /> Joining...</>
            ) : (
              <>{hasAccount ? 'Accept & continue' : 'Join workspace'} <ArrowRight className="w-5 h-5 group-hover:translate-x-1 transition-transform" /></>
            )}
          </span>
        </button>

        {!hasAccount && (
          <button
            type="button"
            disabled={status === 'loading'}
            onClick={() => submit(false)}
            className="w-full text-sm text-neutral-400 hover:text-neutral-200 transition-colors py-2 disabled:opacity-50"
          >
            I'll sign in with Google instead
          </button>
        )}

        {/* Consent (U7.6) — the third account-creation surface. Shown only to a
            BRAND-NEW invitee: someone who already has an account agreed to these
            terms when they created it, and re-asking on a workspace join would be
            noise. Covers both paths above (set a password, or defer to Google). */}
        {!hasAccount && <LegalConsent className="mt-1 text-neutral-500" />}
      </form>
    </>,
  );
}
