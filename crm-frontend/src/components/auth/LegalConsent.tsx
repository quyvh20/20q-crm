import { Link } from 'react-router-dom';
import { TERMS_URL, PRIVACY_URL, TERMS_IS_EXTERNAL, PRIVACY_IS_EXTERNAL } from '../../lib/legal';

// The passive-consent line shown on every surface that can CREATE an account
// (U7.6). There are three of them, not one: the email form on RegisterPage, the
// "Continue with Google" button (which is on RegisterPage AND LoginPage — for a
// new Google user it signs them up), and AcceptInvitePage, where a brand-new
// invitee sets their first password.
//
// The link target is configurable (VITE_TERMS_URL / VITE_PRIVACY_URL); when the
// operator hasn't set one we link the in-app placeholder route. External targets
// open in a new tab so a half-filled signup form isn't destroyed by navigating
// away — and carry rel="noopener noreferrer" because they're cross-origin.

interface LegalLinkProps {
  href: string;
  external: boolean;
  children: React.ReactNode;
}

const linkCls = 'text-primary underline underline-offset-2 hover:no-underline';

function LegalLink({ href, external, children }: LegalLinkProps) {
  if (external) {
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className={linkCls}>
        {children}
      </a>
    );
  }
  // Internal route: <Link> keeps it in the SPA (a raw <a> would full-reload and
  // throw away anything already typed into the signup form).
  return (
    <Link to={href} target="_blank" className={linkCls}>
      {children}
    </Link>
  );
}

interface LegalConsentProps {
  /** Extra classes for the wrapper (mostly spacing on the auth cards). */
  className?: string;
}

export default function LegalConsent({ className = '' }: LegalConsentProps) {
  return (
    <p className={`text-center text-xs leading-relaxed text-muted-foreground ${className}`}>
      By continuing, you agree to our{' '}
      <LegalLink href={TERMS_URL} external={TERMS_IS_EXTERNAL}>
        Terms of Service
      </LegalLink>{' '}
      and{' '}
      <LegalLink href={PRIVACY_URL} external={PRIVACY_IS_EXTERNAL}>
        Privacy Policy
      </LegalLink>
      .
    </p>
  );
}
