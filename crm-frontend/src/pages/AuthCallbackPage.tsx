import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

/**
 * Handles the Google OAuth callback redirect.
 * Extracts tokens from the URL query params, validates them against
 * /api/auth/me, and only then redirects to the dashboard.
 * 
 * This ensures the session is fully bootstrapped before navigation,
 * avoiding race conditions with AuthProvider initialization.
 */
export default function AuthCallbackPage() {
  const [searchParams] = useSearchParams();
  const processed = useRef(false);
  const [status, setStatus] = useState('Completing sign in...');

  useEffect(() => {
    // Guard against double-execution in StrictMode
    if (processed.current) return;
    processed.current = true;

    const accessToken = searchParams.get('access_token');
    const refreshToken = searchParams.get('refresh_token');

    console.log('[AuthCallback] tokens present?', { access: !!accessToken, refresh: !!refreshToken });

    if (!accessToken || !refreshToken) {
      console.error('[AuthCallback] Missing tokens, redirecting to login');
      setStatus('Missing authentication tokens...');
      setTimeout(() => { window.location.replace('/login'); }, 1000);
      return;
    }

    // Store tokens first
    localStorage.setItem('access_token', accessToken);
    localStorage.setItem('refresh_token', refreshToken);
    console.log('[AuthCallback] Tokens saved to localStorage');

    // Verify the token works before redirecting
    setStatus('Verifying session...');
    fetch(`${API_URL}/api/auth/me`, {
      headers: { Authorization: `Bearer ${accessToken}` },
    })
      .then(async (res) => {
        console.log('[AuthCallback] /api/auth/me status:', res.status);
        if (res.ok) {
          const json = await res.json();
          console.log('[AuthCallback] Session verified for:', json.data?.user?.email);
          setStatus('Welcome back! Redirecting...');
          // Small delay to ensure localStorage is fully persisted
          setTimeout(() => { window.location.replace('/'); }, 200);
        } else {
          const text = await res.text();
          console.error('[AuthCallback] Token verification failed:', res.status, text);
          localStorage.removeItem('access_token');
          localStorage.removeItem('refresh_token');
          setStatus(`Authentication failed (${res.status}). Redirecting...`);
          setTimeout(() => { window.location.replace('/login?error=token_verification_failed'); }, 2000);
        }
      })
      .catch((err) => {
        console.error('[AuthCallback] Network error during verification:', err);
        // Tokens are saved, try redirecting anyway - AuthProvider will handle it
        setStatus('Network issue, redirecting...');
        setTimeout(() => { window.location.replace('/'); }, 500);
      });
  }, [searchParams]);

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
      <div className="text-center">
        <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full mx-auto mb-4"></div>
        <p className="text-slate-400">{status}</p>
      </div>
    </div>
  );
}
