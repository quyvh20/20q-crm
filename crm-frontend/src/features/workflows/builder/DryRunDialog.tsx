// A3.5 dry-run sample picker: a token-styled Radix dialog for choosing a sample
// contact or deal to test the workflow against. Mirrors EntityPicker's debounced
// search, but themed for the new builder (the legacy EntityPicker is dark-only and
// bound to RunNowModal). Picking a record hands its id up; the parent runs the
// dry-run mutation and shows the overlay.

import { useEffect, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { Search, Loader2, FlaskConical } from 'lucide-react';
import { getContacts, getDeals } from '../../../lib/api';
import type { Contact, Deal } from '../../../lib/api';
import type { EntityCandidate } from '../EntityPicker';

const DEBOUNCE_MS = 300;
const RESULT_LIMIT = 10;

function contactToCandidate(c: Contact): EntityCandidate {
  const name = `${c.first_name ?? ''} ${c.last_name ?? ''}`.trim();
  return { id: c.id, label: name || c.email || c.id, sublabel: c.email || c.phone || undefined };
}
function dealToCandidate(d: Deal): EntityCandidate {
  const parts: string[] = [];
  if (d.stage?.name) parts.push(d.stage.name);
  if (typeof d.value === 'number') parts.push(d.value.toLocaleString(undefined, { style: 'currency', currency: 'USD' }));
  return { id: d.id, label: d.title || d.id, sublabel: parts.length ? parts.join(' · ') : undefined };
}

interface Props {
  kind: 'contact' | 'deal';
  /** True while the parent's dry-run mutation is in flight. */
  running: boolean;
  error: string | null;
  onPick: (candidate: EntityCandidate) => void;
  onClose: () => void;
}

export function DryRunDialog({ kind, running, error, onPick, onClose }: Props) {
  const [query, setQuery] = useState('');
  const [debounced, setDebounced] = useState('');
  const [results, setResults] = useState<EntityCandidate[]>([]);
  const [loading, setLoading] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const reqIdRef = useRef(0);

  useEffect(() => {
    const t = setTimeout(() => setDebounced(query.trim()), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [query]);

  useEffect(() => {
    const reqId = ++reqIdRef.current;
    setLoading(true);
    setSearchError(null);
    (async () => {
      try {
        const q = debounced || undefined;
        let candidates: EntityCandidate[];
        if (kind === 'contact') {
          const { contacts } = await getContacts({ q, limit: RESULT_LIMIT });
          candidates = (contacts || []).map(contactToCandidate);
        } else {
          const { deals } = await getDeals({ q, limit: RESULT_LIMIT });
          candidates = (deals || []).map(dealToCandidate);
        }
        if (reqId !== reqIdRef.current) return;
        setResults(candidates);
      } catch (e) {
        if (reqId !== reqIdRef.current) return;
        setSearchError(e instanceof Error ? e.message : `Failed to search ${kind}s`);
        setResults([]);
      } finally {
        if (reqId === reqIdRef.current) setLoading(false);
      }
    })();
  }, [debounced, kind]);

  return (
    <Dialog.Root open onOpenChange={(o) => !o && !running && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[460px] max-w-[92vw] -translate-x-1/2 -translate-y-1/2 rounded-xl border border-border bg-card p-4 text-card-foreground shadow-xl focus:outline-none">
          <div className="mb-1 flex items-center gap-2">
            <FlaskConical className="h-4 w-4 text-primary" />
            <Dialog.Title className="text-sm font-semibold text-foreground">Test run</Dialog.Title>
          </div>
          <Dialog.Description className="mb-3 text-xs text-muted-foreground">
            Pick a sample {kind} to preview which steps would run — no emails, tasks, or changes are made.
          </Dialog.Description>

          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              autoFocus
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={kind === 'contact' ? 'Search contacts by name, email, company, or phone…' : 'Search deals by title…'}
              aria-label={`Search ${kind}s`}
              className="w-full rounded-lg border border-border bg-background py-2 pl-8 pr-3 text-sm text-foreground placeholder:text-muted-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="mt-3 max-h-64 overflow-y-auto">
            {loading ? (
              <div className="flex items-center gap-2 py-3 text-sm text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" /> Searching…
              </div>
            ) : searchError ? (
              <p className="py-3 text-sm text-destructive">{searchError}</p>
            ) : results.length === 0 ? (
              <p className="py-3 text-sm text-muted-foreground">
                {debounced ? `No ${kind}s match "${debounced}".` : `No ${kind}s found.`}
              </p>
            ) : (
              <ul className="space-y-1.5" role="listbox" aria-label={`${kind} results`}>
                {results.map((c) => (
                  <li key={c.id}>
                    <button
                      type="button"
                      role="option"
                      aria-selected={false}
                      disabled={running}
                      onClick={() => onPick(c)}
                      className="w-full rounded-lg border border-border bg-background px-3 py-2 text-left transition-colors hover:border-ring/60 hover:bg-accent disabled:opacity-50"
                    >
                      <span className="block truncate text-sm font-medium text-foreground">{c.label}</span>
                      {c.sublabel && <span className="block truncate text-xs text-muted-foreground">{c.sublabel}</span>}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {running && (
            <p className="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> Running dry run…
            </p>
          )}
          {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}

          <div className="mt-3 flex justify-end">
            <button
              type="button"
              onClick={onClose}
              disabled={running}
              className="rounded-md border border-border px-3 py-1.5 text-sm text-foreground hover:bg-muted disabled:opacity-50"
            >
              Cancel
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
