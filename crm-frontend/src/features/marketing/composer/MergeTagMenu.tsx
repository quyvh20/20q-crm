import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Braces, Search } from 'lucide-react';
import { isGuaranteed, type VariableGroup } from './mergeScope';

// Per-path fallback memory: the mandatory-fallback rule is right, but retyping
// "there" on every {{contact.first_name}} insert is the most repetitive
// keystroke in the composer. Local (per browser), capped, best-effort.
const FALLBACK_STORE_KEY = 'mkt_merge_fallbacks';

function recallFallback(path: string): string {
  try {
    const map = JSON.parse(localStorage.getItem(FALLBACK_STORE_KEY) ?? '{}') as Record<string, string>;
    return typeof map[path] === 'string' ? map[path] : '';
  } catch {
    return '';
  }
}

function rememberFallback(path: string, fallback: string) {
  if (!fallback) return;
  try {
    const map = JSON.parse(localStorage.getItem(FALLBACK_STORE_KEY) ?? '{}') as Record<string, string>;
    map[path] = fallback;
    const keys = Object.keys(map);
    if (keys.length > 100) delete map[keys[0]];
    localStorage.setItem(FALLBACK_STORE_KEY, JSON.stringify(map));
  } catch {
    // Private mode / quota — memory is a convenience, never a failure.
  }
}

// Roots whose custom_fields.<key> namespace the backend validates (mirrors
// rootsWithCustomFields in content_validate.go).
const CUSTOM_FIELD_ROOTS = new Set(['contact', 'company']);

/** MergeTagMenu is the two-step "insert variable" popover: a searchable grouped
 *  field list (plus free-path custom fields for contact/company), then a
 *  fallback capture step. A fallback is REQUIRED unless the path is guaranteed
 *  (contact.email / org.name) — mirroring the backend's mandatory-fallback
 *  validation — and may not contain { } or | (the {{path|fallback}} grammar
 *  uses [^}], so those silently corrupt the token). */
