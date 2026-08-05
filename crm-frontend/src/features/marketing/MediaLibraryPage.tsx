import React, { useMemo, useRef, useState } from 'react';
import { AlertCircle, Check, Copy, Folder, FolderInput, ImagePlus, Loader2, Search, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import Modal from '../../components/common/Modal';
import { Button, EmptyState, Input, PageHeader, SpinnerBlock } from '@/components/ui';
import { useToast } from '@/lib/useToast';
import { copyText } from '../../lib/clipboard';
import { displayImageSrc, type MarketingAsset } from './assetsApi';
import { useAssets, useDuplicateAsset, useRemoveAsset, useSetAssetFolder, useUploadAsset } from './assetsQueries';

/** folderIndex derives the implicit folder set (same doctrine as templates). */
export function folderIndex(rows: { folder?: string }[]) {
  const counts = new Map<string, number>();
  let unfiled = 0;
  for (const r of rows) {
    const f = (r.folder ?? '').trim();
    if (f) counts.set(f, (counts.get(f) ?? 0) + 1);
    else unfiled += 1;
  }
  return { named: [...counts.entries()].sort((a, b) => a[0].localeCompare(b[0])), unfiled };
}

/** MediaLibraryPage is the standalone image library (the builder's picker uses
 *  the same store): upload once, reuse in every email, copy public URLs for use
 *  anywhere else. */
export const MediaLibraryPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-5xl"><AccessDeniedPanel capability="marketing.manage" what="the media library" /></div>;
  }
  return <Library />;
};

