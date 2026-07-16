import { useState, useEffect } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { getInvitationPreview, type InvitationPreview } from '../lib/api';
import { useAuth } from '../lib/auth';
import { prettyRole } from '../lib/roles';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import LegalConsent from '../components/auth/LegalConsent';
import { Mail, CheckCircle2, XCircle, ArrowRight, Loader2, Clock, Ban } from 'lucide-react';
import { Button, Card, Input, Label, Spinner } from '@/components/ui';

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

  // One shared shell so every state renders inside the same card.
  const shell = (children: React.ReactNode) => (
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md animate-in fade-in slide-in-from-bottom-8 duration-700">
        <Card className="p-8">
          {children}
        </Card>
      </div>
    </div>
  );

  if (previewLoading) {
    return shell(
      <div className="flex flex-col items-center py-8">
        <Spinner size="lg" />
        <p className="text-sm text-muted-foreground mt-4">Loading your invitation…</p>
      </div>,
    );
  }

  // Dead states: no token, or the link is invalid/expired/revoked/accepted.
  const deadStatus = !token ? 'invalid' : preview?.status;
  if (deadStatus && deadStatus !== 'valid') {
    const config: Record<string, { icon: React.ReactNode; title: string; body: string; action?: () => void; actionLabel?: string }> = {
      invalid: {
        icon: <XCircle className="w-8 h-8 text-destructive" />,
        title: 'Invalid invitation',
        body: 'This link is broken or missing its invitation token. Ask whoever invited you to send a fresh link.',
      },
      expired: {
        icon: <Clock className="w-8 h-8 text-amber-600 dark:text-amber-400" />,
        title: 'This invitation has expired',
        body: `The invite${preview?.org_name ? ` to ${preview.org_name}` : ''} is no longer valid. Ask an admin to resend it — they can do that from their Members settings.`,
      },
      revoked: {
        icon: <Ban className="w-8 h-8 text-destructive" />,
        title: 'This invitation was revoked',
        body: `This invite${preview?.org_name ? ` to ${preview.org_name}` : ''} is no longer active. If you think this is a mistake, ask an admin to invite you again.`,
      },
      accepted: {
        icon: <CheckCircle2 className="w-8 h-8 text-emerald-600 dark:text-emerald-400" />,
        title: 'Already accepted',
        body: 'This invitation has already been used. You can sign in with your account.',
        action: () => navigate('/login'),
        actionLabel: 'Go to sign in',
      },
    };
    const c = config[deadStatus] ?? config.invalid;
    return shell(
      <div className="flex flex-col items-center text-center">
        <div className="w-16 h-16 bg-muted rounded-xl flex items-center justify-center mb-5">{c.icon}</div>
        <h1 className="text-lg font-semibold tracking-tight text-foreground mb-2">{c.title}</h1>
        <p className="text-sm text-muted-foreground mb-6">{c.body}</p>
        <button
          onClick={c.action ?? (() => navigate('/login'))}
          className="flex items-center gap-2 font-medium text-primary hover:underline"
        >
          {c.actionLabel ?? 'Go to sign in'} <ArrowRight className="w-4 h-4" />
        </button>
      </div>,
    );
  }

  if (status === 'success') {
    return shell(
      <div className="animate-in fade-in zoom-in duration-500 flex flex-col items-center">
        <div className="w-16 h-16 bg-emerald-500/10 rounded-full flex items-center justify-center mb-4">
          <CheckCircle2 className="w-8 h-8 text-emerald-600 dark:text-emerald-400" />
        </div>
        <h3 className="text-lg font-semibold tracking-tight text-foreground mb-2">
          {loggedIn ? `Welcome${preview?.org_name ? ` to ${preview.org_name}` : ''}!` : "You're in!"}
        </h3>
        <p className="text-sm text-muted-foreground text-center">
          {loggedIn ? 'Taking you in…' : 'Redirecting you to sign in…'}
        </p>
      </div>,
    );
  }

  // Valid invite → the join form. Header shows the workspace + role + email.
  return shell(
    <>
      <div className="flex justify-center mb-6">
        <div className="w-16 h-16 bg-primary/10 rounded-xl flex items-center justify-center">
          <Mail className="w-8 h-8 text-primary" />
        </div>
      </div>

      <h1 className="text-lg font-semibold tracking-tight text-center text-foreground mb-2">
        Join <span className="text-primary">{preview?.org_name || 'the workspace'}</span>
      </h1>
      <p className="text-sm text-muted-foreground text-center mb-1">
        You've been invited as <span className="font-semibold text-foreground">{prettyRole(preview?.role_name) || 'a member'}</span>.
      </p>
      {/* An existing account gets no form fields, so the invited address is shown
          here. A NEW invitee sees it as a real (read-only) Email field inside the
          form below instead — see the password-manager note there. */}
      {hasAccount && preview?.email && (
        <p className="text-sm text-muted-foreground text-center mb-6">{preview.email}</p>
      )}

      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => { e.preventDefault(); submit(true); }}
      >
        {/* role="alert" so a rejected password (too short, mismatched) is
            announced rather than only appearing visually. */}
        {errorMessage && (
          <div role="alert" className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 flex gap-2 text-sm text-destructive items-center animate-in slide-in-from-top-2">
            <XCircle className="w-4 h-4 shrink-0" />
            <span>{errorMessage}</span>
          </div>
        )}

        {hasAccount ? (
          // Existing account: no password needed — accepting adds the workspace
          // and signs them in (control of the invite link is the auth factor).
          <p className="text-sm text-muted-foreground text-center bg-muted rounded-lg p-3">
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
              <Label htmlFor="invite-email" className="mb-1.5">Email</Label>
              <Input
                id="invite-email"
                name="email"
                type="email"
                autoComplete="username"
                value={preview?.email ?? ''}
                readOnly
                className="cursor-not-allowed opacity-80"
              />
            </div>

            {/* Every field below was placeholder-only: no <label>, no id, no
                aria-label. A placeholder is not an accessible name (it vanishes on
                input and is skipped by many screen readers), so the form read as
                four anonymous boxes. */}
            <div className="flex gap-3">
              <div className="flex-1">
                <Label htmlFor="invite-first-name" className="mb-1.5">First name</Label>
                <Input id="invite-first-name" name="first_name" placeholder="Jane" value={firstName} onChange={e => setFirstName(e.target.value)} autoComplete="given-name" />
              </div>
              <div className="flex-1">
                <Label htmlFor="invite-last-name" className="mb-1.5">Last name</Label>
                <Input id="invite-last-name" name="last_name" placeholder="Doe" value={lastName} onChange={e => setLastName(e.target.value)} autoComplete="family-name" />
              </div>
            </div>
            <div>
              <Label htmlFor="invite-password" className="mb-1.5">Create a password</Label>
              <Input id="invite-password" name="new-password" type="password" placeholder="Min. 8 characters" value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" minLength={8} />
            </div>
            <div>
              <Label htmlFor="invite-confirm-password" className="mb-1.5">Confirm password</Label>
              <Input id="invite-confirm-password" name="confirm-password" type="password" placeholder="Re-enter your password" value={confirm} onChange={e => setConfirm(e.target.value)} autoComplete="new-password" />
            </div>
          </>
        )}

        <Button type="submit" disabled={status === 'loading'} className="w-full mt-1">
          {status === 'loading' ? (
            <><Loader2 className="animate-spin" /> Joining...</>
          ) : (
            <>{hasAccount ? 'Accept & continue' : 'Join workspace'} <ArrowRight /></>
          )}
        </Button>

        {!hasAccount && (
          <button
            type="button"
            disabled={status === 'loading'}
            onClick={() => submit(false)}
            className="w-full text-sm text-muted-foreground hover:text-foreground transition-colors py-2 disabled:opacity-50"
          >
            I'll sign in with Google instead
          </button>
        )}

        {/* Consent (U7.6) — the third account-creation surface. Shown only to a
            BRAND-NEW invitee: someone who already has an account agreed to these
            terms when they created it, and re-asking on a workspace join would be
            noise. Covers both paths above (set a password, or defer to Google). */}
        {!hasAccount && <LegalConsent className="mt-1" />}
      </form>
    </>,
  );
}
