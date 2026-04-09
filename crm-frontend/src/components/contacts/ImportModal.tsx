import { useState, useCallback } from 'react';
import { useDropzone } from 'react-dropzone';
import { importContacts, type ImportResult } from '../../lib/api';

interface ImportModalProps {
  onClose: () => void;
  onSuccess: () => void;
}

export default function ImportModal({ onClose, onSuccess }: ImportModalProps) {
  const [file, setFile] = useState<File | null>(null);
  const [isUploading, setIsUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);

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
      const res = await importContacts(file);
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
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div className="fixed inset-0 bg-black/50 backdrop-blur-sm" onClick={onClose} />

      {/* Dialog */}
      <div className="relative w-full max-w-xl bg-card rounded-2xl border shadow-2xl p-6 mx-4 animate-in zoom-in-95 duration-200">
        <div className="flex items-center justify-between mb-6">
          <h2 className="text-lg font-semibold">Import Contacts</h2>
          <button
            onClick={onClose}
            className="p-1.5 rounded-md hover:bg-accent transition-colors text-muted-foreground"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>
          </button>
        </div>

        {!result ? (
          <>
            {/* Dropzone */}
            <div
              {...getRootProps()}
              className={`
                border-2 border-dashed rounded-xl p-8 text-center cursor-pointer transition-all
                ${isDragActive
                  ? 'border-blue-500 bg-blue-500/5'
                  : 'border-muted-foreground/20 hover:border-blue-500/50 hover:bg-muted/30'
                }
              `}
            >
              <input {...getInputProps()} />
              <div className="flex flex-col items-center gap-3">
                <div className="h-12 w-12 rounded-full bg-blue-500/10 flex items-center justify-center">
                  <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-blue-500"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" x2="12" y1="3" y2="15"/></svg>
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

            {/* Progress */}
            {isUploading && (
              <div className="mt-4">
                <div className="h-2 rounded-full bg-muted overflow-hidden">
                  <div
                    className="h-full bg-gradient-to-r from-blue-500 to-purple-500 rounded-full transition-all duration-500"
                    style={{ width: `${progress}%` }}
                  />
                </div>
                <p className="text-xs text-muted-foreground text-center mt-2">Processing...</p>
              </div>
            )}

            {/* Error */}
            {error && (
              <div className="mt-4 rounded-lg bg-red-500/10 border border-red-500/20 px-4 py-3 text-sm text-red-400">
                {error}
              </div>
            )}

            {/* Actions */}
            <div className="flex gap-3 mt-6">
              <button
                onClick={onClose}
                className="flex-1 px-4 py-2.5 rounded-lg border text-sm font-medium hover:bg-accent transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleUpload}
                disabled={!file || isUploading}
                className="flex-1 px-4 py-2.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {isUploading ? 'Importing...' : 'Import'}
              </button>
            </div>
          </>
        ) : (
          /* Result summary */
          <div className="space-y-4">
            <div className="grid grid-cols-3 gap-3">
              <div className="rounded-xl bg-emerald-500/10 border border-emerald-500/20 p-4 text-center">
                <p className="text-2xl font-bold text-emerald-400">{result.created}</p>
                <p className="text-xs text-muted-foreground mt-1">Created</p>
              </div>
              <div className="rounded-xl bg-yellow-500/10 border border-yellow-500/20 p-4 text-center">
                <p className="text-2xl font-bold text-yellow-400">{result.skipped}</p>
                <p className="text-xs text-muted-foreground mt-1">Skipped</p>
              </div>
              <div className="rounded-xl bg-red-500/10 border border-red-500/20 p-4 text-center">
                <p className="text-2xl font-bold text-red-400">{result.errors}</p>
                <p className="text-xs text-muted-foreground mt-1">Errors</p>
              </div>
            </div>

            {result.error_details && result.error_details.length > 0 && (
              <div className="rounded-lg bg-muted/30 border p-3 max-h-32 overflow-y-auto">
                <p className="text-xs font-medium text-muted-foreground mb-2">Error Details</p>
                {result.error_details.map((detail, i) => (
                  <p key={i} className="text-xs text-red-400">{detail}</p>
                ))}
              </div>
            )}

            <button
              onClick={onClose}
              className="w-full px-4 py-2.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium transition-colors"
            >
              Done
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
