import { useState } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { acceptInvite } from '../lib/api';
import { Mail, CheckCircle2, XCircle, ArrowRight, Loader2 } from 'lucide-react';

export default function AcceptInvitePage() {
  const [searchParams] = useSearchParams();
  const token = searchParams.get('token');
  const navigate = useNavigate();

  const [status, setStatus] = useState<'idle' | 'loading' | 'success' | 'error'>('idle');
  const [errorMessage, setErrorMessage] = useState('');

  const handleAccept = async () => {
    if (!token) return;
    setStatus('loading');
    try {
      await acceptInvite(token);
      setStatus('success');
      setTimeout(() => {
        navigate('/login?message=invitation-accepted');
      }, 3000);
    } catch (err: any) {
      setStatus('error');
      setErrorMessage(err.message || 'Failed to accept invitation.');
    }
  };

  return (
    <div className="min-h-screen bg-neutral-950 flex flex-col items-center justify-center p-4">
      {/* Dynamic Background */}
      <div className="absolute inset-0 overflow-hidden pointer-events-none">
        <div className="absolute -top-[30%] -left-[10%] w-[70%] h-[70%] rounded-full bg-purple-900/20 blur-[120px]" />
        <div className="absolute -bottom-[30%] -right-[10%] w-[70%] h-[70%] rounded-full bg-blue-900/20 blur-[120px]" />
      </div>

      <div className="relative z-10 w-full max-w-md animate-in fade-in slide-in-from-bottom-8 duration-700">
        <div className="bg-neutral-900/80 backdrop-blur-xl border border-neutral-800/50 rounded-3xl p-8 shadow-2xl overflow-hidden relative">
          
          {/* Header */}
          <div className="flex justify-center mb-8">
            <div className="w-16 h-16 bg-gradient-to-tr from-purple-500 to-blue-500 rounded-2xl flex items-center justify-center shadow-lg transform rotate-3 hover:rotate-6 transition-transform">
              <Mail className="w-8 h-8 text-white -rotate-3" />
            </div>
          </div>

          <h1 className="text-3xl font-bold text-center text-white mb-2 tracking-tight">You've been invited!</h1>
          <p className="text-neutral-400 text-center mb-8">
            Join the workspace to start collaborating.
          </p>

          {!token ? (
            <div className="bg-red-500/10 border border-red-500/20 rounded-2xl p-4 flex items-start gap-3">
              <XCircle className="w-6 h-6 text-red-400 shrink-0 mt-0.5" />
              <div>
                <h3 className="text-red-200 font-medium">Invalid Invitation Link</h3>
                <p className="text-red-400 text-sm mt-1">This link appears to be broken or missing the invitation token. Please request a new link.</p>
              </div>
            </div>
          ) : status === 'success' ? (
            <div className="animate-in fade-in zoom-in duration-500 flex flex-col items-center">
              <div className="w-16 h-16 bg-green-500/20 rounded-full flex items-center justify-center mb-4 shadow-[0_0_30px_rgba(34,197,94,0.2)]">
                <CheckCircle2 className="w-8 h-8 text-green-400" />
              </div>
              <h3 className="text-xl font-semibold text-white mb-2">Invitation Accepted</h3>
              <p className="text-neutral-400 text-center mb-6">You will be redirected to the login page momentarily.</p>
              <button
                onClick={() => navigate('/login')}
                className="flex items-center gap-2 text-blue-400 hover:text-blue-300 transition-colors font-medium"
              >
                Go to login <ArrowRight className="w-4 h-4" />
              </button>
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              {status === 'error' && (
                <div className="bg-red-500/10 border border-red-500/20 rounded-xl p-3 flex gap-2 text-red-400 text-sm items-center animate-in slide-in-from-top-2">
                  <XCircle className="w-4 h-4 shrink-0" />
                  <span>{errorMessage}</span>
                </div>
              )}
              
              <button
                onClick={handleAccept}
                disabled={status === 'loading'}
                className="w-full relative group overflow-hidden rounded-xl bg-white text-neutral-950 font-semibold py-4 px-6 transition-all hover:scale-[1.02] active:scale-95 disabled:opacity-70 disabled:hover:scale-100"
              >
                <div className="absolute inset-0 bg-gradient-to-r from-purple-200/50 to-blue-200/50 opacity-0 group-hover:opacity-100 transition-opacity" />
                <span className="relative flex items-center justify-center gap-2">
                  {status === 'loading' ? (
                    <>
                      <Loader2 className="w-5 h-5 animate-spin" />
                      Accepting...
                    </>
                  ) : (
                    <>
                      Accept Invitation <ArrowRight className="w-5 h-5 group-hover:translate-x-1 transition-transform" />
                    </>
                  )}
                </span>
              </button>
            </div>
          )}

        </div>
      </div>
    </div>
  );
}
