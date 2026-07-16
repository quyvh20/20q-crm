import { useState, useRef, useEffect } from 'react';
import { Loader2, Search, Settings, Sparkles } from 'lucide-react';
import { globalSearch, type SearchGroup } from '../../lib/api';
import { recordPath } from '../../features/objects/recordRoutes';
import { useAuth } from '../../lib/auth';
import { visibleSections } from '../../pages/settings/SettingsLayout';

// GlobalSearch is the P6 cross-object search palette: one Ctrl+K box that spans
// every searchable object (custom objects + contacts), grouped by object, backed
// by GET /api/registry/search. Results are already OLS/FLS-filtered server-side,
// so a user only ever sees what they may read. It reuses the look of the legacy
// contact SearchBar and replaces it in the shell when the objects.search flag is on.

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
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [groups, setGroups] = useState<SearchGroup[]>([]);
  const [loading, setLoading] = useState(false);
  const [searched, setSearched] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Open with Ctrl/Cmd+K, close with Escape.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setIsOpen(true);
      }
      if (e.key === 'Escape') setIsOpen(false);
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, []);

  useEffect(() => {
    if (isOpen) setTimeout(() => inputRef.current?.focus(), 50);
  }, [isOpen]);

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
      } catch {
        setGroups([]);
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

  if (!isOpen) {
    return (
      <button
        id="global-search-trigger"
        onClick={() => setIsOpen(true)}
        aria-label="Open search"
        className="inline-flex items-center gap-2 rounded-lg border border-input bg-background px-3 py-1.5 text-[13px] text-muted-foreground shadow-sm transition-colors hover:text-foreground hover:border-ring/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Search aria-hidden className="h-3.5 w-3.5" />
        <span>Search everything…</span>
        <kbd className="ml-2 rounded border border-border bg-muted px-1.5 py-0.5 font-sans text-[11px]">⌘K</kbd>
      </button>
    );
  }

  const groupHeaderClass =
    'flex items-center gap-1.5 px-2.5 py-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground';
  const itemClass =
    'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left no-underline transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
  const itemIconClass =
    'flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-sm';
  const countClass =
    'ml-auto rounded-full bg-muted px-2 text-[10px] font-medium text-muted-foreground';

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/50 px-4 pt-20 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget) setIsOpen(false); }}
    >
      <div className="w-full max-w-[560px] overflow-hidden rounded-2xl border border-border bg-card text-card-foreground shadow-2xl">
        <div className="flex items-center gap-2.5 border-b border-border px-4 py-3.5">
          <Search aria-hidden className="h-4 w-4 shrink-0 text-muted-foreground" />
          <input
            ref={inputRef}
            id="global-search-input"
            type="text"
            placeholder="Search across every object…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="flex-1 bg-transparent text-[15px] text-foreground placeholder:text-muted-foreground focus:outline-none"
          />
          {loading && <Loader2 aria-hidden className="h-4 w-4 shrink-0 animate-spin text-primary" />}
          <button
            onClick={() => setIsOpen(false)}
            className="rounded border border-border bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
          >
            ESC
          </button>
        </div>

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
                      <a className={itemClass} href={s.externalTo ?? `/settings/${s.path}`}>
                        <span className={itemIconClass}>
                          <Settings aria-hidden className="h-3.5 w-3.5 text-primary" />
                        </span>
                        <span className="flex-1 truncate text-[13px] font-medium text-foreground">{s.label}</span>
                      </a>
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
                      <a className={itemClass} href={hrefFor(g.object, h.record.id)}>
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
                      </a>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}

        {searched && !loading && totalHits === 0 && settingsHits.length === 0 && (
          <div className="p-5 text-center text-[13px] text-muted-foreground">No results for "{query}"</div>
        )}
      </div>
    </div>
  );
}
