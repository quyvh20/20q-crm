import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertCircle, CheckCircle2, Mail, Plus, Pencil, Trash2, Send, FileText } from 'lucide-react';
import { useEmailTemplates, useDeleteEmailTemplate, useTestSendEmailTemplate } from './queries';
import { WorkflowsTabs } from './WorkflowsTabs';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { Button, EmptyState, PageHeader, SpinnerBlock } from '@/components/ui';
import type { EmailTemplate } from './api';

/** Email templates library (A5). Token-styled (consistent with the new builder),
 *  reached from the Workflows tab bar at /workflows/email-templates.
 *
 *  Every /api/workflows/email-templates* route — including the list GET —
 *  requires workflows.manage, so the whole page is gated (U3): a member
 *  without the capability gets the friendly denied panel instead of a surface
 *  that 403s. The gate wraps the content component so the denied path never
 *  fires the list query. Wait for the capability fetch to settle before
 *  deciding, so a deep-linked manager doesn't flash the denied panel (the
 *  SettingsLayout trap). */
export const EmailTemplatesPage: React.FC = () => {
  const { can, loaded } = usePermissions();

  if (!loaded) {
    return (
      <div className="mx-auto w-full max-w-6xl">
        <SpinnerBlock label="Loading…" />
      </div>
    );
  }

  if (!can('workflows.manage')) {
    return (
      <div className="mx-auto w-full max-w-6xl">
        <AccessDeniedPanel capability="workflows.manage" what="email templates" />
      </div>
    );
  }

  return <EmailTemplatesContent />;
};

const EmailTemplatesContent: React.FC = () => {
  const navigate = useNavigate();
  const { data, isLoading } = useEmailTemplates();
  const deleteMutation = useDeleteEmailTemplate();
  const testSend = useTestSendEmailTemplate();
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);

  const templates = data?.templates ?? [];

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const handleDelete = (t: EmailTemplate) => {
    if (!confirm(`Delete "${t.name}"? This can't be undone.`)) return;
    deleteMutation.mutate(t.id, {
      onError: (e) => showToast((e as Error).message || 'Failed to delete template', 'error'),
    });
  };

  const handleTestSend = (t: EmailTemplate) => {
    testSend.mutate(t.id, {
      onSuccess: (r) => showToast(`Test email sent to ${r.to}`),
      onError: (e) => showToast((e as Error).message || 'Failed to send test email', 'error'),
    });
  };

  return (
    <div className="mx-auto w-full max-w-6xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error' ? (
            <AlertCircle aria-hidden className="h-4 w-4 shrink-0 text-destructive" />
          ) : (
            <CheckCircle2 aria-hidden className="h-4 w-4 shrink-0 text-primary" />
          )}
          {toast.msg}
        </div>
      )}

      <WorkflowsTabs active="templates" />

      {/* Header */}
      <PageHeader
        title="Email Templates"
        description="Reusable email content for your workflows' send-email actions"
        actions={
          <Button onClick={() => navigate('/workflows/email-templates/new')}>
            <Plus aria-hidden /> New template
          </Button>
        }
      />

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : templates.length === 0 ? (
        <EmptyState
          icon={Mail}
          title="No email templates yet"
          description="Create a template once and reuse it across workflows."
          action={
            <Button onClick={() => navigate('/workflows/email-templates/new')}>
              <Plus aria-hidden /> New template
            </Button>
          }
        />
      ) : (
        <div className="space-y-3">
          {templates.map((t) => (
            <div
              key={t.id}
              className="group flex flex-wrap items-center gap-4 rounded-xl border border-border bg-card p-4 transition-colors hover:border-ring/60"
            >
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
                <FileText className="h-5 w-5 text-primary" />
              </span>
              <button className="min-w-0 flex-1 text-left" onClick={() => navigate(`/workflows/email-templates/${t.id}`)}>
                <h3 className="truncate text-sm font-semibold text-foreground group-hover:text-primary">{t.name}</h3>
                <div className="mt-0.5 flex items-center gap-2">
                  <span className="truncate text-xs text-muted-foreground">{t.subject || <span className="italic">No subject</span>}</span>
                  {t.object_slug && (
                    <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                      {t.object_slug}
                    </span>
                  )}
                </div>
              </button>
              <div className="flex flex-wrap items-center gap-1.5 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:focus-within:opacity-100">
                <button
                  onClick={() => handleTestSend(t)}
                  disabled={testSend.isPending}
                  title="Send a test to yourself"
                  className="inline-flex items-center gap-1 rounded-lg bg-muted px-2.5 py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground disabled:opacity-50"
                >
                  <Send className="h-3.5 w-3.5" /> Test
                </button>
                <button
                  onClick={() => navigate(`/workflows/email-templates/${t.id}`)}
                  title="Edit"
                  className="inline-flex items-center gap-1 rounded-lg bg-muted px-2.5 py-1.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
                >
                  <Pencil className="h-3.5 w-3.5" /> Edit
                </button>
                <button
                  onClick={() => handleDelete(t)}
                  title="Delete"
                  className="inline-flex items-center gap-1 rounded-lg bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive transition-colors hover:bg-destructive/20"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default EmailTemplatesPage;
