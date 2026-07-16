import { useState, type ReactNode } from 'react';
import { AlertTriangle, Check, Copy } from 'lucide-react';
import { Button } from '@/components/ui';

// SecretReveal is the one-time-secret surface (U6): the 2FA backup codes and a new
// personal API token both exist in plaintext for exactly one render, and both need
// the same thing — show it, make it copyable, and make the user acknowledge that
// it's gone once they close.
//
// Pass `value` for a single secret (an API token) or `values` for a list (backup
// codes). The Done button stays disabled until the acknowledgement is ticked, so
// the codes can't be dismissed by reflex.

interface SecretRevealProps {
  title: string;
  description?: ReactNode;
  /** A single one-time secret, e.g. a `crm_pat_…` token. */
  value?: string;
  /** A list of one-time secrets, e.g. the 10 backup codes. */
  values?: string[];
  /** Label of the acknowledgement checkbox. */
  acknowledgeLabel?: string;
  doneLabel?: string;
  onDone: () => void;
}

// copyText copies via the async clipboard API, falling back to a hidden textarea
// (older browsers, and any non-secure context, where navigator.clipboard is absent).
async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to the legacy path
  }
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

export default function SecretReveal({
  title,
  description,
  value,
  values,
  acknowledgeLabel = "I've saved this somewhere safe",
  doneLabel = 'Done',
  onDone,
}: SecretRevealProps) {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState('');
  const [acknowledged, setAcknowledged] = useState(false);

  const list = values ?? [];
  const payload = value ?? list.join('\n');

  const handleCopy = async () => {
    setCopyError('');
    const ok = await copyText(payload);
    if (!ok) {
      setCopyError('Copy failed — select the text above and copy it manually.');
      return;
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="rounded-xl border border-amber-500/40 bg-amber-500/5 p-4 space-y-3">
      <div className="flex items-start gap-2">
        <AlertTriangle className="w-4 h-4 text-amber-600 dark:text-amber-400 mt-0.5 shrink-0" />
        <div>
          <h3 className="text-sm font-semibold text-foreground">{title}</h3>
          <p className="text-xs text-muted-foreground mt-0.5">
            {description ?? "This is the only time you'll see this. Copy it now — it can't be shown again."}
          </p>
        </div>
      </div>

      {value !== undefined && (
        <code
          data-testid="secret-value"
          className="block w-full break-all rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs text-foreground"
        >
          {value}
        </code>
      )}

      {values !== undefined && (
        <ul data-testid="secret-list" className="grid grid-cols-2 gap-1.5">
          {list.map((c) => (
            <li
              key={c}
              className="rounded-md border border-border bg-background px-2 py-1.5 text-center font-mono text-xs text-foreground"
            >
              {c}
            </li>
          ))}
        </ul>
      )}

      {copyError && <p className="text-xs text-destructive">{copyError}</p>}

      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="outline" size="sm" onClick={handleCopy}>
          {copied ? <Check className="text-emerald-600 dark:text-emerald-400" /> : <Copy />}
          {copied ? 'Copied' : list.length > 1 ? 'Copy all' : 'Copy'}
        </Button>

        <label className="flex items-center gap-2 text-xs text-muted-foreground cursor-pointer">
          <input
            type="checkbox"
            checked={acknowledged}
            onChange={(e) => setAcknowledged(e.target.checked)}
            className="rounded border-input"
          />
          {acknowledgeLabel}
        </label>

        <Button type="button" size="sm" onClick={onDone} disabled={!acknowledged} className="ml-auto">
          {doneLabel}
        </Button>
      </div>
    </div>
  );
}