export const MergeTagMenu: React.FC<{
  variableGroups: VariableGroup[];
  onInsert: (path: string, fallback: string) => void;
}> = ({ variableGroups, onInsert }) => {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [pending, setPending] = useState<{ path: string; label: string } | null>(null);
  // customRoot: the "Custom field…" flow — the user types the field key and the
  // path is assembled as <root>.custom_fields.<key>.
  const [customRoot, setCustomRoot] = useState<string | null>(null);
  const [customKey, setCustomKey] = useState('');
  const [fallback, setFallback] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  // Declared BEFORE the outside-click effect that closes over it. It only ever
  // calls stable setState setters so no stale-closure bug was reachable, but
  // referencing it above its declaration made React Compiler bail out of
  // optimising the whole component. Pure statement reorder, no behaviour change.
  const reset = () => { setOpen(false); setSearch(''); setPending(null); setCustomRoot(null); setCustomKey(''); setFallback(''); };

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) reset(); };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const escClose = (e: React.KeyboardEvent) => {
    if (e.key !== 'Escape') return;
    e.stopPropagation(); // don't let the page-level Escape also deselect the block
    reset();
    triggerRef.current?.focus();
  };

  const groups = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return variableGroups;
    return variableGroups
      .map((g) => ({ ...g, fields: g.fields.filter((f) => f.label.toLowerCase().includes(q) || f.path.toLowerCase().includes(q)) }))
      .filter((g) => g.fields.length > 0);
  }, [variableGroups, search]);

  // The custom-field key becomes part of a {{path}} token — the grammar can't
  // carry braces/pipes/spaces, so gate to a safe identifier shape.
  const customKeyInvalid = customKey !== '' && !/^[A-Za-z0-9_.-]+$/.test(customKey);
  const effective = customRoot
    ? (customKey.trim() && !customKeyInvalid
        ? { path: `${customRoot}.custom_fields.${customKey.trim()}`, label: `${customRoot} custom field: ${customKey.trim()}` }
        : null)
    : pending;

  const guaranteed = effective ? isGuaranteed(effective.path) : false;
  const fallbackInvalid = /[{}|]/.test(fallback);
  const canInsert = !!effective && !fallbackInvalid && (guaranteed || fallback.trim() !== '');

  const pick = (f: { path: string; label: string }) => {
    setPending(f);
    setFallback(recallFallback(f.path));
  };

  const doInsert = () => {
    if (!effective || !canInsert) return;
    rememberFallback(effective.path, fallback.trim());
    onInsert(effective.path, fallback.trim());
    reset();
  };

  return (
    <div className="relative" ref={ref} onKeyDown={escClose}>
      <button type="button" ref={triggerRef} onClick={() => (open ? reset() : setOpen(true))} title="Insert merge tag"
        aria-expanded={open} aria-haspopup="dialog"
        className={`flex items-center gap-1 rounded px-2 py-1 text-xs font-medium transition-colors ${open ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'}`}>
        <Braces className="h-3.5 w-3.5" /> Insert variable
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 flex max-h-80 w-72 flex-col overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-2xl">
          {!pending && !customRoot ? (
            <>
              <div className="border-b border-border px-2 py-2">
                <div className="relative">
                  <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                  <input autoFocus value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search fields…"
                    className="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-2 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring" />
                </div>
              </div>
              <div className="flex-1 overflow-y-auto py-1">
                {groups.length === 0 ? (
                  <p className="px-3 py-3 text-center text-xs text-muted-foreground">No matching fields</p>
                ) : groups.map((g) => (
                  <div key={g.key}>
                    <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{g.label}</div>
                    {g.fields.map((f) => (
                      <button key={f.path} type="button" onClick={() => pick(f)}
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors hover:bg-accent hover:text-accent-foreground">
                        <span className="flex-1 truncate text-xs">{f.label}</span>
                        <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{`{{${f.path}}}`}</code>
                      </button>
                    ))}
                    {CUSTOM_FIELD_ROOTS.has(g.key) && (
                      <button type="button" onClick={() => { setCustomRoot(g.key); setCustomKey(''); setFallback(''); }}
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground">
                        Custom field…
                        <code className="ml-auto shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px]">{`{{${g.key}.custom_fields.*}}`}</code>
                      </button>
                    )}
                  </div>
                ))}
              </div>
            </>
          ) : (
            <div className="p-3">
              {customRoot ? (
                <>
                  <p className="mb-1 text-xs font-medium text-foreground">Custom {customRoot} field</p>
                  <label className="mb-1 block text-[11px] text-muted-foreground">Field key (as defined in your workspace)</label>
                  <input autoFocus value={customKey} onChange={(e) => setCustomKey(e.target.value)}
                    placeholder="e.g. plan_tier"
                    className="mb-2 w-full rounded-lg border border-border/60 bg-background px-2 py-1.5 font-mono text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring" />
                  {customKeyInvalid && <p className="mb-2 text-[11px] text-destructive">Use letters, numbers, dots, dashes or underscores only.</p>}
                </>
              ) : (
                <p className="mb-1 text-xs font-medium text-foreground">{pending!.label}</p>
              )}
              <label className="mb-1 block text-[11px] text-muted-foreground">
                Fallback {guaranteed ? '(optional)' : '(required — shown when the value is empty)'}
              </label>
              <input autoFocus={!customRoot} value={fallback} onChange={(e) => setFallback(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') doInsert(); }}
                placeholder="e.g. there"
                className="w-full rounded-lg border border-border/60 bg-background px-2 py-1.5 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring" />
              {fallbackInvalid && <p className="mt-1 text-[11px] text-destructive">A fallback can’t contain {'{'}, {'}'} or |.</p>}
              <div className="mt-2 flex justify-end gap-2">
                <button type="button" onClick={() => { setPending(null); setCustomRoot(null); setCustomKey(''); }} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:text-foreground">Back</button>
                <button type="button" disabled={!canInsert} onClick={doInsert}
                  className="rounded-lg bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50">Insert</button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default MergeTagMenu;
