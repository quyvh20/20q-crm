import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Mail, Plus, Pencil, Trash2, Send, FileText } from 'lucide-react';
import { useEmailTemplates, useDeleteEmailTemplate, useTestSendEmailTemplate } from './queries';
import { WorkflowsTabs } from './WorkflowsTabs';
import type { EmailTemplate } from './api';

/** Email templates library (A5). Token-styled (consistent with the new builder),
 *  reached from the Workflows tab bar at /workflows/email-templates. */
export const EmailTemplatesPage: React.FC = () => {
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
    <div className="mx-auto max-w-5xl px-4 py-8">
      {toast && (
        <div
          className={`fixed right-4 top-4 z-50 flex items-center gap-3 rounded-xl px-4 py-3 text-sm font-medium text-white shadow-lg ${
            toast.type === 'error' ? 'bg-red-500/90' : 'bg-emerald-500/90'
          }`}
        >
          {toast.msg}
        </div>
      )}

      <WorkflowsTabs active="templates" />

      {/* Header */}
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Email Templates</h1>
          <p className="mt-1 text-sm text-muted-foreground">Reusable email content for your workflows' send-email actions</p>
        </div>
        <button
          onClick={() => navigate('/workflows/email-templates/new')}
          className="inline-flex items-center gap-1.5 rounded-xl bg-primary px-4 py-2.5 text-sm font-medium text-primary-foreground shadow-sm transition-colors hover:bg-primary/90"
        >
          <Plus className="h-4 w-4" /> New template
        </button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <div className="h-8 w-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      ) : templates.length === 0 ? (
        <div className="rounded-2xl border border-dashed border-border py-16 text-center">
          <Mail className="mx-auto mb-3 h-8 w-8 text-muted-foreground/60" />
          <p className="mb-1 text-lg text-foreground">No email templates yet</p>
          <p className="mb-4 text-sm text-muted-foreground">Create a template once and reuse it across workflows.</p>
          <button
            onClick={() => navigate('/workflows/email-templates/new')}
            className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Plus className="h-4 w-4" /> New template
          </button>
        </div>
      ) : (
        <div className="space-y-3">
          {templates.map((t) => (
            <div
              key={t.id}
              className="group flex items-center gap-4 rounded-xl border border-border bg-card p-4 transition-colors hover:border-ring/60"
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
              <div className="flex items-center gap-1.5 opacity-0 transition-opacity group-hover:opacity-100">
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
