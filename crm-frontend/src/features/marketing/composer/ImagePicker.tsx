import React, { useRef, useState } from 'react';
import { AlertCircle, ImagePlus, Loader2, Trash2 } from 'lucide-react';
import Modal from '../../../components/common/Modal';
import { useConfirm } from '../../../components/common/ConfirmDialog';
import { Button, SpinnerBlock, EmptyState } from '@/components/ui';
import { displayImageSrc } from '../assetsApi';
import { useAssets, useRemoveAsset, useUploadAsset } from '../assetsQueries';

interface Props {
  open: boolean;
  onClose: () => void;
  onSelect: (url: string) => void;
}

/** ImagePicker is the Klaviyo-style image library: upload once, reuse anywhere.
 *  Selecting hands back the asset's PUBLIC serve URL (what recipients load). */
export const ImagePicker: React.FC<Props> = ({ open, onClose, onSelect }) => {
  const assets = useAssets(open);
  const upload = useUploadAsset();
  const remove = useRemoveAsset();
  const { confirm, dialog } = useConfirm();
  const fileRef = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string | null>(null);

  const pickFile = () => fileRef.current?.click();

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ''; // allow re-picking the same file
    if (!file) return;
    setError(null);
    try {
      const created = await upload.mutateAsync(file);
      onSelect(created.url);
      onClose();
    } catch (err) {
      setError((err as Error).message || 'Upload failed');
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
    } catch (err) {
      setError((err as Error).message || 'Delete failed');
    }
  };

  return (
    <Modal open={open} onClose={onClose} title="Image library" widthClass="max-w-2xl" padded={false}>
      {dialog}
      <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" className="hidden" onChange={onFile} aria-label="Upload image file" />

      <div className="flex items-center justify-between border-b border-border px-4 py-2.5">
        <p className="text-xs text-muted-foreground">PNG, JPEG, GIF or WebP · up to 2 MB</p>
        <Button size="sm" onClick={pickFile} disabled={upload.isPending}>
          {upload.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <ImagePlus className="h-4 w-4" />}
          Upload image
        </Button>
      </div>

      {error && (
        <div className="mx-4 mt-3 flex items-center gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="h-4 w-4 shrink-0" /> {error}
        </div>
      )}

      <div className="max-h-[55vh] overflow-y-auto p-4">
        {assets.isLoading ? (
          <SpinnerBlock label="Loading images…" />
        ) : assets.isError ? (
          <p className="py-10 text-center text-sm text-destructive">Couldn’t load the image library.</p>
        ) : (assets.data?.length ?? 0) === 0 ? (
          <EmptyState
            icon={ImagePlus}
            title="No images yet"
            description="Upload your logo, product shots and banners once — reuse them in every email."
            action={<Button size="sm" onClick={pickFile}>Upload your first image</Button>}
          />
        ) : (
          <div className="grid grid-cols-3 gap-3 sm:grid-cols-4">
            {assets.data!.map((a) => (
              <div key={a.id} className="group relative">
                <button
                  type="button"
                  onClick={() => { onSelect(a.url); onClose(); }}
                  title={`Use ${a.filename}`}
                  className="block w-full overflow-hidden rounded-lg border border-border bg-muted/30 transition-shadow hover:ring-2 hover:ring-primary"
                >
                  <img src={displayImageSrc(a.url)} alt={a.filename} loading="lazy" className="h-24 w-full object-contain" draggable={false} />
                </button>
                <button
                  type="button"
                  title="Delete image"
                  aria-label={`Delete ${a.filename}`}
                  onClick={() => onDelete(a.id, a.filename)}
                  className="absolute right-1 top-1 rounded bg-card/90 p-1 text-muted-foreground opacity-0 shadow-sm transition-opacity hover:text-destructive group-hover:opacity-100"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
                <p className="mt-1 truncate text-[10px] text-muted-foreground" title={a.filename}>
                  {a.filename} · {Math.max(1, Math.round(a.size_bytes / 1024))} KB
                </p>
              </div>
            ))}
          </div>
        )}
      </div>
    </Modal>
  );
};

export default ImagePicker;
