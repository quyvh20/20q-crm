import { useState } from 'react';
import VoiceUploader from '../components/voice/VoiceUploader';
import VoiceLibrary from '../components/voice/VoiceLibrary';
import { PageHeader } from '../components/ui/page-header';
import type { VoiceNote } from '../lib/api';

export default function VoicePage() {
  const [refreshKey, setRefreshKey] = useState(0);

  const handleComplete = (_note: VoiceNote) => {
    setRefreshKey((k) => k + 1);
  };

  return (
    <div className="mx-auto w-full max-w-6xl">
      <PageHeader
        title="Voice Intelligence"
        description="Upload audio files · AI transcription · Automatic CRM updates"
      />

      {/* ── Two-column grid ── */}
      <div className="grid items-start gap-7 lg:grid-cols-[1fr_1.5fr]">
        {/* Left column: Upload */}
        <div>
          <VoiceUploader onUploadComplete={handleComplete} />
        </div>

        {/* Right column: Library */}
        <div>
          <p className="mb-3.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            All Voice Notes
          </p>
          <VoiceLibrary key={refreshKey} />
        </div>
      </div>
    </div>
  );
}
