import React, { useState } from 'react';
import { AlertCircle, Bookmark, Check, CheckCircle2, Pencil, Trash2, X } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Button, EmptyState, Input, PageHeader, SpinnerBlock } from '@/components/ui';
import { PALETTE_ICONS } from './composer/EmailBuilder';
import { PALETTE } from './composer/blocks';
import type { SavedBlockRow } from './savedBlocksApi';
import { useRemoveSavedBlock, useRenameSavedBlock, useSavedBlocks } from './savedBlocksQueries';

/** SavedBlocksPage manages the reusable-block library. Blocks are CREATED from
 *  the builder (bookmark a block); here they're renamed and pruned. */
export const SavedBlocksPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-4xl"><AccessDeniedPanel capability="marketing.manage" what="saved blocks" /></div>;
  }
  return <Library />;
};

const Library: React.FC = () => {
  const navigate = useNavigate();
  const blocks = useSavedBlocks();
  const rename = useRenameSavedBlock();
  const remove = useRemoveSavedBlock();
  const { confirm, dialog } = useConfirm();
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editName, setEditName] = useState('');

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(null), 3000);
  };

  const startRename = (row: SavedBlockRow) => {
    setEditingId(row.id);
    setEditName(row.name);
  };

  const commitRename = async () => {
    if (!editingId || !editName.trim()) return;
    try {
      await rename.mutateAsync({ id: editingId, name: editName.trim() });
      setEditingId(null);
      showToast('Renamed');
    } catch (err) {
      setError((err as Error).message || 'Rename failed');
    }
  };

  const onDelete = async (row: SavedBlockRow) => {
    const ok = await confirm({
      title: 'Delete saved block?',
      body: `"${row.name}" is removed from the library. Emails already using copies of it are unaffected.`,
      confirmLabel: 'Delete',
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await remove.mutateAsync(row.id);
      showToast('Deleted');
    } catch (err) {
      setError((err as Error).message || 'Delete failed');
    }
  };

  const typeLabel = (t: string) => PALETTE.find((p) => p.type === t)?.label ?? t;
  const typeIcon = (t: string) => {
    const iconName = PALETTE.find((p) => p.type === t)?.icon ?? 'Type';
    return PALETTE_ICONS[iconName] ?? PALETTE_ICONS.Type;
  };

  return (
    <div className="mx-auto w-full max-w-4xl">
      {dialog}
      {toast && (
        <div role="status" className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          <CheckCircle2 className="h-4 w-4 text-primary" /> {toast}
        </div>
      )}

      <PageHeader
        title="Saved blocks"
        description="Reusable pieces for the email builder. Save any block from its settings (the bookmark icon); insert them from the builder’s left rail."
      />

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" /> {error}
        </div>
      )}

      {blocks.isLoading ? (
        <SpinnerBlock label="Loading saved blocks…" />
      ) : blocks.isError ? (
        <p className="py-12 text-center text-sm text-destructive">Couldn’t load saved blocks.</p>
      ) : (blocks.data?.length ?? 0) === 0 ? (
        <EmptyState
          icon={Bookmark}
          title="No saved blocks yet"
          description="Build a block you like once — a styled button, a footer section, a product card — then bookmark it from its settings panel to reuse it in every email."
          action={<Button onClick={() => navigate('/marketing/content/new')}>Open the builder</Button>}
        />
      ) : (
        <div className="space-y-2">
          {blocks.data!.map((row) => {
            const Icon = typeIcon(row.block.type);
            const editing = editingId === row.id;
            return (
              <div key={row.id} className="flex items-center gap-3 rounded-xl border border-border bg-card px-4 py-3">
                <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                  <Icon className="h-4 w-4" />
                </span>
                <div className="min-w-0 flex-1">
                  {editing ? (
                    <div className="flex items-center gap-1.5">
                      <Input
                        value={editName}
                        onChange={(e) => setEditName(e.target.value)}
                        maxLength={120}
                        onKeyDown={(e) => { if (e.key === 'Enter') commitRename(); if (e.key === 'Escape') setEditingId(null); }}
                        className="h-8 max-w-xs"
                        aria-label="Saved block name"
                      />
                      <button type="button" title="Save name" aria-label="Save name" onClick={commitRename}
                        className="rounded p-1.5 text-primary hover:bg-primary/10"><Check className="h-4 w-4" /></button>
                      <button type="button" title="Cancel" aria-label="Cancel rename" onClick={() => setEditingId(null)}
                        className="rounded p-1.5 text-muted-foreground hover:bg-accent"><X className="h-4 w-4" /></button>
                    </div>
                  ) : (
                    <p className="truncate text-sm font-medium text-foreground">{row.name}</p>
                  )}
                  <p className="mt-0.5 text-[11px] text-muted-foreground">
                    {typeLabel(row.block.type)} · saved {new Date(row.created_at).toLocaleDateString()}
                  </p>
                </div>
                {!editing && (
                  <div className="flex shrink-0 items-center gap-1">
                    <button type="button" title="Rename" aria-label={`Rename ${row.name}`} onClick={() => startRename(row)}
                      className="rounded-lg p-2 text-muted-foreground hover:bg-accent hover:text-foreground"><Pencil className="h-4 w-4" /></button>
                    <button type="button" title="Delete" aria-label={`Delete ${row.name}`} onClick={() => onDelete(row)}
                      className="rounded-lg p-2 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"><Trash2 className="h-4 w-4" /></button>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

export default SavedBlocksPage;
