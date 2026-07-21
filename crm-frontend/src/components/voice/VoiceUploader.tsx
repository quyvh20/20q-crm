import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Briefcase, CheckCircle2, FileAudio, FolderOpen, Music, RotateCcw, TriangleAlert, Upload, User, X,
} from 'lucide-react';
import {
  uploadVoiceNote,
  getContacts,
  getDeals,
  type VoiceNote,
} from '../../lib/api';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Spinner } from '../ui/spinner';

/* ─────────────────────────── Types ─────────────────────────── */

type UploaderState = 'idle' | 'selecting_file' | 'uploading' | 'done' | 'error';

interface VoiceUploaderProps {
  initialContactId?: string;
  initialDealId?: string;
  onUploadComplete?: (note: VoiceNote) => void;
}

/* ─────────────────────────── Constants ─────────────────────── */

const ACCEPTED_TYPES = ['.mp3', '.wav', '.m4a', '.webm'];

// Full MIME list for secondary validation (browsers vary in what they report)
const ACCEPTED_MIME = new Set([
  'audio/mpeg',      // .mp3
  'audio/mp3',       // .mp3 (alternate)
  'audio/wav',       // .wav
  'audio/x-wav',     // .wav (alternate)
  'audio/wave',      // .wav (alternate)
  'audio/vnd.wave',  // .wav (alternate)
  'audio/mp4',       // .m4a
  'audio/x-m4a',     // .m4a (alternate)
  'audio/aac',       // .m4a sometimes tagged as aac
  'audio/webm',      // .webm
  'video/webm',      // .webm (some browsers report as video)
]);

// Combined accept string — extensions + MIME types so the OS picker hides unsupported files
const ACCEPT_ATTR = [
  ...ACCEPTED_TYPES,
  'audio/mpeg', 'audio/mp3', 'audio/wav', 'audio/x-wav',
  'audio/mp4', 'audio/x-m4a', 'audio/webm', 'video/webm',
].join(',');

const MAX_BYTES = 500 * 1024 * 1024; // 500 MB


const LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'af', label: 'Afrikaans' },
  { code: 'sq', label: 'Albanian' },
  { code: 'ar', label: 'Arabic' },
  { code: 'hy', label: 'Armenian' },
  { code: 'az', label: 'Azerbaijani' },
  { code: 'eu', label: 'Basque' },
  { code: 'be', label: 'Belarusian' },
  { code: 'bn', label: 'Bengali' },
  { code: 'bs', label: 'Bosnian' },
  { code: 'bg', label: 'Bulgarian' },
  { code: 'ca', label: 'Catalan' },
  { code: 'zh', label: 'Chinese' },
  { code: 'hr', label: 'Croatian' },
  { code: 'cs', label: 'Czech' },
  { code: 'da', label: 'Danish' },
  { code: 'nl', label: 'Dutch' },
  { code: 'et', label: 'Estonian' },
  { code: 'fi', label: 'Finnish' },
  { code: 'fr', label: 'French' },
  { code: 'gl', label: 'Galician' },
  { code: 'ka', label: 'Georgian' },
  { code: 'de', label: 'German' },
  { code: 'el', label: 'Greek' },
  { code: 'gu', label: 'Gujarati' },
  { code: 'he', label: 'Hebrew' },
  { code: 'hi', label: 'Hindi' },
  { code: 'hu', label: 'Hungarian' },
  { code: 'is', label: 'Icelandic' },
  { code: 'id', label: 'Indonesian' },
  { code: 'it', label: 'Italian' },
  { code: 'ja', label: 'Japanese' },
  { code: 'kn', label: 'Kannada' },
  { code: 'kk', label: 'Kazakh' },
  { code: 'ko', label: 'Korean' },
  { code: 'lv', label: 'Latvian' },
  { code: 'lt', label: 'Lithuanian' },
  { code: 'mk', label: 'Macedonian' },
  { code: 'ms', label: 'Malay' },
  { code: 'ml', label: 'Malayalam' },
  { code: 'mt', label: 'Maltese' },
  { code: 'mr', label: 'Marathi' },
  { code: 'ne', label: 'Nepali' },
  { code: 'no', label: 'Norwegian' },
  { code: 'fa', label: 'Persian' },
  { code: 'pl', label: 'Polish' },
  { code: 'pt', label: 'Portuguese' },
  { code: 'pa', label: 'Punjabi' },
  { code: 'ro', label: 'Romanian' },
  { code: 'ru', label: 'Russian' },
  { code: 'sr', label: 'Serbian' },
  { code: 'si', label: 'Sinhala' },
  { code: 'sk', label: 'Slovak' },
  { code: 'sl', label: 'Slovenian' },
  { code: 'es', label: 'Spanish' },
  { code: 'sw', label: 'Swahili' },
  { code: 'sv', label: 'Swedish' },
  { code: 'tl', label: 'Tagalog' },
  { code: 'ta', label: 'Tamil' },
  { code: 'te', label: 'Telugu' },
  { code: 'th', label: 'Thai' },
  { code: 'tr', label: 'Turkish' },
  { code: 'uk', label: 'Ukrainian' },
  { code: 'ur', label: 'Urdu' },
  { code: 'uz', label: 'Uzbek' },
  { code: 'vi', label: 'Vietnamese' },
  { code: 'cy', label: 'Welsh' },
  { code: 'auto', label: 'Auto-detect' },
];

