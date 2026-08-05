import { useState, useCallback } from 'react';
import { useDropzone } from 'react-dropzone';
import { Upload, ArrowLeft } from 'lucide-react';
import { bulkImportRecords, type ObjectSchema, type BulkCreateResult } from '../../lib/api';
import Modal from '../../components/common/Modal';
import { Badge, Button } from '@/components/ui';

interface GenericImportModalProps {
  schema: ObjectSchema;
  onClose: () => void;
  onSuccess: () => void;
}

type Step = 'upload' | 'map' | 'result';

// A light client-side split, good enough to populate the mapping step's
// preview — it does NOT need to handle every CSV-quoting edge case, because
// the actual import re-parses the file with a real CSV reader server-side
// (record_handler.go BulkImport). This only drives what headers the user maps.
function previewParse(text: string): { headers: string[]; sampleRows: string[][] } {
  const lines = text.split(/\r?\n/).filter((l) => l.trim() !== '');
  if (lines.length === 0) return { headers: [], sampleRows: [] };
  const headers = lines[0].split(',').map((h) => h.trim().replace(/^"|"$/g, ''));
  const sampleRows = lines.slice(1, 4).map((l) => l.split(',').map((c) => c.trim().replace(/^"|"$/g, '')));
  return { headers, sampleRows };
}

// GenericImportModal is the R8.2 column-mapped importer for any object
// EXCEPT contacts (which keep the older dedup-aware ImportModal). It has no
// company-resolution or email-dedup logic — a straight "map columns, create
// rows" path, matching what RecordService.BulkCreate offers.
export default function GenericImportModal({ schema, onClose, onSuccess }: GenericImportModalProps) {
  const [step, setStep] = useState<Step>('upload');
  const [file, setFile] = useState<File | null>(null);
  const [headers, setHeaders] = useState<string[]>([]);
  const [sampleRows, setSampleRows] = useState<string[][]>([]);
  const [mapping, setMapping] = useState<Record<string, string>>({});
  const [isUploading, setIsUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<BulkCreateResult | null>(null);

  // Relation fields are excluded — the generic importer writes raw string
  // values, and a relation field expects a target-record UUID, not a name.
  const mappableFields = schema.fields.filter((f) => f.type !== 'relation');

  const onDrop = useCallback((accepted: File[]) => {
    if (accepted.length === 0) return;
    const f = accepted[0];
    setFile(f);
    setError(null);
    f.text().then((text) => {
      const { headers: h, sampleRows: rows } = previewParse(text);
      setHeaders(h);
      setSampleRows(rows);
      // Auto-map a header that matches a field key or label exactly (case-insensitive).
      const auto: Record<string, string> = {};
      for (const header of h) {
        const match = mappableFields.find(
          (f) => f.key.toLowerCase() === header.toLowerCase() || f.label.toLowerCase() === header.toLowerCase()
        );
        if (match) auto[header] = match.key;
      }
      setMapping(auto);
      setStep('map');
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    onDrop,
    accept: { 'text/csv': ['.csv'] },
    maxFiles: 1,
    maxSize: 10 * 1024 * 1024,
  });

  const handleImport = async () => {
    if (!file) return;
    setIsUploading(true);
    setError(null);
    try {
      const activeMapping = Object.fromEntries(Object.entries(mapping).filter(([, v]) => v));
      const res = await bulkImportRecords(schema.slug, file, activeMapping);
      setResult(res);
      setStep('result');
      onSuccess();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setIsUploading(false);
    }
  };

  return (
    <Modal open onClose={onClose} title={`Import ${schema.label_plural}`} size="xl" dismissable={!isUploading}>
      <>
        {step === 'upload' && (
          <>
            <div
              {...getRootProps()}
              className={`border-2 border-dashed rounded-xl p-8 text-center cursor-pointer transition-all ${
                isDragActive ? 'border-primary bg-primary/5' : 'border-muted-foreground/20 hover:border-primary/50 hover:bg-muted/30'
              }`}
            >
              <input {...getInputProps()} />
              <div className="flex flex-col items-center gap-3">
                <div className="h-12 w-12 rounded-full bg-primary/10 flex items-center justify-center text-primary">
                  <Upload aria-hidden className="h-6 w-6" />
                </div>
                <p className="text-sm font-medium">
                  {isDragActive ? 'Drop the file here...' : 'Drop a CSV file, or click to browse'}
                </p>
                <p className="text-xs text-muted-foreground">Max 10MB · CSV only</p>
              </div>
            </div>
            {error && (
              <div className="mt-4 rounded-lg bg-destructive/10 border border-destructive/20 px-4 py-3 text-sm text-destructive">
                {error}
              </div>
            )}
          </>
        )}

        {step === 'map' && (
          <div className="space-y-4">
            <button
              type="button"
              onClick={() => setStep('upload')}
              className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            >
              <ArrowLeft aria-hidden size={14} /> Choose a different file
            </button>
            <p className="text-sm text-muted-foreground">
              Map each column in <span className="font-medium text-foreground">{file?.name}</span> to a {schema.label} field.
            </p>
            <div className="max-h-80 space-y-2 overflow-y-auto">
              {headers.map((header) => (
                <div key={header} className="flex items-center gap-3 rounded-lg border border-border p-2.5">
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium">{header}</div>
                    {sampleRows[0] && (
                      <div className="truncate text-xs text-muted-foreground">
                        e.g. {sampleRows.map((r) => r[headers.indexOf(header)]).filter(Boolean).slice(0, 2).join(', ')}
                      </div>
                    )}
                  </div>
                  <select
                    value={mapping[header] ?? ''}
                    onChange={(e) => setMapping((m) => ({ ...m, [header]: e.target.value }))}
                    className="w-48 rounded-md border border-border bg-background px-2 py-1.5 text-sm"
                  >
                    <option value="">Skip this column</option>
                    {mappableFields.map((f) => (
                      <option key={f.key} value={f.key}>{f.label}</option>
                    ))}
                  </select>
                </div>
              ))}
            </div>

            {error && (
              <div className="rounded-lg bg-destructive/10 border border-destructive/20 px-4 py-3 text-sm text-destructive">
                {error}
              </div>
            )}

            <div className="flex gap-3">
              <Button variant="outline" onClick={onClose} className="flex-1">Cancel</Button>
              <Button
                onClick={handleImport}
                disabled={isUploading || Object.values(mapping).every((v) => !v)}
                className="flex-1"
              >
                {isUploading ? 'Importing...' : 'Import'}
              </Button>
            </div>
          </div>
        )}

        {step === 'result' && result && (
          <div className="space-y-4">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div className="rounded-xl border border-border bg-card p-4 text-center">
                <p className="text-2xl font-bold text-foreground">{result.created}</p>
                <Badge variant="success" className="mt-1">Created</Badge>
              </div>
              <div className="rounded-xl border border-border bg-card p-4 text-center">
                <p className="text-2xl font-bold text-foreground">{result.errors}</p>
                <Badge variant="destructive" className="mt-1">Errors</Badge>
              </div>
            </div>
            {result.error_details && result.error_details.length > 0 && (
              <div className="rounded-lg bg-muted/30 border p-3 max-h-32 overflow-y-auto">
                <p className="text-xs font-medium text-muted-foreground mb-2">Error Details</p>
                {result.error_details.map((d, i) => (
                  <p key={i} className="text-xs text-destructive">{d}</p>
                ))}
              </div>
            )}
            <Button onClick={onClose} className="w-full">Done</Button>
          </div>
        )}
      </>
    </Modal>
  );
}
