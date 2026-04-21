import { useCallback, useEffect, useRef, useState } from 'react';
import {
  uploadVoiceNote,
  getContacts,
  getDeals,
  type VoiceNote,
} from '../../lib/api';

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
    <div style={{
      background: 'linear-gradient(135deg, #0f0c29, #302b63, #24243e)',
      borderRadius: 20, padding: 28, color: '#fff',
      fontFamily: "'Inter', sans-serif", maxWidth: 560,
    }}>

      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 24 }}>
        <span style={{ fontSize: 22 }}>📁</span>
        <h3 style={{ margin: 0, fontSize: 18, fontWeight: 700, letterSpacing: '-0.5px' }}>
          Upload Audio File
        </h3>
        <span style={{
          marginLeft: 'auto', fontSize: 11, fontWeight: 600, opacity: 0.5,
          border: '1px solid rgba(255,255,255,0.2)', borderRadius: 6, padding: '2px 8px',
        }}>
          {ACCEPTED_TYPES.join(' · ')} · max 500 MB
        </span>
      </div>

      {/* ── Contact / Deal selectors (global /voice only) ── */}
      {showSelectors && uploaderState !== 'done' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginBottom: 20 }}>

          {/* Contact — REQUIRED */}
          <div style={{ position: 'relative' }}>
            <label style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
              <span style={{ opacity: 0.7 }}>Contact</span>
              <span style={{ background: 'rgba(239,68,68,0.25)', color: '#fca5a5', fontSize: 10, fontWeight: 700, padding: '1px 6px', borderRadius: 4, letterSpacing: '0.5px' }}>REQUIRED</span>
            </label>
            {contactId ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderRadius: 8, background: 'rgba(99,102,241,0.25)', border: '1px solid rgba(99,102,241,0.5)' }}>
                <span>👤</span>
                <span style={{ flex: 1, fontSize: 13, fontWeight: 600 }}>{selectedContactName}</span>
                <button onClick={() => { setContactId(''); setSelectedContactName(''); setContactSearch(''); setShowContactError(false); }}
                  style={clearBtnStyle} title="Clear">✕</button>
              </div>
            ) : (
              <div style={{ position: 'relative' }}>
                <input
                  id="uploader-contact-search"
                  value={contactSearch}
                  onChange={e => { setContactSearch(e.target.value); setContactDropOpen(true); setShowContactError(false); }}
                  onFocus={() => setContactDropOpen(true)}
                  onBlur={() => setTimeout(() => setContactDropOpen(false), 150)}
                  placeholder="🔍 Search by name or email…"
                  autoComplete="off"
                  style={{ ...inputStyle, borderColor: showContactError ? '#ef4444' : 'rgba(255,255,255,0.2)' }}
                />
                {contactDropOpen && contacts.length > 0 && (
                  <div style={dropdownStyle}>
                    {contacts.map(c => (
                      <div key={c.id} id={`uploader-contact-opt-${c.id}`}
                        style={dropItemStyle}
                        onMouseDown={e => e.preventDefault()}
                        onClick={() => { setContactId(c.id); setSelectedContactName(c.name); setContactSearch(''); setContactDropOpen(false); setShowContactError(false); }}>
                        <span style={{ marginRight: 8 }}>👤</span>{c.name}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
            {showContactError && (
              <p style={{ margin: '5px 0 0', fontSize: 11, color: '#fca5a5' }}>⚠ A contact is required before uploading</p>
            )}
          </div>

          {/* Deal — OPTIONAL */}
          <div style={{ position: 'relative' }}>
            <label style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
              <span style={{ opacity: 0.7 }}>Deal</span>
              <span style={{ background: 'rgba(107,114,128,0.3)', color: 'rgba(255,255,255,0.5)', fontSize: 10, fontWeight: 700, padding: '1px 6px', borderRadius: 4, letterSpacing: '0.5px' }}>OPTIONAL</span>
            </label>
            {dealId ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderRadius: 8, background: 'rgba(34,197,94,0.15)', border: '1px solid rgba(34,197,94,0.35)' }}>
                <span>💼</span>
                <span style={{ flex: 1, fontSize: 13, fontWeight: 600 }}>{selectedDealTitle}</span>
                <button onClick={() => { setDealId(''); setSelectedDealTitle(''); setDealSearch(''); }}
                  style={clearBtnStyle} title="Clear">✕</button>
              </div>
            ) : (
              <div style={{ position: 'relative' }}>
                <input
                  id="uploader-deal-search"
                  value={dealSearch}
                  onChange={e => { setDealSearch(e.target.value); setDealDropOpen(true); }}
                  onFocus={() => setDealDropOpen(true)}
                  onBlur={() => setTimeout(() => setDealDropOpen(false), 150)}
                  placeholder="🔍 Search deals… (optional)"
                  autoComplete="off"
                  style={inputStyle}
                />
                {dealDropOpen && deals.length > 0 && (
                  <div style={dropdownStyle}>
                    {deals.map(d => (
                      <div key={d.id} id={`uploader-deal-opt-${d.id}`}
                        style={dropItemStyle}
                        onMouseDown={e => e.preventDefault()}
                        onClick={() => { setDealId(d.id); setSelectedDealTitle(d.title); setDealSearch(''); setDealDropOpen(false); }}>
                        <span style={{ marginRight: 8 }}>💼</span>{d.title}
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
        <div style={{ marginBottom: 20 }}>
          <label style={{ fontSize: 12, opacity: 0.7, display: 'block', marginBottom: 6 }}>Language</label>
          <select
            id="uploader-language-select"
            value={languageCode}
            onChange={e => setLanguageCode(e.target.value)}
            style={{ ...inputStyle, cursor: 'pointer' }}
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
          style={{
            border: `2px dashed ${isDragOver ? '#6366f1' : selectedFile ? 'rgba(34,197,94,0.6)' : 'rgba(255,255,255,0.2)'}`,
            borderRadius: 14,
            padding: selectedFile ? '16px 20px' : '36px 20px',
            textAlign: 'center',
            cursor: 'pointer',
            background: isDragOver
              ? 'rgba(99,102,241,0.12)'
              : selectedFile
                ? 'rgba(34,197,94,0.07)'
                : 'rgba(255,255,255,0.03)',
            transition: 'all 0.2s ease',
            marginBottom: 16,
          }}
        >
          <input
            ref={fileInputRef}
            type="file"
            accept={ACCEPT_ATTR}
            style={{ display: 'none' }}
            onChange={handleInputChange}
            id="uploader-file-input"
          />

          {!selectedFile ? (
            <>
              <div style={{ fontSize: 36, marginBottom: 12, opacity: isDragOver ? 1 : 0.5 }}>
                {isDragOver ? '📂' : '🎵'}
              </div>
              <p style={{ margin: '0 0 6px', fontSize: 14, fontWeight: 600, opacity: isDragOver ? 1 : 0.7 }}>
                {isDragOver ? 'Drop your audio file here' : 'Drag & drop or click to browse'}
              </p>
              <p style={{ margin: 0, fontSize: 12, opacity: 0.4 }}>
                {ACCEPTED_TYPES.join(', ')} · Maximum 500 MB
              </p>
            </>
          ) : (
            /* ── File preview ── */
            <div onClick={e => e.stopPropagation()}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 14, textAlign: 'left' }}>
                <div style={{ width: 44, height: 44, borderRadius: 10, background: 'rgba(34,197,94,0.2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 22, flexShrink: 0 }}>
                  🎵
                </div>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <p style={{ margin: 0, fontSize: 13, fontWeight: 700, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                    {selectedFile.name}
                  </p>
                  <p style={{ margin: '3px 0 0', fontSize: 12, opacity: 0.5 }}>
                    {formatBytes(selectedFile.size)}
                  </p>
                </div>
                <button
                  id="uploader-remove-file"
                  onClick={() => { setSelectedFile(null); if (audioUrl) URL.revokeObjectURL(audioUrl); setAudioUrl(null); setUploaderState('idle'); setFileError(null); }}
                  style={{ ...clearBtnStyle, fontSize: 18 }}
                  title="Remove file"
                >✕</button>
              </div>

              {/* Audio preview player */}
              {audioUrl && (
                <audio
                  id="uploader-audio-preview"
                  controls
                  src={audioUrl}
                  style={{
                    width: '100%', height: 36, borderRadius: 8,
                    accentColor: '#6366f1',
                    filter: 'invert(1) hue-rotate(180deg) brightness(1.1)',
                  }}
                />
              )}

              <p style={{ margin: '10px 0 0', fontSize: 11, opacity: 0.45, textAlign: 'center' }}>
                Click elsewhere in the box to replace file
              </p>
            </div>
          )}
        </div>
      )}

      {/* ── File validation error ── */}
      {fileError && (
        <div style={{ marginBottom: 16, padding: '10px 14px', borderRadius: 10, background: 'rgba(239,68,68,0.12)', border: '1px solid rgba(239,68,68,0.35)', fontSize: 13, color: '#fca5a5', display: 'flex', gap: 8, alignItems: 'flex-start' }}>
          <span style={{ fontSize: 16, flexShrink: 0 }}>⚠</span>
          <span>{fileError}</span>
        </div>
      )}

      {/* ── Uploading state ── */}
      {uploaderState === 'uploading' && (
        <div style={{ textAlign: 'center', padding: '32px 0' }}>
          <div style={{ fontSize: 40, marginBottom: 16, animation: 'spin 1.5s linear infinite', display: 'inline-block' }}>⟳</div>
          <p style={{ margin: 0, fontSize: 14, fontWeight: 600 }}>{progress}</p>
          <p style={{ margin: '6px 0 20px', fontSize: 12, opacity: 0.5 }}>Uploading securely to server…</p>

          <div style={{ width: '100%', height: 6, background: 'rgba(255,255,255,0.1)', borderRadius: 10, overflow: 'hidden' }}>
            <div style={{ 
              width: `${uploadProgressPct}%`, 
              height: '100%', 
              background: 'linear-gradient(90deg, #6366f1, #a855f7)',
              transition: 'width 0.2s ease-out' 
            }} />
          </div>
        </div>
      )}

      {/* ── Done state ── */}
      {uploaderState === 'done' && (
        <div style={{ textAlign: 'center', padding: '28px 20px', background: 'rgba(34,197,94,0.1)', borderRadius: 14, border: '1px solid rgba(34,197,94,0.3)' }}>
          <div style={{ fontSize: 44, marginBottom: 12 }}>✅</div>
          <p style={{ margin: '0 0 6px', fontSize: 15, fontWeight: 700, color: '#4ade80' }}>Upload Successful</p>
          <p style={{ margin: '0 0 20px', fontSize: 13, opacity: 0.7 }}>{progress}</p>
          <button id="uploader-reset-btn" onClick={reset} style={primaryBtnStyle('#6366f1')}>
            ↩ Upload Another File
          </button>
        </div>
      )}

      {/* ── Upload error ── */}
      {uploaderState === 'error' && uploadError && (
        <div style={{ marginBottom: 16, padding: '12px 14px', borderRadius: 10, background: 'rgba(239,68,68,0.12)', border: '1px solid rgba(239,68,68,0.35)', fontSize: 13, color: '#fca5a5' }}>
          <p style={{ margin: '0 0 8px', fontWeight: 600 }}>✗ Upload failed</p>
          <p style={{ margin: 0, opacity: 0.85 }}>{uploadError}</p>
          <button onClick={() => setUploaderState('selecting_file')} style={{ marginTop: 10, ...primaryBtnStyle('#ef4444') }}>
            Try Again
          </button>
        </div>
      )}

      {/* ── Submit button ── */}
      {(uploaderState === 'idle' || uploaderState === 'selecting_file') && selectedFile && (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8 }}>
          <button
            id="uploader-submit-btn"
            onClick={handleUpload}
            title={!canUpload ? 'Select a contact first' : undefined}
            style={{
              ...primaryBtnStyle('#6366f1'),
              width: '100%',
              opacity: canUpload ? 1 : 0.45,
              cursor: canUpload ? 'pointer' : 'not-allowed',
              fontSize: 15,
              padding: '12px 24px',
            }}
          >
            ⬆ Upload File
          </button>
          {!canUpload && (
            <span style={{ fontSize: 11, color: '#fca5a5', opacity: 0.8 }}>Select a contact to enable upload</span>
          )}
        </div>
      )}

      <style>{`
        @keyframes spin { to { transform: rotate(360deg); } }
      `}</style>
    </div>
  );
}

/* ─────────────────────────── Style tokens ──────────────────── */

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '9px 12px', borderRadius: 8,
  background: 'rgba(255,255,255,0.08)', border: '1px solid rgba(255,255,255,0.2)',
  color: '#fff', fontSize: 13, outline: 'none', boxSizing: 'border-box',
};

const dropdownStyle: React.CSSProperties = {
  position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 99, marginTop: 4,
  background: '#1e1b4b', border: '1px solid rgba(99,102,241,0.4)',
  borderRadius: 8, maxHeight: 180, overflowY: 'auto',
  boxShadow: '0 8px 24px rgba(0,0,0,0.4)',
};

const dropItemStyle: React.CSSProperties = {
  padding: '9px 12px', cursor: 'pointer', fontSize: 13,
  transition: 'background 0.15s',
};

const clearBtnStyle: React.CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  color: 'rgba(255,255,255,0.5)', fontSize: 16, lineHeight: 1,
  padding: 0, flexShrink: 0, transition: 'color 0.15s',
};

const primaryBtnStyle = (bg: string): React.CSSProperties => ({
  padding: '10px 24px', borderRadius: 10, border: 'none', cursor: 'pointer',
  background: `linear-gradient(135deg, ${bg}, ${bg}cc)`,
  color: '#fff', fontSize: 14, fontWeight: 600, letterSpacing: '0.3px',
  boxShadow: `0 4px 15px ${bg}55`, transition: 'opacity 0.15s, transform 0.1s',
});
