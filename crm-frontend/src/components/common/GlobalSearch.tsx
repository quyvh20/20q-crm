import { useEffect, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Loader2, Search, Settings, Sparkles } from 'lucide-react';
import { globalSearch, type SearchGroup } from '../../lib/api';
import { recordPath } from '../../features/objects/recordRoutes';
import { useAuth } from '../../lib/auth';
// From ./sections, NOT from SettingsLayout: this component is mounted in
// AppLayout and therefore lives in the eager entry chunk, so importing the
// settings SHELL here would pin that lazy route into the first load.
import { visibleSections } from '../../pages/settings/sections';
import { ErrorState } from '@/components/ui';
// Modal is ALREADY in the first load (index.html modulepreloads Modal-*.js for
// AppLayout's mobile nav), so composing it here costs no extra bytes — unlike
// the settings shell above, which is why that one is imported through sections.ts.
import Modal from './Modal';

// GlobalSearch is the P6 cross-object search palette: one Ctrl+K box that spans
// every searchable object (custom objects + contacts), grouped by object, backed
// by GET /api/registry/search. Results are already OLS/FLS-filtered server-side,
// so a user only ever sees what they may read. It reuses the look of the legacy
// contact SearchBar and replaces it in the shell when the objects.search flag is on.
//
// R7.5 — TWO defects, one structural and one navigational:
//
//  1. The overlay was a hand-rolled `fixed inset-0` div: no role="dialog", no
//     aria-modal, no focus trap, no focus restore, and it REPLACED its own
//     trigger button in the tree, so there was nothing left to restore focus to.
//     It now composes Modal.tsx (Radix) like every other overlay in the app.
//  2. Every result was a raw <a href>, i.e. a full page reload. Post-R6 that is
//     the expensive path by a wide margin: a reload re-downloads and re-executes
//     the whole eager shell — index + rolldown-runtime + react-vendor + ui + x +
//     Modal + useQuery + bell + api + index.css = 593 kB raw / 170 kB gzip across
//     10 files — then re-boots React, re-runs the auth bootstrap and throws away
//     the React Query cache. Client-side navigation to the same destination
//     fetches ONE route chunk: 6.6 kB / 2.6 kB gzip for ObjectRecordPage,
//     31 kB / 8.1 kB for DealDetailPage, 4.1 kB / 3.1 kB gzip for a settings
//     section (shell + section). Links here are <Link>s for that reason.
//
// Keyboard model: the input and the results form ONE focus ring. ArrowDown /
// ArrowUp move real DOM focus along it (so assistive tech announces each result
// as an ordinary link, and Enter activates it natively); Enter in the input
// opens the top result; Escape and the focus trap are Radix's. Focus is NOT set
// with autoFocus — the input is simply the first tabbable node in the dialog, so
// Radix's focus scope lands there on open, which is the doctrine for Modal.

// hrefFor maps a result to its URL-addressable record page. Deals keep their
// bespoke /deals/:id page; every other object lands on the unified record page.
function hrefFor(object: string, id: string): string {
  return recordPath(object, id);
}

