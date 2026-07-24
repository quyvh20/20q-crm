import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertCircle, CheckCircle2, Mail, Plus, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { Badge, Button, EmptyState, PageHeader, SpinnerBlock } from '@/components/ui';
import { useContentList, useRemoveContent } from './contentQueries';
import type { CampaignContent } from './contentApi';

export const CampaignContentListPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-5xl"><AccessDeniedPanel capability="marketing.manage" what="email content" /></div>;
  }
  return <Content />;
};

const Content: React.FC = () => {
  const navigate = useNavigate();
  const { data, isLoading, isError } = useContentList();
  const removeMut = useRemoveContent();
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);

  const rows = data ?? [];

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 3500);
  };

  const del = (c: CampaignContent) => {
    if (!confirm(`Delete "${c.name}"? This can't be undone.`)) return;
    removeMut.mutate(c.id, {
      onSuccess: () => showToast(`Deleted ${c.name}`),
      onError: (e) => showToast((e as Error).message || 'Failed to delete', 'error'),
    });
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error' ? <AlertCircle className="h-4 w-4 text-destructive" /> : <CheckCircle2 className="h-4 w-4 text-primary" />} {toast.msg}
        </div>
      )}
      <PageHeader
        title="Email content"
        description="Design email-safe marketing content with merge tags. Content compiles to bulletproof HTML and is reused by campaigns."
        actions={<Button onClick={() => navigate('/marketing/content/new')}><Plus className="h-4 w-4" /> New content</Button>}
      />

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : isError ? (
        <div className="flex items-start gap-3 rounded-xl border border-destructive/30 bg-destructive/10 p-4 text-sm">
          <AlertCircle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
          <div>
            <p className="font-medium text-foreground">Couldn’t load email content</p>
            <p className="text-muted-foreground">Reload the page to try again — nothing has been lost.</p>
          </div>
        </div>
      ) : rows.length === 0 ? (
        <EmptyState icon={Mail} title="No email content yet" description="Create your first email to reuse across campaigns."
          action={<Button onClick={() => navigate('/marketing/content/new')}><Plus className="h-4 w-4" /> New content</Button>} />
      ) : (
        <div className="space-y-2">
          {rows.map((c) => (
            <div key={c.id} className="group flex items-center gap-4 rounded-xl border border-border bg-card p-4 transition-colors hover:border-ring/60">
              <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10"><Mail className="h-5 w-5 text-primary" /></span>
              <button className="min-w-0 flex-1 text-left" onClick={() => navigate(`/marketing/content/${c.id}`)}>
                <h3 className="truncate text-sm font-semibold text-foreground group-hover:text-primary">{c.name}</h3>
                <p className="mt-0.5 truncate text-xs text-muted-foreground">{c.subject || <span className="italic">No subject</span>}</p>
              </button>
              {c.compiled_size_bytes > 0 && <Badge variant="secondary">{Math.round(c.compiled_size_bytes / 1024)} KB</Badge>}
              <button onClick={() => del(c)} title="Delete" className="rounded-lg bg-destructive/10 px-2.5 py-1.5 text-destructive transition-colors hover:bg-destructive/20"><Trash2 className="h-3.5 w-3.5" /></button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default CampaignContentListPage;
