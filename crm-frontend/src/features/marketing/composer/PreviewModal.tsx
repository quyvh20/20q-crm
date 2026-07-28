import React, { useState } from 'react';
import { Monitor, Moon, Smartphone, Sun } from 'lucide-react';
import Modal from '../../../components/common/Modal';
import type { PreviewResult } from '../contentApi';

interface Props {
  open: boolean;
  onClose: () => void;
  preview: PreviewResult | null;
  previewErr: boolean;
}

/** PreviewModal shows the SERVER-compiled email (mjml-go output — the exact HTML
 *  recipients get, footer included) in a sandboxed iframe, with desktop/mobile
 *  widths and a dark-scheme toggle. */
export const PreviewModal: React.FC<Props> = ({ open, onClose, preview, previewErr }) => {
  const [device, setDevice] = useState<'desktop' | 'mobile'>('desktop');
  const [dark, setDark] = useState(false);

  const width = device === 'desktop' ? 640 : 375;

  return (
    <Modal open={open} onClose={onClose} title="Email preview" widthClass="max-w-[720px]" padded={false}>
      <div className="flex items-center justify-between border-b border-border px-4 py-2">
        <div className="flex items-center gap-1">
          <DeviceButton active={device === 'desktop'} title="Desktop width" onClick={() => setDevice('desktop')}>
            <Monitor className="h-4 w-4" />
          </DeviceButton>
          <DeviceButton active={device === 'mobile'} title="Mobile width" onClick={() => setDevice('mobile')}>
            <Smartphone className="h-4 w-4" />
          </DeviceButton>
          <div className="mx-1 h-4 w-px bg-border" />
          <DeviceButton active={dark} title="Toggle dark scheme" onClick={() => setDark((d) => !d)}>
            {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </DeviceButton>
        </div>
        {preview?.size_bytes != null && (
          <span className={`text-[11px] ${preview.too_large ? 'font-medium text-destructive' : 'text-muted-foreground'}`}>
            {Math.round(preview.size_bytes / 1024)} KB{preview.too_large ? ' (over 100KB!)' : ''}
          </span>
        )}
      </div>

      <div className="flex justify-center overflow-auto bg-muted/50 p-4" style={{ maxHeight: '70vh' }}>
        {preview?.compile_error ? (
          <p className="py-16 text-sm text-destructive">Couldn’t compile: {preview.compile_error}</p>
        ) : previewErr || !preview?.html ? (
          <p className="py-16 text-sm text-muted-foreground">Preview unavailable — it’ll refresh on your next edit.</p>
        ) : (
          <iframe
            title="Compiled email preview"
            // sandbox="" = opaque origin, no scripts/forms/same-origin — static
            // email HTML renders, anything active is dead.
            sandbox=""
            style={{ width, height: '62vh', background: dark ? '#0b0b0c' : '#ffffff' }}
            className="rounded-lg shadow-md ring-1 ring-black/5"
            srcDoc={dark ? `<div style="background:#0b0b0c;padding:12px">${preview.html}</div>` : preview.html}
          />
        )}
      </div>
    </Modal>
  );
};

const DeviceButton: React.FC<{ active: boolean; title: string; onClick: () => void; children: React.ReactNode }> = ({ active, title, onClick, children }) => (
  <button
    type="button"
    title={title}
    aria-label={title}
    aria-pressed={active}
    onClick={onClick}
    className={`flex h-7 w-7 items-center justify-center rounded transition-colors ${
      active ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'
    }`}
  >
    {children}
  </button>
);

export default PreviewModal;
