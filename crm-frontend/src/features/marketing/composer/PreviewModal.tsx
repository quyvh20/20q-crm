import React, { useEffect, useState } from 'react';
import { Check, Copy, Monitor, Moon, Smartphone, Sun } from 'lucide-react';
import Modal from '../../../components/common/Modal';
import type { Contact } from '../../../lib/api';
import { previewContent, type ContentInput, type PreviewResult } from '../contentApi';
import { ContactPicker, contactLabel } from './ContactPicker';

interface Props {
  open: boolean;
  onClose: () => void;
  preview: PreviewResult | null;
  previewErr: boolean;
  // The current editor state as a preview request — lets the modal re-render
  // the email AS a chosen recipient (real merge data, like a live send).
  previewInput: Omit<ContentInput, 'name'>;
}

/** PreviewModal shows the SERVER-compiled email (mjml-go output — the exact HTML
 *  recipients get, footer included) in a sandboxed iframe, with desktop/mobile
 *  widths (media queries — device-visibility rules really apply), a dark-scheme
 *  toggle, and Brevo-style "view as" any contact in the CRM. */
export const PreviewModal: React.FC<Props> = ({ open, onClose, preview, previewErr, previewInput }) => {
  const [device, setDevice] = useState<'desktop' | 'mobile'>('desktop');
  const [dark, setDark] = useState(false);
  const [copied, setCopied] = useState(false);

  // Recipient picker state.
  const [contact, setContact] = useState<Contact | null>(null);
  const [resolved, setResolved] = useState<PreviewResult | null>(null);
  const [resolving, setResolving] = useState(false);

  // Fetch the resolved render whenever a contact is chosen (or the doc changes
  // while the modal is open as that contact).
  useEffect(() => {
    if (!open || !contact) {
      setResolved(null);
      return;
    }
    let cancelled = false;
    setResolving(true);
    previewContent({ ...previewInput, contact_id: contact.id })
      .then((r) => { if (!cancelled) setResolved(r); })
      .catch(() => { if (!cancelled) setResolved(null); })
      .finally(() => { if (!cancelled) setResolving(false); });
    return () => { cancelled = true; };
  }, [open, contact, previewInput]);

  const active = contact && resolved?.html ? resolved : preview;
  const width = device === 'desktop' ? 640 : 375;

  const copyHtml = async () => {
    if (!active?.html) return;
    try {
      await navigator.clipboard.writeText(active.html);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable (permissions) — nothing to do
    }
  };

  return (
    <Modal open={open} onClose={onClose} title="Email preview" widthClass="max-w-[760px]" padded={false}>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2">
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
          <div className="mx-1 h-4 w-px bg-border" />
          <DeviceButton active={copied} title="Copy compiled HTML" onClick={copyHtml}>
            {copied ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
          </DeviceButton>
        </div>

        {/* View-as picklist */}
        <ContactPicker value={contact} onChange={setContact} busy={resolving} />

        {active?.size_bytes != null && (
          <span className={`text-[11px] ${active.too_large ? 'font-medium text-destructive' : 'text-muted-foreground'}`}>
            {Math.round(active.size_bytes / 1024)} KB{active.too_large ? ' (over 100KB!)' : ''}
          </span>
        )}
      </div>

      {/* Resolved subject line when viewing as a contact */}
      {contact && resolved?.subject_resolved != null && (
        <div className="border-b border-border bg-muted/40 px-4 py-1.5 text-xs text-muted-foreground">
          Subject for {contactLabel(contact)}: <span className="font-medium text-foreground">{resolved.subject_resolved || '(empty)'}</span>
        </div>
      )}
      {contact && resolved?.preview_contact_error && (
        <div className="border-b border-border bg-destructive/10 px-4 py-1.5 text-xs text-destructive">
          {resolved.preview_contact_error}
        </div>
      )}

      <div className="flex justify-center overflow-auto bg-muted/50 p-4" style={{ maxHeight: '68vh' }}>
        {active?.compile_error ? (
          <p className="py-16 text-sm text-destructive">Couldn’t compile: {active.compile_error}</p>
        ) : previewErr || !active?.html ? (
          <p className="py-16 text-sm text-muted-foreground">Preview unavailable — it’ll refresh on your next edit.</p>
        ) : (
          <iframe
            title="Compiled email preview"
            // sandbox="" = opaque origin, no scripts/forms/same-origin — static
            // email HTML renders, anything active is dead.
            sandbox=""
            style={{ width, height: '60vh', background: dark ? '#0b0b0c' : '#ffffff' }}
            className="rounded-lg shadow-md ring-1 ring-black/5"
            // The compiled email declares color-scheme "light dark" (for real
            // dark-mode inboxes) — without pinning, the iframe follows the OS
            // theme and paints a dark canvas under near-black text. The toggle
            // switches the pinned scheme, approximating a dark-mode client.
            srcDoc={`${active.html}<style>:root{color-scheme:${dark ? 'dark' : 'light'}}</style>`}
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
