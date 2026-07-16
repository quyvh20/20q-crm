import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { setAccessToken } from '../lib/api';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Spinner } from '@/components/ui';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

/**
 * Handles the Google OAuth callback redirect.
 *
 * The server has already set the refresh token as an httpOnly cookie; only the
 * short-lived access token arrives — in the URL FRAGMENT (P3), which never hits
 * server logs or the Referer header. We hold it in memory, verify it against
 * /api/auth/me, then redirect (to the chooser when the server flagged a multi-org
 * user via needs_chooser). On the subsequent full-page load the AuthProvider
 * re-establishes the session from the cookie.
 *
 * 2FA (U6.4) never reaches this page: Google proves the identity, not the second
 * factor, so a 2FA-enrolled user is redirected by the server straight to
 * /login/2fa with the challenge in an httpOnly cookie — there is no access token
 * to hand over yet. And a user whose workspace REQUIRES 2FA they haven't set up
 * does get a session here (they need one to enroll); their first protected request
 * 403s with `two_factor_required`, and apiFetch parks them on the enrollment page.
 */
export default function AuthCallbackPage() {
  useDocumentTitle('Signing In');
  const [searchParams] = useSearchParams();
  const processed = useRef(false);
  const [status, setStatus] = useState('Completing sign in...');

  useEffect(() => {
    // Guard against double-execution in StrictMode
    if (processed.current) return;
    processed.current = true;

    // The access token rides in the URL fragment; needs_chooser rides in the query.
    const hashParams = new URLSearchParams(window.location.hash.replace(/^#/, ''));
    const accessToken = hashParams.get('access_token');
    const needsChooser = searchParams.get('needs_chooser') === 'true';
    const destination = needsChooser ? '/choose-workspace' : '/';
    // Scrub the token out of the address bar / history immediately.
    if (window.history?.replaceState) {
      window.history.replaceState(null, '', window.location.pathname);
    }
    if (!accessToken) {
      console.error('[AuthCallback] Missing access token, redirecting to login');
      setStatus('Missing authentication token...');
      setTimeout(() => { window.location.replace('/login'); }, 1000);
      return;
    }

    // Access token in memory only; the refresh token is the httpOnly cookie.
    setAccessToken(accessToken);

    setStatus('Verifying session...');
    fetch(`${API_URL}/api/auth/me`, {
      credentials: 'include',
      headers: { Authorization: `Bearer ${accessToken}` },
    })
      .then(async (res) => {
        if (res.ok) {
          setStatus('Welcome! Redirecting...');
          setTimeout(() => { window.location.replace(destination); }, 200);
        } else {
          setAccessToken(null);
          const text = await res.text();
          console.error('[AuthCallback] Token verification failed:', res.status, text);
          setStatus(`Authentication failed (${res.status}). Redirecting...`);
          setTimeout(() => { window.location.replace('/login?error=token_verification_failed'); }, 2000);
        }
      })
      .catch((err) => {
        console.error('[AuthCallback] Network error during verification:', err);
        setStatus('Network issue, redirecting...');
        setTimeout(() => { window.location.replace('/'); }, 500);
      });
  }, [searchParams]);

  return (
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="text-center">
        <Spinner size="lg" className="mb-4" />
        <p className="text-sm text-muted-foreground">{status}</p>
      </div>
    </div>
  );
}
