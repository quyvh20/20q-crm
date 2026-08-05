import React, { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { CheckCircle2, MailCheck, AlertCircle } from 'lucide-react';
import { Button, SpinnerBlock } from '@/components/ui';
import { fetchConfirmInfo, submitConfirm, type ConfirmInfo } from './publicConfirmApi';

// PUBLIC double-opt-in confirmation (R9), routed at /marketing/confirm/:token
// OUTSIDE ProtectedRoute/AppLayout — mirrors PreferenceCenterPage.tsx. Calls
// ONLY the public token endpoint; the click here is what promotes the address
// to mailable (marketing_status='subscribed'), so this page IS the consent
// mechanism, not just a confirmation of one that already happened.

type Phase = 'loading' | 'invalid' | 'ready' | 'confirming' | 'done' | 'error';

const Shell: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <div className="flex min-h-screen items-center justify-center bg-background px-4 py-12 text-foreground">
    <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 shadow-sm">{children}</div>
  </div>
);

export default function ConfirmSubscriptionPage() {
  const { token = '' } = useParams();
  const [phase, setPhase] = useState<Phase>('loading');
  const [info, setInfo] = useState<ConfirmInfo | null>(null);
  const [errMsg, setErrMsg] = useState('');

  useEffect(() => {
    let cancelled = false;
    fetchConfirmInfo(token)
      .then((i) => { if (!cancelled) { setInfo(i); setPhase('ready'); } })
      .catch(() => { if (!cancelled) setPhase('invalid'); });
    return () => { cancelled = true; };
  }, [token]);

  const orgLabel = info?.orgName ? info.orgName : 'this organization';

  const confirm = async () => {
    setPhase('confirming');
    setErrMsg('');
    try {
      await submitConfirm(token);
      setPhase('done');
    } catch (e) {
      setErrMsg(e instanceof Error ? e.message : 'Could not confirm your subscription.');
      setPhase('error');
    }
  };

  if (phase === 'loading') {
    return <Shell><SpinnerBlock label="Loading…" /></Shell>;
  }

  if (phase === 'invalid') {
    return (
      <Shell>
        <div className="text-center">
          <AlertCircle aria-hidden className="mx-auto h-10 w-10 text-muted-foreground" />
          <h1 className="mt-4 text-xl font-semibold">Link not valid</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            This confirmation link is invalid or has expired. If you're expecting an email from
            us, ask us to send a new confirmation link.
          </p>
        </div>
      </Shell>
    );
  }

  if (phase === 'done') {
    return (
      <Shell>
        <div className="text-center">
          <CheckCircle2 aria-hidden className="mx-auto h-10 w-10 text-emerald-500" />
          <h1 className="mt-4 text-xl font-semibold">Subscription confirmed</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            You'll now receive emails from {orgLabel}. You can unsubscribe at any time from the
            link in any email we send.
          </p>
        </div>
      </Shell>
    );
  }

  // ready | confirming | error
  return (
    <Shell>
      <div className="text-center">
        <MailCheck aria-hidden className="mx-auto h-10 w-10 text-muted-foreground" />
        <h1 className="mt-4 text-xl font-semibold">Confirm your subscription</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Click below to confirm you'd like to receive emails from {orgLabel}.
        </p>
      </div>

      {errMsg && (
        <div className="mt-4 flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-foreground">
          <AlertCircle aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
          {errMsg}
        </div>
      )}

      <div className="mt-6">
        <Button className="w-full" onClick={confirm} disabled={phase === 'confirming'}>
          {phase === 'confirming' ? 'Confirming…' : 'Confirm subscription'}
        </Button>
      </div>
    </Shell>
  );
}
