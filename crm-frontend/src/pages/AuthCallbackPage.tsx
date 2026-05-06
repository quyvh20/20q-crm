import { useEffect, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

/**
 * Handles the Google OAuth callback redirect.
 * Extracts tokens from the URL query params and stores them.
 *
 * Uses window.location.replace() instead of React Router navigate() to
 * guarantee a full page reload so AuthProvider re-reads localStorage.
 * This avoids the race condition where navigate() + reload() fires before
 * the URL actually changes, causing the page to reload at /auth/callback
 * without query params and redirecting to /login.
 */
export default function AuthCallbackPage() {
  const [searchParams] = useSearchParams();
  const processed = useRef(false);

  useEffect(() => {
    // Guard against double-execution in StrictMode
    if (processed.current) return;
    processed.current = true;

    const accessToken = searchParams.get('access_token');
    const refreshToken = searchParams.get('refresh_token');

    if (accessToken && refreshToken) {
      localStorage.setItem('access_token', accessToken);
      localStorage.setItem('refresh_token', refreshToken);
      // Full page navigation so AuthProvider re-reads localStorage on mount
      window.location.replace('/');
    } else {
      window.location.replace('/login');
    }
  }, [searchParams]);

  return (
    <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
      <div className="text-center">
        <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full mx-auto mb-4"></div>
        <p className="text-slate-400">Completing sign in...</p>
      </div>
    </div>
  );
}
