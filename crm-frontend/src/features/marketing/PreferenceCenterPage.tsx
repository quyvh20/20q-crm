import React, { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { CheckCircle2, MailX, AlertCircle } from 'lucide-react';
import { Button, SpinnerBlock } from '@/components/ui';
import { fetchPreferenceInfo, submitUnsubscribe, type PreferenceInfo } from './publicUnsubApi';

// PUBLIC preference center (M3), routed at /u/:token OUTSIDE ProtectedRoute/AppLayout.
// It is reached by clicking the visible in-body unsubscribe link (the RFC-8058
// one-click header POSTs straight to the backend, bypassing this page). It renders
// its own full-screen chrome and calls ONLY the public token endpoint — never an
// authenticated /api/marketing/* route — and shows NO per-recipient subscription
// state (a forwarded token must not leak what someone is subscribed to).

type Phase = 'loading' | 'invalid' | 'ready' | 'done_all' | 'done_topic' | 'error';

const Shell: React.FC<{ children: React.ReactNode }> = ({ children }) => (
  <div className="flex min-h-screen items-center justify-center bg-background px-4 py-12 text-foreground">
    <div className="w-full max-w-md rounded-2xl border border-border bg-card p-8 shadow-sm">{children}</div>
  </div>
);

export const PreferenceCenterPage: React.FC = () => {
  const { token = '' } = useParams();
  const [phase, setPhase] = useState<Phase>('loading');
  const [info, setInfo] = useState<PreferenceInfo | null>(null);
  const [busy, setBusy] = useState(false);
  const [doneTopic, setDoneTopic] = useState('');
  const [errMsg, setErrMsg] = useState('');

  useEffect(() => {
    let cancelled = false;
    fetchPreferenceInfo(token)
      .then((i) => { if (!cancelled) { setInfo(i); setPhase('ready'); } })
      .catch(() => { if (!cancelled) setPhase('invalid'); });
    return () => { cancelled = true; };
  }, [token]);

  const orgLabel = info?.orgName ? info.orgName : 'this organization';

  const unsubAll = async () => {
    setBusy(true);
    setErrMsg('');
    try {
      await submitUnsubscribe(token);
      setPhase('done_all');
    } catch (e) {
      setErrMsg((e as Error).message);
      setPhase('error');
    } finally {
      setBusy(false);
    }
  };

  const unsubTopic = async (topicId: string, topicName: string) => {
    setBusy(true);
    setErrMsg('');
    try {
      await submitUnsubscribe(token, topicId);
      setDoneTopic(topicName);
      setPhase('done_topic');
    } catch (e) {
      setErrMsg((e as Error).message);
      setPhase('error');
    } finally {
      setBusy(false);
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
            This unsubscribe link is invalid or has expired. If you keep receiving unwanted email, reply to the
            message and ask to be removed.
          </p>
        </div>
      </Shell>
    );
  }

  if (phase === 'done_all') {
    return (
      <Shell>
        <div className="text-center">
          <CheckCircle2 aria-hidden className="mx-auto h-10 w-10 text-emerald-500" />
          <h1 className="mt-4 text-xl font-semibold">You’re unsubscribed</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            You will no longer receive marketing emails from {orgLabel}. It can take a short time for the change to
            take effect across in-flight sends.
          </p>
        </div>
      </Shell>
    );
  }

  if (phase === 'done_topic') {
    return (
      <Shell>
        <div className="text-center">
          <CheckCircle2 aria-hidden className="mx-auto h-10 w-10 text-emerald-500" />
          <h1 className="mt-4 text-xl font-semibold">Preference updated</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            You’ve been unsubscribed from <strong className="text-foreground">{doneTopic}</strong>. You may still
            receive other marketing emails from {orgLabel}.
          </p>
        </div>
      </Shell>
    );
  }

  // ready | error (error keeps the actionable UI so the user can retry)
  return (
    <Shell>
      <div className="text-center">
        <MailX aria-hidden className="mx-auto h-10 w-10 text-muted-foreground" />
        <h1 className="mt-4 text-xl font-semibold">Manage email preferences</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Choose what you’d like to receive from {orgLabel}.
        </p>
      </div>

      {errMsg && (
        <div className="mt-4 flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-foreground">
          <AlertCircle aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-destructive" />
          {errMsg}
        </div>
      )}

      <div className="mt-6">
        <Button variant="destructive" className="w-full" onClick={unsubAll} disabled={busy}>
          Unsubscribe from all marketing emails
        </Button>
      </div>

      {info?.topics && info.topics.length > 0 && (
        <div className="mt-6 border-t border-border pt-6">
          <p className="text-sm font-medium text-foreground">Or opt out of specific topics</p>
          <ul className="mt-3 space-y-3">
            {info.topics.map((t) => (
              <li key={t.id} className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground">{t.name}</p>
                  {t.description && <p className="truncate text-xs text-muted-foreground">{t.description}</p>}
                </div>
                <Button variant="outline" size="sm" onClick={() => unsubTopic(t.id, t.name)} disabled={busy}>
                  Unsubscribe
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Shell>
  );
};

export default PreferenceCenterPage;
