import React, { useRef, useState } from 'react';
import { AlertCircle, Check, CheckCircle2, Copy, ImagePlus, Loader2, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Button, EmptyState, PageHeader, SpinnerBlock } from '@/components/ui';
import { copyText } from '../../lib/clipboard';
import { displayImageSrc } from './assetsApi';
import { useAssets, useRemoveAsset, useUploadAsset } from './assetsQueries';

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
  const fileRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);

  const showToast = (msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(null), 3000);
  };

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setError(null);
    try {
      await upload.mutateAsync(file);
      showToast('Image uploaded');
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
      showToast('Image deleted');
    } catch (err) {
      setError((err as Error).message || 'Delete failed');
    }
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {dialog}
      {toast && (
        <div role="status" className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          <CheckCircle2 className="h-4 w-4 text-primary" /> {toast}
        </div>
      )}
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

      {assets.isLoading ? (
        <SpinnerBlock label="Loading images…" />
      ) : assets.isError ? (
        <p className="py-12 text-center text-sm text-destructive">Couldn’t load the media library.</p>
      ) : (assets.data?.length ?? 0) === 0 ? (
        <EmptyState
          icon={ImagePlus}
          title="No images yet"
          description="Upload your logo, product shots and banners — they’ll be one click away in the email builder."
          action={<Button onClick={() => fileRef.current?.click()}>Upload your first image</Button>}
        />
      ) : (
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4">
          {assets.data!.map((a) => (
            <div key={a.id} className="group overflow-hidden rounded-xl border border-border bg-card">
              <div className="flex h-36 items-center justify-center bg-muted/30 p-2">
                <img src={displayImageSrc(a.url)} alt={a.filename} loading="lazy" className="max-h-full max-w-full object-contain" draggable={false} />
              </div>
              <div className="border-t border-border p-2.5">
                <p className="truncate text-xs font-medium text-foreground" title={a.filename}>{a.filename}</p>
                <p className="mt-0.5 text-[11px] text-muted-foreground">
                  {Math.max(1, Math.round(a.size_bytes / 1024))} KB · {new Date(a.created_at).toLocaleDateString()}
                </p>
                <div className="mt-2 flex items-center gap-1">
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
    </div>
  );
};

export default MediaLibraryPage;