/* ─────────────────────────── Helpers ───────────────────────── */

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function validateFile(file: File): string | null {
  const rawExt = file.name.split('.').pop()?.toLowerCase() ?? '';
  const ext = rawExt ? `.${rawExt}` : '';

  // Step 1 — Extension is the primary gate (always enforced)
  if (!ACCEPTED_TYPES.includes(ext)) {
    return `Unsupported file type "${ext || file.name}". Please upload: ${ACCEPTED_TYPES.join(', ')}`;
  }

  // Step 2 — MIME type guard (only when browser provides one, not empty string)
  // Catches renamed files: e.g. video.mp4 → recording.mp3 would have MIME "video/mp4"
  if (file.type !== '' && !ACCEPTED_MIME.has(file.type)) {
    return `File detected as "${file.type}" — not a valid audio format. Only ${ACCEPTED_TYPES.join(', ')} files are accepted.`;
  }

  // Step 3 — Size
  if (file.size > MAX_BYTES) {
    return `File is too large (${formatBytes(file.size)}). Maximum allowed: 500 MB`;
  }

  return null;
}

/* ─────────────────────────── Shared classes ────────────────── */

const inputClass =
  'w-full rounded-lg border border-input bg-background px-3 py-2 text-sm text-foreground placeholder:text-muted-foreground focus:border-ring focus:outline-none focus:ring-2 focus:ring-ring/20';

/* ─────────────────────────── Component ─────────────────────── */

