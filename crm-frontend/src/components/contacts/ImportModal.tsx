import { useState, useCallback } from 'react';
import { useDropzone } from 'react-dropzone';
import { Upload, CircleX, Pencil, AlertTriangle } from 'lucide-react';
import { importContacts, type ImportResult } from '../../lib/api';
import Modal from '../common/Modal';
import { Badge, Button } from '@/components/ui';

interface ImportModalProps {
  onClose: () => void;
  onSuccess: () => void;
}

type ConflictMode = 'skip' | 'overwrite';

export default function ImportModal({ onClose, onSuccess }: ImportModalProps) {
  const [file, setFile] = useState<File | null>(null);
  const [isUploading, setIsUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [conflictMode, setConflictMode] = useState<ConflictMode>('skip');

  const onDrop = useCallback((acceptedFiles: File[]) => {
    if (acceptedFiles.length > 0) {
      setFile(acceptedFiles[0]);
      setError(null);
      setResult(null);
    }
  }, []);

  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    onDrop,
    accept: {
      'text/csv': ['.csv'],
      'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': ['.xlsx'],
    },
    maxFiles: 1,
    maxSize: 10 * 1024 * 1024, // 10MB
  });

  const handleUpload = async () => {
    if (!file) return;

    setIsUploading(true);
    setProgress(20);
    setError(null);

    try {
      setProgress(50);
      const res = await importContacts(file, conflictMode);
      setProgress(100);
      setResult(res);
      onSuccess();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setIsUploading(false);
    }
  };

  return (
    // Shared Radix modal (U7): the close X, Escape, focus trap/restore and aria
    // are the primitive's job now. Dismissal is blocked while the upload is in
    // flight so a stray Escape can't hide a running import.
    <Modal
      open
      onClose={onClose}
      title="Import Contacts"
      size="xl"
      dismissable={!isUploading}
    >
      <>
        {!result ? (
          <>
            {/* Dropzone */}
            <div
              {...getRootProps()}
              className={`
                border-2 border-dashed rounded-xl p-8 text-center cursor-pointer transition-all
                ${isDragActive
                  ? 'border-primary bg-primary/5'
                  : 'border-muted-foreground/20 hover:border-primary/50 hover:bg-muted/30'
                }
              `}
            >
              <input {...getInputProps()} />
              <div className="flex flex-col items-center gap-3">
                <div className="h-12 w-12 rounded-full bg-primary/10 flex items-center justify-center text-primary">
                  <Upload aria-hidden className="h-6 w-6" />
                </div>
                {file ? (
                  <div>
                    <p className="font-medium text-sm">{file.name}</p>
                    <p className="text-xs text-muted-foreground mt-1">
                      {(file.size / 1024).toFixed(1)} KB
                    </p>
                  </div>
                ) : (
                  <div>
                    <p className="text-sm font-medium">
                      {isDragActive ? 'Drop the file here...' : 'Drop a CSV or XLSX file, or click to browse'}
                    </p>
                    <p className="text-xs text-muted-foreground mt-1">Max 10MB</p>
                  </div>
                )}
              </div>
            </div>

            {/* Column mapping info */}
            <div className="mt-4 p-3 rounded-lg bg-muted/30 border border-muted">
              <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">Expected columns</p>
              <div className="flex flex-wrap gap-1.5">
                {['first_name', 'last_name', 'email', 'phone', 'company_name', 'tags'].map((col) => (
                  <span key={col} className="inline-flex items-center rounded-md bg-background px-2 py-0.5 text-xs font-mono border">
                    {col}
                  </span>
                ))}
              </div>
            </div>

            {/* On Duplicate Email — conflict mode toggle */}
            <div className="mt-4">
              <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                On duplicate email
              </p>
              <div className="grid grid-cols-2 gap-2">
                <button
                  id="conflict-mode-skip"
                  onClick={() => setConflictMode('skip')}
                  className={`flex items-center gap-2 px-4 py-2.5 rounded-lg border text-sm font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                    conflictMode === 'skip'
                      ? 'border-primary bg-primary/10 text-primary'
                      : 'border-border hover:border-muted-foreground/40 text-muted-foreground'
                  }`}
                >
                  <CircleX aria-hidden className="h-[15px] w-[15px]" />
                  Skip duplicates
                </button>
                <button
                  id="conflict-mode-overwrite"
                  onClick={() => setConflictMode('overwrite')}
                  className={`flex items-center gap-2 px-4 py-2.5 rounded-lg border text-sm font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                    conflictMode === 'overwrite'
                      ? 'border-primary bg-primary/10 text-primary'
                      : 'border-border hover:border-muted-foreground/40 text-muted-foreground'
                  }`}
                >
                  <Pencil aria-hidden className="h-[15px] w-[15px]" />
                  Overwrite existing
                </button>
              </div>
              {conflictMode === 'overwrite' && (
                <p className="text-xs text-muted-foreground mt-2 flex items-center gap-1">
                  <AlertTriangle aria-hidden className="h-3 w-3 shrink-0" />
                  Existing contacts with matching email will be updated with values from the file.
                </p>
              )}
            </div>

            {/* Progress */}
            {isUploading && (
              <div className="mt-4">
                <div className="h-2 rounded-full bg-muted overflow-hidden">
                  <div
                    className="h-full bg-primary rounded-full transition-all duration-500"
                    style={{ width: `${progress}%` }}
                  />
                </div>
                <p className="text-xs text-muted-foreground text-center mt-2">Processing...</p>
              </div>
            )}

            {/* Error */}
            {error && (
              <div className="mt-4 rounded-lg bg-destructive/10 border border-destructive/20 px-4 py-3 text-sm text-destructive">
                {error}
              </div>
            )}

            {/* Actions */}
            <div className="flex gap-3 mt-6">
              <Button variant="outline" onClick={onClose} className="flex-1">
                Cancel
              </Button>
              <Button
                onClick={handleUpload}
                disabled={!file || isUploading}
                className="flex-1"
              >
                {isUploading ? 'Importing...' : 'Import'}
              </Button>
            </div>
          </>
        ) : (
          /* Result summary */
          <div className="space-y-4">
            <div className="grid grid-cols-3 gap-3">
              <div className="rounded-xl border border-border bg-card p-4 text-center">
                <p className="text-2xl font-bold text-foreground">{result.created}</p>
                <Badge variant="success" className="mt-1">Created</Badge>
              </div>
              <div className="rounded-xl border border-border bg-card p-4 text-center">
                <p className="text-2xl font-bold text-foreground">{result.skipped}</p>
                <Badge variant="warning" className="mt-1">Skipped</Badge>
              </div>
              <div className="rounded-xl border border-border bg-card p-4 text-center">
                <p className="text-2xl font-bold text-foreground">{result.errors}</p>
                <Badge variant="destructive" className="mt-1">Errors</Badge>
              </div>
            </div>

            {result.error_details && result.error_details.length > 0 && (
              <div className="rounded-lg bg-muted/30 border p-3 max-h-32 overflow-y-auto">
                <p className="text-xs font-medium text-muted-foreground mb-2">Error Details</p>
                {result.error_details.map((detail, i) => (
                  <p key={i} className="text-xs text-destructive">{detail}</p>
                ))}
              </div>
            )}

            <Button onClick={onClose} className="w-full">
              Done
            </Button>
          </div>
        )}
      </>
    </Modal>
  );
}
