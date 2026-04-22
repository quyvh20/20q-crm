import { useState } from 'react';
import VoiceUploader from '../components/voice/VoiceUploader';
import VoiceLibrary from '../components/voice/VoiceLibrary';
import type { VoiceNote } from '../lib/api';

export default function VoicePage() {
  const [refreshKey, setRefreshKey] = useState(0);

  const handleComplete = (_note: VoiceNote) => {
    setRefreshKey((k) => k + 1);
  };

  return (
    <div style={{
      minHeight: '100vh',
      background: 'linear-gradient(135deg, #0f0c29 0%, #302b63 50%, #1a1a2e 100%)',
      padding: '32px 24px',
      fontFamily: "'Inter', sans-serif",
    }}>
      <div style={{ maxWidth: 1140, margin: '0 auto' }}>

        {/* ── Page header ── */}
        <div style={{ marginBottom: 32 }}>
          <h1 style={{ margin: 0, fontSize: 28, fontWeight: 800, color: '#fff', letterSpacing: '-1px', display: 'flex', alignItems: 'center', gap: 12 }}>
            <span style={{ fontSize: 30 }}>🎙</span>
            Voice Intelligence
          </h1>
          <p style={{ margin: '6px 0 0', color: 'rgba(255,255,255,0.5)', fontSize: 14 }}>
            Upload audio files · AI transcription · Automatic CRM updates
          </p>
        </div>

        {/* ── Two-column grid ── */}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1.5fr', gap: 28, alignItems: 'start' }}>

          {/* Left column: Upload */}
          <div>
            <VoiceUploader onUploadComplete={handleComplete} />
          </div>

          {/* Right column: Library */}
          <div>
            <p style={{ margin: '0 0 14px', fontSize: 13, fontWeight: 600, color: 'rgba(255,255,255,0.5)', textTransform: 'uppercase', letterSpacing: '1px' }}>
              All Voice Notes
            </p>
            <VoiceLibrary key={refreshKey} />
          </div>

        </div>
      </div>
    </div>
  );
}