const Library: React.FC = () => {
  const assets = useAssets();
  const upload = useUploadAsset();
  const remove = useRemoveAsset();
  const { confirm, dialog } = useConfirm();
  const moveMut = useSetAssetFolder();
  const dupMut = useDuplicateAsset();
  const [duplicatingId, setDuplicatingId] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const toast = useToast();
  const [search, setSearch] = useState('');
  // '' = All; '~' = Unfiled (folder names are trimmed, so '~' can't collide).
  const [activeFolder, setActiveFolder] = useState('');
  const [moveTarget, setMoveTarget] = useState<MarketingAsset | null>(null);
  const [newFolderName, setNewFolderName] = useState('');

  const rows = assets.data ?? [];
  const folders = useMemo(() => folderIndex(rows), [rows]);
  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    return rows.filter((a) => {
      const f = (a.folder ?? '').trim();
      if (activeFolder === '~' && f !== '') return false;
      if (activeFolder !== '' && activeFolder !== '~' && f !== activeFolder) return false;
      if (q === '') return true;
      return a.filename.toLowerCase().includes(q) || f.toLowerCase().includes(q);
    });
  }, [rows, search, activeFolder]);

  const duplicate = async (a: MarketingAsset) => {
    setDuplicatingId(a.id);
    try {
      const created = await dupMut.mutateAsync(a.id);
      toast.show(`Duplicated as "${created.filename}"`);
    } catch (err) {
      setError((err as Error).message || 'Failed to duplicate');
    } finally {
      setDuplicatingId(null);
    }
  };

  const moveTo = async (a: MarketingAsset, folder: string) => {
    try {
      await moveMut.mutateAsync({ id: a.id, folder });
      setMoveTarget(null);
      setNewFolderName('');
      toast.show(folder ? `Moved to "${folder}"` : 'Moved to Unfiled');
    } catch (err) {
      setError((err as Error).message || 'Failed to move');
    }
  };

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setError(null);
    try {
      // Uploading while a folder is active files the image there directly.
      const folder = activeFolder !== '' && activeFolder !== '~' ? activeFolder : '';
      await upload.mutateAsync({ file, folder });
      toast.show(folder ? `Uploaded to "${folder}"` : 'Image uploaded');
    } catch (err) {
      setError((err as Error).message || 'Upload failed');
    }
  };

  const copyUrl = async (id: string, url: string) => {
    if (await copyText(url)) {
      setCopiedId(id);
      setTimeout(() => setCopiedId(null), 1500);
    } else {
      setError('Clipboard unavailable — copy the URL from the image itself.');
    }
  };

  const onDelete = async (id: string, filename: string) => {
    const ok = await confirm({
      title: 'Delete image?',
      body: `Emails already sent keep pointing at "${filename}" — their copies will stop loading. Drafts using it will show a broken image.`,
      confirmLabel: 'Delete',
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await remove.mutateAsync(id);
      toast.show('Image deleted');
    } catch (err) {
      setError((err as Error).message || 'Delete failed');
    }
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {dialog}
      <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="hidden" onChange={onFile} aria-label="Upload image file" />

      <PageHeader
        title="Media library"
        description="Images for your emails — upload once, reuse everywhere. PNG, JPEG, GIF or WebP, up to 2 MB."
        actions={
          <Button onClick={() => fileRef.current?.click()} disabled={upload.isPending}>
            {upload.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <ImagePlus className="h-4 w-4" />}
            Upload image
          </Button>
        }
      />

      {error && (
        <div className="mb-4 flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" /> {error}
        </div>
      )}

      {/* Search + folder filter */}
      {rows.length > 0 && (
        <div className="mb-4 space-y-2">
          <div className="relative max-w-sm">
            <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search images…" aria-label="Search images" className="pl-8" />
          </div>
          {folders.named.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Folders">
              <FolderChip label="All" count={rows.length} active={activeFolder === ''} onClick={() => setActiveFolder('')} />
              {folders.unfiled > 0 && (
                <FolderChip label="Unfiled" count={folders.unfiled} active={activeFolder === '~'} onClick={() => setActiveFolder('~')} />
              )}
              {folders.named.map(([name, count]) => (
                <FolderChip key={name} label={name} count={count} active={activeFolder === name} onClick={() => setActiveFolder(name)} icon />
              ))}
            </div>
          )}
        </div>
      )}

      {assets.isLoading ? (
        <SpinnerBlock label="Loading images…" />
      ) : assets.isError ? (
        <p className="py-12 text-center text-sm text-destructive">Couldn’t load the media library.</p>
      ) : rows.length === 0 ? (
        <EmptyState
          icon={ImagePlus}
          title="No images yet"
          description="Upload your logo, product shots and banners — they’ll be one click away in the email builder."
          action={<Button onClick={() => fileRef.current?.click()}>Upload your first image</Button>}
        />
      ) : visible.length === 0 ? (
        <p className="py-12 text-center text-sm text-muted-foreground">No images match — clear the search or pick another folder.</p>
      ) : (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
          {visible.map((a) => (
            <div key={a.id} className="group overflow-hidden rounded-xl border border-border bg-card">
              <div className="flex h-36 items-center justify-center bg-muted/30 p-2">
                <img src={displayImageSrc(a.url)} alt={a.filename} loading="lazy" className="max-h-full max-w-full object-contain" draggable={false} />
              </div>
              <div className="border-t border-border p-2.5">
                <p className="truncate text-xs font-medium text-foreground" title={a.filename}>{a.filename}</p>
                <p className="mt-0.5 flex items-center gap-1 text-[11px] text-muted-foreground">
                  {Math.max(1, Math.round(a.size_bytes / 1024))} KB · {new Date(a.created_at).toLocaleDateString()}
                  {activeFolder === '' && (a.folder ?? '').trim() !== '' && (
                    <span className="flex min-w-0 items-center gap-0.5"><Folder className="h-3 w-3 shrink-0" /><span className="truncate">{a.folder}</span></span>
                  )}
                </p>
                <div className="mt-2 flex items-center gap-1">
                  <button
                    type="button"
                    title="Duplicate image"
                    aria-label={`Duplicate ${a.filename}`}
                    disabled={duplicatingId === a.id}
                    onClick={() => duplicate(a)}
                    className="rounded-lg border border-border p-1.5 text-muted-foreground hover:border-ring hover:text-foreground disabled:opacity-50"
                  >
                    {duplicatingId === a.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Copy className="h-3 w-3" />}
                  </button>
                  <button
                    type="button"
                    title="Move to folder"
                    aria-label={`Move ${a.filename} to a folder`}
                    onClick={() => { setMoveTarget(a); setNewFolderName(''); }}
                    className="rounded-lg border border-border p-1.5 text-muted-foreground hover:border-ring hover:text-foreground"
                  >
                    <FolderInput className="h-3 w-3" />
                  </button>
                  <button
                    type="button"
                    onClick={() => copyUrl(a.id, a.url)}
                    title="Copy public URL"
                    className="flex flex-1 items-center justify-center gap-1 rounded-lg border border-border py-1 text-[11px] font-medium text-muted-foreground hover:border-ring hover:text-foreground"
                  >
                    {copiedId === a.id ? <Check className="h-3 w-3 text-primary" /> : <Copy className="h-3 w-3" />}
                    {copiedId === a.id ? 'Copied' : 'Copy URL'}
                  </button>
                  <button
                    type="button"
                    title="Delete image"
                    aria-label={`Delete ${a.filename}`}
                    onClick={() => onDelete(a.id, a.filename)}
                    className="rounded-lg border border-border p-1.5 text-muted-foreground hover:border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
                  >
                    <Trash2 className="h-3 w-3" />
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Move-to-folder modal */}
      <Modal open={moveTarget !== null} onClose={() => setMoveTarget(null)} title={`Move "${moveTarget?.filename ?? ''}"`} size="sm">
        {moveTarget && (
          <div className="space-y-3">
            <div className="space-y-1">
              {(moveTarget.folder ?? '').trim() !== '' && (
                <button type="button" onClick={() => moveTo(moveTarget, '')}
                  className="flex w-full items-center gap-2 rounded-lg border border-border px-3 py-2 text-left text-sm text-foreground hover:border-ring hover:bg-accent">
                  <ImagePlus className="h-4 w-4 text-muted-foreground" /> Unfiled (no folder)
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
                  placeholder="e.g. Logos"
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

export default MediaLibraryPage;
