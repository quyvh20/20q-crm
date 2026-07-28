import React, { useEffect, useState } from 'react';
import { Check, Copy, Loader2, Monitor, Moon, Smartphone, Sun, UserRound, X } from 'lucide-react';
import Modal from '../../../components/common/Modal';
import { getContacts, type Contact } from '../../../lib/api';
import { previewContent, type ContentInput, type PreviewResult } from '../contentApi';

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
  const [search, setSearch] = useState('');
  const [results, setResults] = useState<Contact[]>([]);
  const [searching, setSearching] = useState(false);
  const [resolved, setResolved] = useState<PreviewResult | null>(null);
  const [resolving, setResolving] = useState(false);

  // Debounced contact search (only while the picker has a query).
  useEffect(() => {
    if (!open || search.trim() === '') {
      setResults([]);
      return;
    }
    let cancelled = false;
    setSearching(true);
    const t = setTimeout(() => {
      getContacts({ q: search.trim(), limit: 8 })
        .then((r) => { if (!cancelled) setResults(r.contacts ?? []); })
        .catch(() => { if (!cancelled) setResults([]); })
        .finally(() => { if (!cancelled) setSearching(false); });
    }, 300);
    return () => { cancelled = true; clearTimeout(t); };
  }, [open, search]);

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

  const contactLabel = (c: Contact) =>
    [c.first_name, c.last_name].filter(Boolean).join(' ') || c.email || 'Unnamed contact';

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

        {/* View-as picker */}
        <div className="relative flex items-center gap-2">
          {contact ? (
            <span className="flex items-center gap-1.5 rounded-full bg-primary/10 py-1 pl-2.5 pr-1 text-xs font-medium text-primary">
              <UserRound className="h-3.5 w-3.5" />
              {contactLabel(contact)}
              {resolving && <Loader2 className="h-3 w-3 animate-spin" />}
              <button type="button" aria-label="Back to sample data" title="Back to sample data"
                onClick={() => { setContact(null); setSearch(''); }}
                className="rounded-full p-0.5 hover:bg-primary/20">
                <X className="h-3 w-3" />
              </button>
            </span>
          ) : (
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="View as contact…"
              aria-label="Search contacts to preview as"
              className="w-44 rounded-lg border border-border bg-background px-2.5 py-1.5 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
            />
          )}
          {!contact && search.trim() !== '' && (
            <div className="absolute right-0 top-full z-50 mt-1 w-64 overflow-hidden rounded-xl border border-border bg-popover py-1 text-popover-foreground shadow-2xl">
              {searching ? (
                <p className="px-3 py-2 text-xs text-muted-foreground">Searching…</p>
              ) : results.length === 0 ? (
                <p className="px-3 py-2 text-xs text-muted-foreground">No matching contacts</p>
              ) : (
                results.map((c) => (
                  <button key={c.id} type="button" onClick={() => { setContact(c); setSearch(''); }}
                    className="flex w-full flex-col px-3 py-1.5 text-left hover:bg-accent hover:text-accent-foreground">
                    <span className="text-xs font-medium">{contactLabel(c)}</span>
                    {c.email && <span className="text-[11px] text-muted-foreground">{c.email}</span>}
                  </button>
                ))
              )}
            </div>
          )}
        </div>

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
            srcDoc={dark ? `<div style="background:#0b0b0c;padding:12px">${active.html}</div>` : active.html}
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