export default function GlobalSearch() {
  // Tolerate mounting outside an AuthProvider (unit tests render the palette
  // bare): without auth context there are simply no settings destinations.
  let hasCapability: (cap: string) => boolean = () => false;
  try {
    hasCapability = useAuth().hasCapability; // eslint-disable-line react-hooks/rules-of-hooks
  } catch {
    /* no provider — records search still works */
  }
  const navigate = useNavigate();
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [groups, setGroups] = useState<SearchGroup[]>([]);
  const [loading, setLoading] = useState(false);
  const [searched, setSearched] = useState(false);
  // A search that FAILED must not render as a search that found nothing.
  const [searchError, setSearchError] = useState<unknown>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Open with Ctrl/Cmd+K. Escape is no longer handled here: the dialog owns its
  // own dismissal, and a document-level listener would also fire for Escapes
  // meant for something layered above it.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        setIsOpen(true);
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, []);

  // Debounced cross-object search.
  useEffect(() => {
    if (!query.trim()) {
      setGroups([]);
      setSearched(false);
      return;
    }
    clearTimeout(timeoutRef.current);
    timeoutRef.current = setTimeout(async () => {
      setLoading(true);
      try {
        const res = await globalSearch(query, 6);
        setGroups(res.groups);
        setSearchError(null);
      } catch (e) {
        setGroups([]);
        setSearchError(e);
      } finally {
        setLoading(false);
        setSearched(true);
      }
    }, 300);
    return () => clearTimeout(timeoutRef.current);
  }, [query]);

  const totalHits = groups.reduce((n, g) => n + g.hits.length, 0);

  // Settings destinations (U1.5): the palette also jumps to any settings
  // section the member can see — matched by label, or all of them when the
  // query is "set…"/"settings".
  const q = query.trim().toLowerCase();
  const showAllSettings = q.length >= 3 && 'settings'.startsWith(q);
  const settingsHits = q
    ? visibleSections(hasCapability).filter(
        (s) => showAllSettings || s.label.toLowerCase().includes(q)
      )
    : [];

  const settingsTo = (path: string, externalTo?: string) => externalTo ?? `/settings/${path}`;

  // Every destination in render order — the same order the results are painted
  // in, so "Enter opens the top result" and the arrow-key ring agree.
  const destinations = [
    ...settingsHits.map((s) => settingsTo(s.path, s.externalTo)),
    ...groups.flatMap((g) => g.hits.map((h) => hrefFor(g.object, h.record.id))),
  ];

  const close = () => setIsOpen(false);
  const go = (to: string) => {
    setIsOpen(false);
    navigate(to);
  };

  // Arrow keys walk ONE ring: the input, then the results in document order.
  // Focus is moved for real rather than tracked in state — there is no selection
  // state to keep in sync with the result list (and no effect to sync it in),
  // and a focused <a> is something a screen reader already knows how to read.
  const moveFocus = (dir: 1 | -1) => {
    const items = Array.from(panelRef.current?.querySelectorAll<HTMLElement>('[data-search-result]') ?? []);
    const ring = [inputRef.current, ...items].filter((el): el is HTMLElement => el != null);
    if (ring.length < 2) return;
    const at = ring.indexOf(document.activeElement as HTMLElement);
    ring[((at < 0 ? 0 : at) + dir + ring.length) % ring.length].focus();
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      moveFocus(e.key === 'ArrowDown' ? 1 : -1);
      return;
    }
    // Enter on a result is the anchor's own job. Enter in the box is the
    // "I typed enough, take me to the obvious one" shortcut.
    if (e.key === 'Enter' && e.target === inputRef.current && destinations.length > 0) {
      e.preventDefault();
      go(destinations[0]);
    }
  };

  const groupHeaderClass =
    'flex items-center gap-1.5 px-2.5 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground';
  // focus:bg-accent as well as the ring: the arrow keys move focus programmatically,
  // and :focus-visible heuristics are not something a highlight should depend on.
  const itemClass =
    'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left no-underline transition-colors hover:bg-accent focus:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
  const itemIconClass =
    'flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-sm';
  const countClass =
    'ml-auto rounded-full bg-muted px-2 text-[10px] font-medium text-muted-foreground';

  return (
    <>
      {/* The trigger stays mounted while the palette is open — it is what Modal
          restores focus to on close. The old overlay replaced it, so a keyboard
          user was dropped on <body> every time they dismissed the search. */}
      <button
        id="global-search-trigger"
        onClick={() => setIsOpen(true)}
        aria-label="Open search"
        aria-haspopup="dialog"
        aria-expanded={isOpen}
        className="inline-flex items-center gap-2 rounded-lg border border-input bg-background px-3 py-1.5 text-[13px] text-muted-foreground shadow-sm transition-colors hover:text-foreground hover:border-ring/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Search aria-hidden className="h-3.5 w-3.5" />
        <span>Search everything…</span>
        <kbd className="ml-2 rounded border border-border bg-muted px-1.5 py-0.5 font-sans text-[11px]">⌘K</kbd>
      </button>

      <Modal open={isOpen} onClose={close} title="Search" size="xl" hideHeader padded={false}>
        {/* One keydown handler for the whole panel: the ring spans the input and
            the result links, and they are siblings, not ancestors. */}
        <div ref={panelRef} onKeyDown={onKeyDown}>
          <div className="flex items-center gap-2.5 border-b border-border px-4 py-3.5">
            <Search aria-hidden className="h-4 w-4 shrink-0 text-muted-foreground" />
            <input
              ref={inputRef}
              id="global-search-input"
              type="text"
              placeholder="Search across every object…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-describedby="global-search-hint"
              className="flex-1 bg-transparent text-[15px] text-foreground placeholder:text-muted-foreground focus:outline-none"
            />
            {loading && <Loader2 aria-hidden className="h-4 w-4 shrink-0 animate-spin text-primary" />}
            <button
              onClick={close}
              aria-label="Close search"
              className="rounded border border-border bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
            >
              ESC
            </button>
          </div>
          <p id="global-search-hint" className="sr-only">
            Use the up and down arrow keys to move through the results and Enter to open one. Escape closes the search.
          </p>
          {/* Results arrive asynchronously; without a live region a screen-reader
              user gets no signal that the list under the box has changed. */}
          <p role="status" aria-live="polite" className="sr-only">
            {loading
              ? 'Searching…'
              : destinations.length > 0
                ? `${destinations.length} results for ${query}`
                : ''}
          </p>

          <div className="flex items-center gap-2 border-b border-border bg-primary/5 px-4 py-2 text-xs text-primary">
            <Sparkles aria-hidden className="h-3.5 w-3.5" />
            <span>Semantic + full-text search across all searchable objects</span>
          </div>

          {(totalHits > 0 || settingsHits.length > 0) && (
            <div className="max-h-[420px] overflow-y-auto p-2">
              {settingsHits.length > 0 && (
                <div className="mb-2">
                  <div className={groupHeaderClass}>
                    <Settings aria-hidden className="h-3 w-3" />
                    <span>Settings</span>
                    <span className={countClass}>{settingsHits.length}</span>
                  </div>
                  <ul className="m-0 list-none p-0">
                    {settingsHits.map((s) => (
                      <li key={s.path}>
                        <Link
                          data-search-result
                          className={itemClass}
                          to={settingsTo(s.path, s.externalTo)}
                          onClick={close}
                        >
                          <span className={itemIconClass}>
                            <Settings aria-hidden className="h-3.5 w-3.5 text-primary" />
                          </span>
                          <span className="flex-1 truncate text-[13px] font-medium text-foreground">{s.label}</span>
                        </Link>
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {groups.map((g) => (
                <div key={g.object} className="mb-2">
                  <div className={groupHeaderClass}>
                    {/* g.icon is the object's user-chosen emoji — data, not chrome. */}
                    <span aria-hidden>{g.icon}</span>
                    <span>{g.label_plural}</span>
                    <span className={countClass}>{g.hits.length}</span>
                  </div>
                  <ul className="m-0 list-none p-0">
                    {g.hits.map((h) => (
                      <li key={h.record.id}>
                        <Link
                          data-search-result
                          className={itemClass}
                          to={hrefFor(g.object, h.record.id)}
                          onClick={close}
                        >
                          <span aria-hidden className={itemIconClass}>{g.icon}</span>
                          <span className="flex-1 truncate text-[13px] font-medium text-foreground">{h.record.display || '(untitled)'}</span>
                          {h.score ? (
                            <span
                              className="whitespace-nowrap rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-semibold text-primary"
                              title="Semantic similarity"
                            >
                              {Math.round(h.score * 100)}%
                            </span>
                          ) : null}
                        </Link>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          )}

          {searched && !loading && searchError != null && (
            <div className="p-3">
              <ErrorState compact title="Search failed." error={searchError} />
            </div>
          )}

          {searched && !loading && searchError == null && totalHits === 0 && settingsHits.length === 0 && (
            <div className="p-5 text-center text-[13px] text-muted-foreground">No results for "{query}"</div>
          )}
        </div>
      </Modal>
    </>
  );
}
