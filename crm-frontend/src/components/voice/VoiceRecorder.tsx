import { useCallback, useEffect, useRef, useState } from 'react';
import {
  uploadVoiceNote,
  getVoiceNote,
  getContacts,
  getDeals,
  type VoiceNote,
} from '../../lib/api';

interface VoiceRecorderProps {
  initialContactId?: string;
  initialDealId?: string;
  onRecordingComplete?: (note: VoiceNote) => void;
}

const LANGUAGES = [
  // — Pinned at top —
  { code: 'en', label: 'English' },
  // — A–Z —
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
  // — Special —
  { code: 'auto', label: 'Auto-detect' },
];



type RecorderState = 'idle' | 'recording' | 'uploading' | 'processing' | 'done' | 'error';

export default function VoiceRecorder({ initialContactId, initialDealId, onRecordingComplete }: VoiceRecorderProps) {
  const [state, setState] = useState<RecorderState>('idle');
  const [contactId, setContactId] = useState(initialContactId ?? '');
  const [dealId, setDealId] = useState(initialDealId ?? '');
  const [languageCode, setLanguageCode] = useState('en');
  const [elapsed, setElapsed] = useState(0);
  const [errorMsg, setErrorMsg] = useState('');
  const [finishedNote, setFinishedNote] = useState<VoiceNote | null>(null);
  const [contacts, setContacts] = useState<{ id: string; name: string }[]>([]);
  const [deals, setDeals] = useState<{ id: string; title: string }[]>([]);
  const [contactSearch, setContactSearch] = useState('');
  const [dealSearch, setDealSearch] = useState('');

  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const streamRef = useRef<MediaStream | null>(null);
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const startTimestampRef = useRef(0);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const animFrameRef = useRef<number>(0);
  const analyserRef = useRef<AnalyserNode | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Dropdown open state (focus-gated)
  const [contactDropOpen, setContactDropOpen] = useState(false);
  const [dealDropOpen, setDealDropOpen] = useState(false);
  // Display names for selected items
  const [selectedContactName, setSelectedContactName] = useState('');
  const [selectedDealTitle, setSelectedDealTitle] = useState('');
  // Validation
  const [showContactError, setShowContactError] = useState(false);

  const showSelectors = !initialContactId && !initialDealId;
  const canRecord = !showSelectors || !!contactId; // Contact required on /voice


  useEffect(() => {
    if (!showSelectors) return;
    getContacts({ limit: 50, q: contactSearch }).then(({ contacts: c }) =>
      setContacts(c.map((x) => ({ id: x.id, name: `${x.first_name} ${x.last_name}` }))),
    );
  }, [contactSearch, showSelectors]);

  useEffect(() => {
    if (!showSelectors) return;
    getDeals({ limit: 50, q: dealSearch }).then(({ deals: d }) =>
      setDeals(d.map((x) => ({ id: x.id, title: x.title }))),
    );
  }, [dealSearch, showSelectors]);

  const drawWaveform = useCallback(() => {
    const analyser = analyserRef.current;
    const canvas = canvasRef.current;
    if (!analyser || !canvas) return;
    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const data = new Uint8Array(analyser.frequencyBinCount);
    const draw = () => {
      animFrameRef.current = requestAnimationFrame(draw);
      analyser.getByteTimeDomainData(data);
      ctx.clearRect(0, 0, canvas.width, canvas.height);

      const gradient = ctx.createLinearGradient(0, 0, canvas.width, 0);
      gradient.addColorStop(0, '#6366f1');
      gradient.addColorStop(0.5, '#8b5cf6');
      gradient.addColorStop(1, '#ec4899');
      ctx.strokeStyle = gradient;
      ctx.lineWidth = 2;
      ctx.beginPath();

      const sliceWidth = canvas.width / data.length;
      let x = 0;
      for (let i = 0; i < data.length; i++) {
        const v = data[i] / 128.0;
        const y = (v * canvas.height) / 2;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
        x += sliceWidth;
      }
      ctx.lineTo(canvas.width, canvas.height / 2);
      ctx.stroke();
    };
    draw();
  }, []);

  const stopWaveform = useCallback(() => {
    cancelAnimationFrame(animFrameRef.current);
    const canvas = canvasRef.current;
    if (canvas) {
      const ctx = canvas.getContext('2d');
      ctx?.clearRect(0, 0, canvas.width, canvas.height);
    }
  }, []);

  const startRecording = useCallback(async () => {
    // Enforce required contact on /voice global page
    if (showSelectors && !contactId) {
      setShowContactError(true);
      return;
    }
    setShowContactError(false);
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;

      const audioCtx = new AudioContext();
      const source = audioCtx.createMediaStreamSource(stream);
      const analyser = audioCtx.createAnalyser();
      analyser.fftSize = 256;
      source.connect(analyser);
      analyserRef.current = analyser;

      const mr = new MediaRecorder(stream, { mimeType: 'audio/webm;codecs=opus' });
      mediaRecorderRef.current = mr;
      chunksRef.current = [];

      mr.ondataavailable = (e) => {
        if (e.data.size > 0) chunksRef.current.push(e.data);
      };

      mr.start(100);
      startTimestampRef.current = Date.now();
      setState('recording');
      setElapsed(0);

      timerRef.current = setInterval(() => {
        setElapsed(Math.floor((Date.now() - startTimestampRef.current) / 1000));
      }, 1000);

      drawWaveform();
    } catch {
      setErrorMsg('Microphone access denied. Please allow microphone access.');
      setState('error');
    }
  }, [drawWaveform]);

  const stopAndUpload = useCallback(async () => {
    if (!mediaRecorderRef.current) return;
    if (timerRef.current) clearInterval(timerRef.current);
    stopWaveform();

    const durationSeconds = Math.floor((Date.now() - startTimestampRef.current) / 1000);

    mediaRecorderRef.current.stop();
    streamRef.current?.getTracks().forEach((t) => t.stop());

    await new Promise<void>((resolve) => {
      if (mediaRecorderRef.current) mediaRecorderRef.current.onstop = () => resolve();
      else resolve();
    });

    const audioBlob = new Blob(chunksRef.current, { type: 'audio/webm;codecs=opus' });
    const filename = `voice_${Date.now()}.webm`;

    setState('uploading');
    setErrorMsg('');

    try {
      const { voice_note } = await uploadVoiceNote(
        audioBlob,
        filename,
        languageCode,
        contactId || undefined,
        dealId || undefined,
        durationSeconds,
        undefined, // onProgress
        true       // autoAnalyze
      );

      setState('processing');
      let noteId = voice_note.id;

      pollTimerRef.current = setInterval(async () => {
        try {
          const updated = await getVoiceNote(noteId);
          if (updated.status === 'done' || updated.status === 'error') {
            if (pollTimerRef.current) clearInterval(pollTimerRef.current);
            setState(updated.status);
            setFinishedNote(updated);
            if (updated.status === 'done') onRecordingComplete?.(updated);
            if (updated.status === 'error') setErrorMsg(updated.error_message || 'Processing failed');
          }
        } catch {
          // ignore transient poll errors
        }
      }, 3000);
    } catch (err: unknown) {
      setErrorMsg(err instanceof Error ? err.message : 'Upload failed');
      setState('error');
    }
  }, [languageCode, contactId, dealId, onRecordingComplete, stopWaveform]);

  useEffect(() => {
    return () => {
      if (timerRef.current) clearInterval(timerRef.current);
      if (pollTimerRef.current) clearInterval(pollTimerRef.current);
      stopWaveform();
    };
  }, [stopWaveform]);

  const formatTime = (s: number) => `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;

  const reset = () => {
    setState('idle');
    setElapsed(0);
    setErrorMsg('');
    setFinishedNote(null);
    chunksRef.current = [];
    // Restore the original context IDs — don't lose the pre-fill
    setContactId(initialContactId ?? '');
    setDealId(initialDealId ?? '');
  };

  return (
    <div style={{
      background: 'linear-gradient(135deg, #0f0c29, #302b63, #24243e)',
      borderRadius: 20,
      padding: 28,
      color: '#fff',
      fontFamily: "'Inter', sans-serif",
      maxWidth: 520,
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 20 }}>
        <span style={{ fontSize: 22 }}>🎙</span>
        <h3 style={{ margin: 0, fontSize: 18, fontWeight: 700, letterSpacing: '-0.5px' }}>Voice Intelligence</h3>
        {state === 'recording' && (
          <span style={{
            marginLeft: 'auto', background: '#ef4444', borderRadius: 100,
            padding: '2px 10px', fontSize: 12, fontWeight: 600, animation: 'pulse 1.4s infinite',
          }}>● REC {formatTime(elapsed)}</span>
        )}
      </div>

      {showSelectors && state === 'idle' && (
        <div style={{ marginBottom: 20, display: 'flex', gap: 12, flexDirection: 'column' }}>

          {/* ── Contact (Required) ── */}
          <div style={{ position: 'relative' }}>
            <label style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
              <span style={{ opacity: 0.7 }}>Contact</span>
              <span style={{ background: 'rgba(239,68,68,0.25)', color: '#fca5a5', fontSize: 10, fontWeight: 700, padding: '1px 6px', borderRadius: 4, letterSpacing: '0.5px' }}>REQUIRED</span>
            </label>

            {/* Selected chip */}
            {contactId ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderRadius: 8, background: 'rgba(99,102,241,0.25)', border: '1px solid rgba(99,102,241,0.5)' }}>
                <span style={{ fontSize: 16 }}>👤</span>
                <span style={{ flex: 1, fontSize: 13, fontWeight: 600 }}>{selectedContactName}</span>
                <button
                  id="voice-contact-clear"
                  onClick={() => { setContactId(''); setSelectedContactName(''); setContactSearch(''); setShowContactError(false); }}
                  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(255,255,255,0.5)', fontSize: 16, lineHeight: 1, padding: 0 }}
                  title="Clear contact"
                >✕</button>
              </div>
            ) : (
              <div style={{ position: 'relative' }}>
                <input
                  id="voice-contact-search"
                  value={contactSearch}
                  onChange={(e) => { setContactSearch(e.target.value); setContactDropOpen(true); setShowContactError(false); }}
                  onFocus={() => setContactDropOpen(true)}
                  onBlur={() => setTimeout(() => setContactDropOpen(false), 150)}
                  placeholder="🔍 Search by name or email…"
                  autoComplete="off"
                  style={{ ...inputStyle, borderColor: showContactError ? '#ef4444' : 'rgba(255,255,255,0.2)' }}
                />
                {contactDropOpen && contacts.length > 0 && (
                  <div style={{ ...dropdownStyle, position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 99, marginTop: 4, boxShadow: '0 8px 24px rgba(0,0,0,0.4)' }}>
                    {contacts.map((c) => (
                      <div
                        key={c.id}
                        id={`voice-contact-opt-${c.id}`}
                        style={dropdownItemStyle(false)}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => { setContactId(c.id); setSelectedContactName(c.name); setContactSearch(''); setContactDropOpen(false); setShowContactError(false); }}
                      >
                        <span style={{ marginRight: 8 }}>👤</span>{c.name}
                      </div>
                    ))}
                    {contacts.length === 0 && (
                      <div style={{ padding: '10px 12px', fontSize: 12, opacity: 0.5 }}>No contacts found</div>
                    )}
                  </div>
                )}
              </div>
            )}
            {showContactError && (
              <p style={{ margin: '5px 0 0', fontSize: 11, color: '#fca5a5' }}>⚠ A contact is required before recording</p>
            )}
          </div>

          {/* ── Deal (Optional) ── */}
          <div style={{ position: 'relative' }}>
            <label style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 6, marginBottom: 6 }}>
              <span style={{ opacity: 0.7 }}>Deal</span>
              <span style={{ background: 'rgba(107,114,128,0.3)', color: 'rgba(255,255,255,0.5)', fontSize: 10, fontWeight: 700, padding: '1px 6px', borderRadius: 4, letterSpacing: '0.5px' }}>OPTIONAL</span>
            </label>

            {/* Selected chip */}
            {dealId ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px', borderRadius: 8, background: 'rgba(34,197,94,0.15)', border: '1px solid rgba(34,197,94,0.35)' }}>
                <span style={{ fontSize: 16 }}>💼</span>
                <span style={{ flex: 1, fontSize: 13, fontWeight: 600 }}>{selectedDealTitle}</span>
                <button
                  id="voice-deal-clear"
                  onClick={() => { setDealId(''); setSelectedDealTitle(''); setDealSearch(''); }}
                  style={{ background: 'none', border: 'none', cursor: 'pointer', color: 'rgba(255,255,255,0.5)', fontSize: 16, lineHeight: 1, padding: 0 }}
                  title="Clear deal"
                >✕</button>
              </div>
            ) : (
              <div style={{ position: 'relative' }}>
                <input
                  id="voice-deal-search"
                  value={dealSearch}
                  onChange={(e) => { setDealSearch(e.target.value); setDealDropOpen(true); }}
                  onFocus={() => setDealDropOpen(true)}
                  onBlur={() => setTimeout(() => setDealDropOpen(false), 150)}
                  placeholder="🔍 Search deals… (optional)"
                  autoComplete="off"
                  style={inputStyle}
                />
                {dealDropOpen && deals.length > 0 && (
                  <div style={{ ...dropdownStyle, position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 99, marginTop: 4, boxShadow: '0 8px 24px rgba(0,0,0,0.4)' }}>
                    {deals.map((d) => (
                      <div
                        key={d.id}
                        id={`voice-deal-opt-${d.id}`}
                        style={dropdownItemStyle(false)}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => { setDealId(d.id); setSelectedDealTitle(d.title); setDealSearch(''); setDealDropOpen(false); }}
                      >
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

      {state === 'idle' && (
        <div style={{ marginBottom: 16 }}>
          <label style={{ fontSize: 12, opacity: 0.7, display: 'block', marginBottom: 4 }}>Language</label>
          <select
            id="voice-language-select"
            value={languageCode}
            onChange={(e) => setLanguageCode(e.target.value)}
            style={{ ...inputStyle, cursor: 'pointer' }}
          >
            {LANGUAGES.map((l) => <option key={l.code} value={l.code}>{l.label}</option>)}
          </select>
        </div>
      )}

      <div style={{ position: 'relative', height: 64, marginBottom: 20, borderRadius: 12, overflow: 'hidden', background: 'rgba(255,255,255,0.05)' }}>
        <canvas ref={canvasRef} width={440} height={64} style={{ width: '100%', height: '100%' }} />
        {state !== 'recording' && (
          <div style={{
            position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
            opacity: 0.4, fontSize: 13,
          }}>
            {state === 'idle' ? 'Press record to start' :
             state === 'uploading' ? '⬆ Uploading…' :
             state === 'processing' ? '⟳ AI analyzing…' :
             state === 'done' ? '✓ Done' :
             state === 'error' ? '✗ ' + errorMsg : ''}
          </div>
        )}
      </div>

      <div style={{ display: 'flex', gap: 10, justifyContent: 'center' }}>
        {state === 'idle' && (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6 }}>
            <button
              id="voice-record-btn"
              onClick={startRecording}
              title={!canRecord ? 'Select a contact first' : undefined}
              style={{
                ...primaryBtnStyle('#6366f1'),
                opacity: canRecord ? 1 : 0.45,
                cursor: canRecord ? 'pointer' : 'not-allowed',
              }}
            >
              ● Start Recording
            </button>
            {!canRecord && (
              <span style={{ fontSize: 11, color: '#fca5a5', opacity: 0.8 }}>Select a contact to enable recording</span>
            )}
          </div>
        )}

        {state === 'recording' && (
          <button id="voice-stop-btn" onClick={stopAndUpload} style={primaryBtnStyle('#ef4444')}>
            ■ Stop & Analyze
          </button>
        )}
        {(state === 'done' || state === 'error') && (
          <button id="voice-reset-btn" onClick={reset} style={primaryBtnStyle('#6b7280')}>
            ↩ Record Again
          </button>
        )}
        {(state === 'uploading' || state === 'processing') && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, opacity: 0.8, fontSize: 14 }}>
            <span style={{ animation: 'spin 1s linear infinite', display: 'inline-block' }}>⟳</span>
            {state === 'uploading' ? 'Uploading audio…' : 'Analyzing transcript…'}
          </div>
        )}
      </div>

      {state === 'done' && finishedNote && (
        <div style={{ marginTop: 20, padding: 16, background: 'rgba(99,102,241,0.15)', borderRadius: 12, border: '1px solid rgba(99,102,241,0.3)' }}>
          <p style={{ margin: '0 0 6px', fontSize: 13, fontWeight: 600, color: '#a5b4fc' }}>✦ AI Summary</p>
          <p style={{ margin: 0, fontSize: 13, opacity: 0.9, lineHeight: 1.5 }}>{finishedNote.summary}</p>
          {finishedNote.sentiment && (
            <span style={{ marginTop: 8, display: 'inline-block', padding: '2px 10px', borderRadius: 100, fontSize: 11, fontWeight: 600, background: sentimentColor(finishedNote.sentiment) }}>
              {finishedNote.sentiment}
            </span>
          )}
        </div>
      )}

      <style>{`
        @keyframes pulse { 0%,100%{opacity:1} 50%{opacity:.4} }
        @keyframes spin { to{transform:rotate(360deg)} }
      `}</style>
    </div>
  );
}

function sentimentColor(s: string) {
  if (s === 'positive') return 'rgba(34,197,94,0.3)';
  if (s === 'negative') return 'rgba(239,68,68,0.3)';
  if (s === 'mixed') return 'rgba(245,158,11,0.3)';
  return 'rgba(107,114,128,0.3)';
}

const inputStyle: React.CSSProperties = {
  width: '100%', padding: '8px 12px', borderRadius: 8,
  background: 'rgba(255,255,255,0.1)', border: '1px solid rgba(255,255,255,0.2)',
  color: '#fff', fontSize: 13, outline: 'none', boxSizing: 'border-box',
};

const dropdownStyle: React.CSSProperties = {
  background: '#1e1b4b', border: '1px solid rgba(99,102,241,0.4)',
  borderRadius: 8, marginTop: 4, maxHeight: 160, overflowY: 'auto',
};

const dropdownItemStyle = (selected: boolean): React.CSSProperties => ({
  padding: '7px 12px', cursor: 'pointer', fontSize: 13,
  background: selected ? 'rgba(99,102,241,0.3)' : 'transparent',
  transition: 'background 0.15s',
});

const primaryBtnStyle = (bg: string): React.CSSProperties => ({
  padding: '10px 24px', borderRadius: 10, border: 'none', cursor: 'pointer',
  background: `linear-gradient(135deg, ${bg}, ${bg}cc)`,
  color: '#fff', fontSize: 14, fontWeight: 600, letterSpacing: '0.3px',
  boxShadow: `0 4px 15px ${bg}55`, transition: 'transform 0.1s, box-shadow 0.1s',
});
