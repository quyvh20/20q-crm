import React, { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { AlertCircle, Copy, Folder, FolderInput, Loader2, Mail, Plus, Search, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import Modal from '../../components/common/Modal';
import { Badge, Button, EmptyState, Input, PageHeader, SpinnerBlock } from '@/components/ui';
import { useToast } from '@/lib/useToast';
import { useContentList, useCreateContent, useRemoveContent, useSetContentFolder } from './contentQueries';
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
  const moveMut = useSetContentFolder();
  const { confirm, dialog } = useConfirm();
  const toast = useToast();
  const [duplicatingId, setDuplicatingId] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  // '' = All; '~' = Unfiled (folder names are trimmed, so '~' can't collide).
  const [activeFolder, setActiveFolder] = useState('');
  const [moveTarget, setMoveTarget] = useState<CampaignContent | null>(null);
  const [newFolderName, setNewFolderName] = useState('');

  const rows = data ?? [];

  // Folders exist implicitly: the distinct non-empty labels, with counts.
  const folders = useMemo(() => {
    const counts = new Map<string, number>();
    let unfiled = 0;
    for (const r of rows) {
      const f = (r.folder ?? '').trim();
      if (f) counts.set(f, (counts.get(f) ?? 0) + 1);
      else unfiled += 1;
    }
    return {
      named: [...counts.entries()].sort((a, b) => a[0].localeCompare(b[0])),
      unfiled,
    };
  }, [rows]);

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    return rows.filter((r) => {
      const f = (r.folder ?? '').trim();
      if (activeFolder === '~' && f !== '') return false;
      if (activeFolder !== '' && activeFolder !== '~' && f !== activeFolder) return false;
      if (q === '') return true;
      return (
        r.name.toLowerCase().includes(q) ||
        (r.subject ?? '').toLowerCase().includes(q) ||
        f.toLowerCase().includes(q)
      );
    });
  }, [rows, search, activeFolder]);

  const moveTo = async (c: CampaignContent, folder: string) => {
    try {
      await moveMut.mutateAsync({ id: c.id, folder });
      setMoveTarget(null);
      setNewFolderName('');
      toast.show(folder ? `Moved to "${folder}"` : 'Moved to Unfiled');
    } catch (e) {
      toast.error((e as Error).message || 'Failed to move');
    }
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
      onSuccess: () => toast.show(`Deleted ${c.name}`),
      onError: (e) => toast.error((e as Error).message || 'Failed to delete'),
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
        folder: c.folder || undefined, // the copy stays in the same folder
      });
      toast.show(`Duplicated as "${created.name}"`);
    } catch (e) {
      toast.error((e as Error).message || 'Failed to duplicate');
    } finally {
      setDuplicatingId(null);
    }
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {dialog}
      <PageHeader
        title="Email templates"
        description="Design email-safe marketing content with merge tags. Templates compile to bulletproof HTML and are reused by campaigns and sequences."
        actions={<Button onClick={() => navigate('/marketing/content/new')}><Plus className="h-4 w-4" /> New template</Button>}
      />

      {/* Search + folder filter */}
      {rows.length > 0 && (
        <div className="mb-4 space-y-2">
          <div className="relative max-w-sm">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search templates…"
              aria-label="Search templates"
              className="pl-8"
            />
          </div>
          {(folders.named.length > 0 || folders.unfiled < rows.length) && (
            <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Folders">
              <FolderChip label="All" count={rows.length} active={activeFolder === ''} onClick={() => setActiveFolder('')} />
              {folders.unfiled > 0 && folders.named.length > 0 && (
                <FolderChip label="Unfiled" count={folders.unfiled} active={activeFolder === '~'} onClick={() => setActiveFolder('~')} />
              )}
              {folders.named.map(([name, count]) => (
                <FolderChip key={name} label={name} count={count} active={activeFolder === name} onClick={() => setActiveFolder(name)} icon />
              ))}
            </div>
          )}
        </div>
      )}

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
      ) : visible.length === 0 ? (
        <p className="py-12 text-center text-sm text-muted-foreground">No templates match — clear the search or pick another folder.</p>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {visible.map((c) => (
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
                <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                  {c.compiled_size_bytes > 0 && <Badge variant="secondary">{Math.round(c.compiled_size_bytes / 1024)} KB</Badge>}
                  {activeFolder === '' && (c.folder ?? '').trim() !== '' && (
                    <span className="flex items-center gap-0.5 truncate">
                      <Folder className="h-3 w-3 shrink-0" />
                      <span className="max-w-24 truncate">{c.folder}</span>
                    </span>
                  )}
                </span>
                <div className="flex items-center gap-1">
                  <button
                    type="button"
                    title="Move to folder"
                    aria-label={`Move ${c.name} to a folder`}
                    onClick={() => { setMoveTarget(c); setNewFolderName(''); }}
                    className="rounded-lg p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                  >
                    <FolderInput className="h-3.5 w-3.5" />
                  </button>
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

      {/* Move-to-folder modal */}
      <Modal open={moveTarget !== null} onClose={() => setMoveTarget(null)} title={`Move "${moveTarget?.name ?? ''}"`} size="sm">
        {moveTarget && (
          <div className="space-y-3">
            <div className="space-y-1">
              {(moveTarget.folder ?? '').trim() !== '' && (
                <button type="button" onClick={() => moveTo(moveTarget, '')}
                  className="flex w-full items-center gap-2 rounded-lg border border-border px-3 py-2 text-left text-sm text-foreground hover:border-ring hover:bg-accent">
                  <Mail className="h-4 w-4 text-muted-foreground" /> Unfiled (no folder)
                </button>
              )}
              {folders.named
                .filter(([name]) => name !== (moveTarget.folder ?? '').trim())
                .map(([name, count]) => (
                  <button key={name} type="button" onClick={() => moveTo(moveTarget, name)}
                    className="flex w-full items-center gap-2 rounded-lg border border-border px-3 py-2 text-left text-sm text-foreground hover:border-ring hover:bg-accent">
                    <Folder className="h-4 w-4 text-muted-foreground" />
                    <span className="min-w-0 flex-1 truncate">{name}</span>
                    <span className="shrink-0 text-[11px] text-muted-foreground">{count}</span>
                  </button>
                ))}
            </div>
            <div>
              <p className="mb-1 text-[11px] font-medium text-muted-foreground">Or a new folder</p>
              <div className="flex items-center gap-1.5">
                <Input
                  value={newFolderName}
                  onChange={(e) => setNewFolderName(e.target.value)}
                  maxLength={80}
                  placeholder="e.g. Newsletters"
                  aria-label="New folder name"
                  onKeyDown={(e) => { if (e.key === 'Enter' && newFolderName.trim()) moveTo(moveTarget, newFolderName.trim()); }}
                />
                <Button size="sm" disabled={!newFolderName.trim() || moveMut.isPending}
                  onClick={() => moveTo(moveTarget, newFolderName.trim())}>
                  Move
                </Button>
              </div>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
};

/** FolderChip is one filter pill in the folder bar. */
const FolderChip: React.FC<{ label: string; count: number; active: boolean; onClick: () => void; icon?: boolean }> = ({ label, count, active, onClick, icon }) => (
  <button
    type="button"
    aria-pressed={active}
    onClick={onClick}
    className={`flex items-center gap-1 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${
      active ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:border-ring hover:text-foreground'
    }`}
  >
    {icon && <Folder className="h-3 w-3" />}
    <span className="max-w-32 truncate">{label}</span>
    <span className={active ? 'text-primary/70' : 'text-muted-foreground/70'}>{count}</span>
  </button>
);

export default CampaignContentListPage;
