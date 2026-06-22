import { useState, useRef, useEffect } from 'react';
import { globalSearch, type SearchGroup } from '../../lib/api';

// GlobalSearch is the P6 cross-object search palette: one Ctrl+K box that spans
// every searchable object (custom objects + contacts), grouped by object, backed
// by GET /api/registry/search. Results are already OLS/FLS-filtered server-side,
// so a user only ever sees what they may read. It reuses the look of the legacy
// contact SearchBar and replaces it in the shell when the objects.search flag is on.

// hrefFor maps a result to the best available route. Deals have a detail page;
// contacts and custom objects route to their list area (no per-record route yet).
function hrefFor(object: string, id: string): string {
  if (object === 'deal') return `/deals/${id}`;
  if (object === 'contact') return '/contacts';
  return `/objects/${object}`;
}

export default function GlobalSearch() {
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

  if (!isOpen) {
    return (
      <button id="global-search-trigger" onClick={() => setIsOpen(true)} className="gsearch-trigger" aria-label="Open search">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" /></svg>
        <span>Search everything…</span>
        <kbd>⌘K</kbd>
      </button>
    );
  }

  return (
    <div className="gsearch-overlay" onClick={(e) => { if (e.target === e.currentTarget) setIsOpen(false); }}>
      <div className="gsearch-modal">
        <div className="gsearch-input-row">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#6b7280" strokeWidth="2"><circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" /></svg>
          <input
            ref={inputRef}
            id="global-search-input"
            type="text"
            placeholder="Search across every object…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="gsearch-input"
          />
          {loading && <span className="gsearch-spinner" />}
          <button onClick={() => setIsOpen(false)} className="gsearch-escape">ESC</button>
        </div>

        <div className="gsearch-banner">
          <span>✦</span>
          <span>Semantic + full-text search across all searchable objects</span>
        </div>

        {totalHits > 0 && (
          <div className="gsearch-results">
            {groups.map((g) => (
              <div key={g.object} className="gsearch-group">
                <div className="gsearch-group-header">
                  <span>{g.icon}</span>
                  <span>{g.label_plural}</span>
                  <span className="gsearch-group-count">{g.hits.length}</span>
                </div>
                <ul>
                  {g.hits.map((h) => (
                    <li key={h.record.id}>
                      <a className="gsearch-item" href={hrefFor(g.object, h.record.id)}>
                        <span className="gsearch-icon">{g.icon}</span>
                        <span className="gsearch-item-name">{h.record.display || '(untitled)'}</span>
                        {h.score ? (
                          <span className="gsearch-score" title="Semantic similarity">
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

        {searched && !loading && totalHits === 0 && (
          <div className="gsearch-empty">No results for "{query}"</div>
        )}
      </div>

      <style>{`
        .gsearch-trigger { display: flex; align-items: center; gap: 6px; padding: 6px 12px; background: var(--muted, #f3f4f6); border: 1px solid var(--border, #e5e7eb); border-radius: 8px; cursor: pointer; color: #6b7280; font-size: 13px; transition: all 0.15s; }
        .gsearch-trigger:hover { border-color: #6366f1; color: #6366f1; }
        .gsearch-trigger kbd { margin-left: 8px; padding: 1px 5px; background: var(--background, #fff); border: 1px solid var(--border, #e5e7eb); border-radius: 4px; font-size: 11px; }
        .gsearch-overlay { position: fixed; inset: 0; z-index: 2000; background: rgba(0,0,0,0.4); backdrop-filter: blur(2px); display: flex; align-items: flex-start; justify-content: center; padding-top: 80px; animation: gsFade 0.15s ease; }
        @keyframes gsFade { from { opacity: 0; } }
        .gsearch-modal { width: 560px; background: var(--background, #fff); border-radius: 16px; box-shadow: 0 32px 80px rgba(0,0,0,0.25); overflow: hidden; animation: gsSlide 0.15s ease; }
        @keyframes gsSlide { from { transform: translateY(-8px); opacity: 0; } }
        .gsearch-input-row { display: flex; align-items: center; gap: 10px; padding: 14px 16px; border-bottom: 1px solid var(--border, #e5e7eb); }
        .gsearch-input { flex: 1; border: none; outline: none; font-size: 15px; background: transparent; color: var(--foreground, #111); }
        .gsearch-escape { padding: 2px 6px; background: var(--muted, #f3f4f6); border: 1px solid var(--border, #e5e7eb); border-radius: 4px; font-size: 11px; color: #6b7280; cursor: pointer; }
        .gsearch-spinner { width: 14px; height: 14px; border: 2px solid #e5e7eb; border-top-color: #6366f1; border-radius: 50%; animation: gsSpin 0.6s linear infinite; flex-shrink: 0; }
        @keyframes gsSpin { to { transform: rotate(360deg); } }
        .gsearch-banner { display: flex; align-items: center; gap: 8px; padding: 8px 16px; background: linear-gradient(to right, #eef2ff, #faf5ff); font-size: 12px; color: #4f46e5; border-bottom: 1px solid #e0e7ff; }
        .gsearch-results { padding: 8px; max-height: 420px; overflow-y: auto; }
        .gsearch-group { margin-bottom: 8px; }
        .gsearch-group-header { display: flex; align-items: center; gap: 6px; padding: 6px 10px; font-size: 11px; font-weight: 700; color: #6b7280; text-transform: uppercase; letter-spacing: 0.04em; }
        .gsearch-group-count { margin-left: auto; background: var(--muted, #f3f4f6); border-radius: 99px; padding: 0 7px; font-size: 10px; color: #9ca3af; }
        .gsearch-group ul { list-style: none; margin: 0; padding: 0; }
        .gsearch-item { display: flex; align-items: center; gap: 10px; width: 100%; padding: 8px 10px; border-radius: 10px; background: transparent; cursor: pointer; text-align: left; text-decoration: none; transition: background 0.1s; }
        .gsearch-item:hover { background: var(--muted, #f3f4f6); }
        .gsearch-icon { width: 28px; height: 28px; border-radius: 8px; background: linear-gradient(135deg, #6366f1, #8b5cf6); color: #fff; display: flex; align-items: center; justify-content: center; flex-shrink: 0; font-size: 14px; }
        .gsearch-item-name { flex: 1; font-size: 13px; font-weight: 500; color: var(--foreground, #111); }
        .gsearch-score { padding: 2px 7px; background: linear-gradient(135deg, #6366f1, #8b5cf6); color: #fff; border-radius: 99px; font-size: 10px; font-weight: 700; white-space: nowrap; }
        .gsearch-empty { padding: 20px; text-align: center; color: #9ca3af; font-size: 13px; }
      `}</style>
    </div>
  );
}