export default function VoiceUploader({
  initialContactId,
  initialDealId,
  onUploadComplete,
}: VoiceUploaderProps) {
  /* — State — */
  const [uploaderState, setUploaderState] = useState<UploaderState>('idle');
  const [isDragOver, setIsDragOver]    = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [audioUrl, setAudioUrl]         = useState<string | null>(null);
  const [fileError, setFileError]       = useState<string | null>(null);
  const [uploadError, setUploadError]   = useState<string | null>(null);
  const [progress, setProgress]         = useState<string>('');
  const [uploadProgressPct, setUploadProgressPct] = useState<number>(0);

  /* — Language — */
  const [languageCode, setLanguageCode] = useState('en');

  /* — Contact selector — */
  const [contactId, setContactId]               = useState(initialContactId ?? '');
  const [selectedContactName, setSelectedContactName] = useState('');
  const [contactSearch, setContactSearch]       = useState('');
  const [contacts, setContacts]                 = useState<{ id: string; name: string }[]>([]);
  const [contactDropOpen, setContactDropOpen]   = useState(false);
  const [showContactError, setShowContactError] = useState(false);

  /* — Deal selector — */
  const [dealId, setDealId]                     = useState(initialDealId ?? '');
  const [selectedDealTitle, setSelectedDealTitle] = useState('');
  const [dealSearch, setDealSearch]             = useState('');
  const [deals, setDeals]                       = useState<{ id: string; title: string }[]>([]);
  const [dealDropOpen, setDealDropOpen]         = useState(false);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const showSelectors = !initialContactId && !initialDealId;
  const canUpload = !showSelectors || !!contactId;

  /* — Load contact/deal suggestions — */
  useEffect(() => {
    if (!showSelectors) return;
    getContacts({ limit: 50, q: contactSearch }).then(({ contacts: c }) =>
      setContacts(c.map(x => ({ id: x.id, name: `${x.first_name} ${x.last_name}` }))),
    ).catch(() => {});
  }, [contactSearch, showSelectors]);

  useEffect(() => {
    if (!showSelectors) return;
    getDeals({ limit: 50, q: dealSearch }).then(({ deals: d }) =>
      setDeals(d.map(x => ({ id: x.id, title: x.title }))),
    ).catch(() => {});
  }, [dealSearch, showSelectors]);

  /* — Revoke audio object URL on unmount / file change — */
  useEffect(() => {
    return () => { if (audioUrl) URL.revokeObjectURL(audioUrl); };
  }, [audioUrl]);

  /* — File selection — */
  const acceptFile = useCallback((file: File) => {
    const err = validateFile(file);
    if (err) { setFileError(err); setSelectedFile(null); setAudioUrl(null); return; }
    setFileError(null);
    setSelectedFile(file);
    if (audioUrl) URL.revokeObjectURL(audioUrl);
    setAudioUrl(URL.createObjectURL(file));
    setUploaderState('selecting_file');
  }, [audioUrl]);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setIsDragOver(false);
    const file = e.dataTransfer.files[0];
    if (file) acceptFile(file);
  }, [acceptFile]);

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) acceptFile(file);
    e.target.value = '';
  };

  /* — Upload — */
  const handleUpload = async () => {
    if (!selectedFile) return;
    if (showSelectors && !contactId) { setShowContactError(true); return; }
    setShowContactError(false);
    setUploaderState('uploading');
    setUploadError(null);
    setProgress('Uploading audio…');

    try {
      const note = await uploadVoiceNote(
        selectedFile,
        selectedFile.name,
        languageCode === 'auto' ? '' : languageCode,
        contactId || undefined,
        dealId   || undefined,
        0,
        (pct) => {
          setUploadProgressPct(pct);
          setProgress(`Uploading audio… ${pct}%`);
        }
      );
      setUploadProgressPct(100);
      setProgress('Uploaded successfully. View in Voice Library to analyze.');
      setUploaderState('done');
      onUploadComplete?.(note.voice_note);
    } catch (err) {
      setUploadError(err instanceof Error ? err.message : 'Upload failed');
      setUploaderState('error');
    }
  };

  /* — Reset — */
  const reset = () => {
    setUploaderState('idle');
    setSelectedFile(null);
    if (audioUrl) URL.revokeObjectURL(audioUrl);
    setAudioUrl(null);
    setFileError(null);
    setUploadError(null);
    setProgress('');
    setUploadProgressPct(0);
    setContactId(initialContactId ?? '');
    setDealId(initialDealId ?? '');
    setSelectedContactName('');
    setSelectedDealTitle('');
    setContactSearch('');
    setDealSearch('');
    setShowContactError(false);
  };

  /* ──────────────────────── Render ───────────────────────── */
  return (
    <div className="w-full max-w-xl rounded-xl border border-border bg-card p-6 text-card-foreground">

      {/* Header */}
      <div className="mb-5 flex flex-wrap items-center gap-2.5">
        <FileAudio aria-hidden className="h-5 w-5 text-primary" />
        <h3 className="text-base font-semibold tracking-tight">Upload Audio File</h3>
        <Badge variant="outline" className="ml-auto">
          {ACCEPTED_TYPES.join(' · ')} · max 500 MB
        </Badge>
      </div>

      {/* ── Contact / Deal selectors (global /voice only) ── */}
      {showSelectors && uploaderState !== 'done' && (
        <div className="mb-5 flex flex-col gap-3">

          {/* Contact — REQUIRED */}
          <div className="relative">
            <label className="mb-1.5 flex items-center gap-1.5 text-xs">
              <span className="text-muted-foreground">Contact</span>
              <Badge variant="destructive" className="px-1.5 text-[10px] font-bold tracking-wide">REQUIRED</Badge>
            </label>
            {contactId ? (
              <div className="flex items-center gap-2 rounded-lg border border-primary/50 bg-primary/10 px-3 py-2">
                <User aria-hidden className="h-4 w-4 shrink-0 text-primary" />
                <span className="flex-1 truncate text-[13px] font-semibold">{selectedContactName}</span>
                <Button
                  variant="ghost" size="icon" className="h-6 w-6 shrink-0 text-muted-foreground hover:text-foreground"
                  onClick={() => { setContactId(''); setSelectedContactName(''); setContactSearch(''); setShowContactError(false); }}
                  title="Clear"
                >
                  <X aria-hidden />
                </Button>
              </div>
            ) : (
              <div className="relative">
                <input
                  id="uploader-contact-search"
                  value={contactSearch}
                  onChange={e => { setContactSearch(e.target.value); setContactDropOpen(true); setShowContactError(false); }}
                  onFocus={() => setContactDropOpen(true)}
                  onBlur={() => setTimeout(() => setContactDropOpen(false), 150)}
                  placeholder="Search by name, email, company, or phone…"
                  autoComplete="off"
                  className={`${inputClass} ${showContactError ? 'border-destructive' : ''}`}
                />
                {contactDropOpen && contacts.length > 0 && (
                  <div className="absolute inset-x-0 top-full z-50 mt-1 max-h-44 overflow-y-auto rounded-lg border border-border bg-popover text-popover-foreground shadow-lg">
                    {contacts.map(c => (
                      <div key={c.id} id={`uploader-contact-opt-${c.id}`}
                        className="flex cursor-pointer items-center gap-2 px-3 py-2 text-[13px] transition-colors hover:bg-accent"
                        onMouseDown={e => e.preventDefault()}
                        onClick={() => { setContactId(c.id); setSelectedContactName(c.name); setContactSearch(''); setContactDropOpen(false); setShowContactError(false); }}>
                        <User aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />{c.name}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
            {showContactError && (
              <p className="mt-1.5 flex items-center gap-1 text-[11px] text-destructive">
                <TriangleAlert aria-hidden className="h-3 w-3 shrink-0" />
                A contact is required before uploading
              </p>
            )}
          </div>

          {/* Deal — OPTIONAL */}
          <div className="relative">
            <label className="mb-1.5 flex items-center gap-1.5 text-xs">
              <span className="text-muted-foreground">Deal</span>
              <Badge variant="secondary" className="px-1.5 text-[10px] font-bold tracking-wide text-muted-foreground">OPTIONAL</Badge>
            </label>
            {dealId ? (
              <div className="flex items-center gap-2 rounded-lg border border-emerald-500/40 bg-emerald-500/10 px-3 py-2">
                <Briefcase aria-hidden className="h-4 w-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
                <span className="flex-1 truncate text-[13px] font-semibold">{selectedDealTitle}</span>
                <Button
                  variant="ghost" size="icon" className="h-6 w-6 shrink-0 text-muted-foreground hover:text-foreground"
                  onClick={() => { setDealId(''); setSelectedDealTitle(''); setDealSearch(''); }}
                  title="Clear"
                >
                  <X aria-hidden />
                </Button>
              </div>
            ) : (
              <div className="relative">
                <input
                  id="uploader-deal-search"
                  value={dealSearch}
                  onChange={e => { setDealSearch(e.target.value); setDealDropOpen(true); }}
                  onFocus={() => setDealDropOpen(true)}
                  onBlur={() => setTimeout(() => setDealDropOpen(false), 150)}
                  placeholder="Search deals… (optional)"
                  autoComplete="off"
                  className={inputClass}
                />
                {dealDropOpen && deals.length > 0 && (
                  <div className="absolute inset-x-0 top-full z-50 mt-1 max-h-44 overflow-y-auto rounded-lg border border-border bg-popover text-popover-foreground shadow-lg">
                    {deals.map(d => (
                      <div key={d.id} id={`uploader-deal-opt-${d.id}`}
                        className="flex cursor-pointer items-center gap-2 px-3 py-2 text-[13px] transition-colors hover:bg-accent"
                        onMouseDown={e => e.preventDefault()}
                        onClick={() => { setDealId(d.id); setSelectedDealTitle(d.title); setDealSearch(''); setDealDropOpen(false); }}>
                        <Briefcase aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />{d.title}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {/* ── Language selector ── */}
      {uploaderState !== 'done' && (
        <div className="mb-5">
          <label htmlFor="uploader-language-select" className="mb-1.5 block text-xs text-muted-foreground">Language</label>
          <select
            id="uploader-language-select"
            value={languageCode}
            onChange={e => setLanguageCode(e.target.value)}
            className={`${inputClass} cursor-pointer`}
          >
            {LANGUAGES.map(l => <option key={l.code} value={l.code}>{l.label}</option>)}
          </select>
        </div>
      )}

      {/* ── Drag & Drop Zone ── */}
      {uploaderState !== 'uploading' && uploaderState !== 'done' && (
        <div
          id="uploader-drop-zone"
          onDragOver={e => { e.preventDefault(); setIsDragOver(true); }}
          onDragLeave={() => setIsDragOver(false)}
          onDrop={handleDrop}
          onClick={() => fileInputRef.current?.click()}
          className={`mb-4 cursor-pointer rounded-xl border-2 border-dashed text-center transition-colors ${
            isDragOver
              ? 'border-primary bg-primary/10'
              : selectedFile
                ? 'border-emerald-500/50 bg-emerald-500/5'
                : 'border-border bg-muted/30'
          } ${selectedFile ? 'px-5 py-4' : 'px-5 py-9'}`}
        >
          <input
            ref={fileInputRef}
            type="file"
            accept={ACCEPT_ATTR}
            className="hidden"
            onChange={handleInputChange}
            id="uploader-file-input"
          />

          {!selectedFile ? (
            <>
              {isDragOver
                ? <FolderOpen aria-hidden className="mx-auto mb-3 h-9 w-9 text-primary" />
                : <Music aria-hidden className="mx-auto mb-3 h-9 w-9 text-muted-foreground/70" />}
              <p className={`mb-1.5 text-sm font-semibold ${isDragOver ? 'text-foreground' : 'text-muted-foreground'}`}>
                {isDragOver ? 'Drop your audio file here' : 'Drag & drop or click to browse'}
              </p>
              <p className="text-xs text-muted-foreground/70">
                {ACCEPTED_TYPES.join(', ')} · Maximum 500 MB
              </p>
            </>
          ) : (
            /* ── File preview ── */
            <div onClick={e => e.stopPropagation()}>
              <div className="mb-3.5 flex items-center gap-3 text-left">
                <div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-emerald-500/15">
                  <Music aria-hidden className="h-5 w-5 text-emerald-600 dark:text-emerald-400" />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-bold">{selectedFile.name}</p>
                  <p className="mt-0.5 text-xs text-muted-foreground">{formatBytes(selectedFile.size)}</p>
                </div>
                <Button
                  id="uploader-remove-file"
                  variant="ghost" size="icon" className="h-7 w-7 shrink-0 text-muted-foreground hover:text-foreground"
                  onClick={() => { setSelectedFile(null); if (audioUrl) URL.revokeObjectURL(audioUrl); setAudioUrl(null); setUploaderState('idle'); setFileError(null); }}
                  title="Remove file"
                >
                  <X aria-hidden />
                </Button>
              </div>

              {/* Audio preview player */}
              {audioUrl && (
                <audio
                  id="uploader-audio-preview"
                  controls
                  src={audioUrl}
                  className="h-9 w-full rounded-lg"
                />
              )}

              <p className="mt-2.5 text-center text-[11px] text-muted-foreground/70">
                Click elsewhere in the box to replace file
              </p>
            </div>
          )}
        </div>
      )}

      {/* ── File validation error ── */}
      {fileError && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 px-3.5 py-2.5 text-[13px] text-destructive">
          <TriangleAlert aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{fileError}</span>
        </div>
      )}

      {/* ── Uploading state ── */}
      {uploaderState === 'uploading' && (
        <div className="py-8 text-center">
          <div className="mb-4 flex justify-center">
            <Spinner size="lg" />
          </div>
          <p className="text-sm font-semibold">{progress}</p>
          <p className="mb-5 mt-1.5 text-xs text-muted-foreground">Uploading securely to server…</p>

          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full bg-primary transition-[width] duration-200 ease-out"
              style={{ width: `${uploadProgressPct}%` }}
            />
          </div>
        </div>
      )}

      {/* ── Done state ── */}
      {uploaderState === 'done' && (
        <div className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-5 py-7 text-center">
          <CheckCircle2 aria-hidden className="mx-auto mb-3 h-11 w-11 text-emerald-600 dark:text-emerald-400" />
          <p className="mb-1.5 text-[15px] font-bold text-emerald-600 dark:text-emerald-400">Upload Successful</p>
          <p className="mb-5 text-[13px] text-muted-foreground">{progress}</p>
          <Button id="uploader-reset-btn" onClick={reset}>
            <RotateCcw aria-hidden />
            Upload Another File
          </Button>
        </div>
      )}

      {/* ── Upload error ── */}
      {uploaderState === 'error' && uploadError && (
        <div className="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3.5 py-3 text-[13px] text-destructive">
          <p className="mb-2 flex items-center gap-1.5 font-semibold">
            <TriangleAlert aria-hidden className="h-4 w-4 shrink-0" />
            Upload failed
          </p>
          <p className="opacity-90">{uploadError}</p>
          <Button variant="destructive" size="sm" className="mt-2.5" onClick={() => setUploaderState('selecting_file')}>
            Try Again
          </Button>
        </div>
      )}

      {/* ── Submit button ── */}
      {(uploaderState === 'idle' || uploaderState === 'selecting_file') && selectedFile && (
        <div className="flex flex-col items-center gap-2">
          {/* Deliberately NOT `disabled`: clicking without a contact surfaces the
              inline "contact required" error instead of doing nothing. */}
          <Button
            id="uploader-submit-btn"
            onClick={handleUpload}
            title={!canUpload ? 'Select a contact first' : undefined}
            className={`w-full ${!canUpload ? 'cursor-not-allowed opacity-50' : ''}`}
          >
            <Upload aria-hidden />
            Upload File
          </Button>
          {!canUpload && (
            <span className="text-[11px] text-destructive/80">Select a contact to enable upload</span>
          )}
        </div>
      )}
    </div>
  );
}
