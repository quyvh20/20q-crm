import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertCircle, CheckCircle2, Copy, Loader2, Mail, Plus, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Badge, Button, EmptyState, PageHeader, SpinnerBlock } from '@/components/ui';
import { useContentList, useCreateContent, useRemoveContent } from './contentQueries';
import type { CampaignContent } from './contentApi';

export const CampaignContentListPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-5xl"><AccessDeniedPanel capability="marketing.manage" what="email content" /></div>;
  }
  return <Content />;
};

/** TemplateThumb is a true compiled-email preview: the stored HTML rendered in
 *  a scaled, sandboxed iframe. No screenshot service needed — the list API
 *  already carries body_html_compiled. pointer-events die on the wrapper so the
 *  whole card stays one click target. */
const TemplateThumb: React.FC<{ html: string; title: string }> = ({ html, title }) => {
  if (!html) {
    return (
      <div className="flex h-44 items-center justify-center bg-muted/30">
        <Mail className="h-8 w-8 text-muted-foreground/50" />
      </div>
    );
  }
  return (
    <div className="pointer-events-none relative h-44 overflow-hidden bg-white">
      <iframe
        title={`Preview of ${title}`}
        sandbox=""
        loading="lazy"
        tabIndex={-1}
        aria-hidden
        // 600px email scaled to ~29% ≈ card width; tall enough to fill the crop.
        srcDoc={`${html}<style>:root{color-scheme:light}</style>`}
        style={{ width: 600, height: 620, transform: 'scale(0.29)', transformOrigin: 'top left', border: 0 }}
      />
    </div>
  );
};

const Content: React.FC = () => {
  const navigate = useNavigate();
  const { data, isLoading, isError } = useContentList();
  const removeMut = useRemoveContent();
  const createMut = useCreateContent();
  const { confirm, dialog } = useConfirm();
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);
  const [duplicatingId, setDuplicatingId] = useState<string | null>(null);

  const rows = data ?? [];

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 3500);
  };

  const del = async (c: CampaignContent) => {
    const ok = await confirm({
      title: 'Delete template?',
      body: `"${c.name}" is removed for campaigns and sequences that would pick it in the future. This can't be undone.`,
      confirmLabel: 'Delete',
      tone: 'danger',
    });
    if (!ok) return;
    removeMut.mutate(c.id, {
      onSuccess: () => showToast(`Deleted ${c.name}`),
      onError: (e) => showToast((e as Error).message || 'Failed to delete', 'error'),
    });
  };

  // Duplicate goes through the normal create path — the copy re-validates and
  // re-compiles server-side, so it's a full, independent template.
  const duplicate = async (c: CampaignContent) => {
    setDuplicatingId(c.id);
    try {
      const created = await createMut.mutateAsync({
        name: `${c.name} (copy)`,
        subject: c.subject,
        preheader: c.preheader,
        body_json: c.body_json,
        merge_scope: c.merge_scope,
      });
      showToast(`Duplicated as "${created.name}"`);
    } catch (e) {
      showToast((e as Error).message || 'Failed to duplicate', 'error');
    } finally {
      setDuplicatingId(null);
    }
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {dialog}
      {toast && (
        <div role={toast.type === 'error' ? 'alert' : 'status'} className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error' ? <AlertCircle className="h-4 w-4 text-destructive" /> : <CheckCircle2 className="h-4 w-4 text-primary" />} {toast.msg}
        </div>
      )}
      <PageHeader
        title="Email templates"
        description="Design email-safe marketing content with merge tags. Templates compile to bulletproof HTML and are reused by campaigns and sequences."
        actions={<Button onClick={() => navigate('/marketing/content/new')}><Plus className="h-4 w-4" /> New template</Button>}
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
        <EmptyState icon={Mail} title="No email templates yet" description="Create your first email to reuse across campaigns."
          action={<Button onClick={() => navigate('/marketing/content/new')}><Plus className="h-4 w-4" /> New template</Button>} />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {rows.map((c) => (
            <div key={c.id} className="group overflow-hidden rounded-xl border border-border bg-card transition-colors hover:border-ring/60">
              <button
                type="button"
                onClick={() => navigate(`/marketing/content/${c.id}`)}
                className="block w-full text-left"
                aria-label={`Edit template ${c.name}`}
              >
                <TemplateThumb html={c.body_html_compiled} title={c.name} />
                <div className="border-t border-border p-3">
                  <h3 className="truncate text-sm font-semibold text-foreground group-hover:text-primary">{c.name}</h3>
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">{c.subject || <span className="italic">No subject</span>}</p>
                </div>
              </button>
              <div className="flex items-center justify-between border-t border-border px-3 py-2">
                <span className="text-[11px] text-muted-foreground">
                  {c.compiled_size_bytes > 0 && <Badge variant="secondary">{Math.round(c.compiled_size_bytes / 1024)} KB</Badge>}
                </span>
                <div className="flex items-center gap-1">
                  <button
                    type="button"
                    title="Duplicate template"
                    aria-label={`Duplicate ${c.name}`}
                    disabled={duplicatingId === c.id}
                    onClick={() => duplicate(c)}
                    className="rounded-lg p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"
                  >
                    {duplicatingId === c.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Copy className="h-3.5 w-3.5" />}
                  </button>
                  <button
                    type="button"
                    title="Delete template"
                    aria-label={`Delete ${c.name}`}
                    onClick={() => del(c)}
                    className="rounded-lg p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
};

export default CampaignContentListPage;
