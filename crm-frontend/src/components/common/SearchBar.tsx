import { useState, useRef, useEffect } from 'react';
import { getContacts } from '../../lib/api';
import type { Contact } from '../../lib/api';

interface SearchBarProps {
  onSelectContact?: (contact: Contact) => void;
}

export default function SearchBar({ onSelectContact }: SearchBarProps) {
  const [isOpen, setIsOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<Contact[]>([]);
  const [loading, setLoading] = useState(false);
  const [semanticMode, setSemanticMode] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const overlayRef = useRef<HTMLDivElement>(null);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Open with Ctrl+K
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

  // Debounced search
  useEffect(() => {
    if (!query.trim()) { setResults([]); return; }
    clearTimeout(timeoutRef.current);
    timeoutRef.current = setTimeout(async () => {
      setLoading(true);
      try {
        const { contacts } = await getContacts({ q: query, limit: 8 });
        setResults(contacts);
      } catch {
        setResults([]);
      } finally {
        setLoading(false);
      }
    }, 300);
    return () => clearTimeout(timeoutRef.current);
  }, [query, semanticMode]);

  const handleSelect = (contact: Contact) => {
    onSelectContact?.(contact);
    setIsOpen(false);
    setQuery('');
    setResults([]);
  };

  const isSemanticQuery = query.trim().split(/\s+/).length > 2;

  if (!isOpen) {
    return (
      <button
        id="global-search-trigger"
        onClick={() => setIsOpen(true)}
        className="search-trigger"
        aria-label="Open search"
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>
        <span>Search…</span>
        <kbd>⌘K</kbd>
      </button>
    );
  }

  return (
    <div className="search-overlay" onClick={(e) => { if (e.target === e.currentTarget) setIsOpen(false); }}>
      <div className="search-modal" ref={overlayRef}>
        <div className="search-input-row">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#6b7280" strokeWidth="2"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>
          <input
            ref={inputRef}
            id="global-search-input"
            type="text"
            placeholder={semanticMode ? 'Describe who you\'re looking for…' : 'Search contacts…'}
            value={query}
            onChange={e => setQuery(e.target.value)}
            className="search-input"
          />
          {loading && <span className="search-spinner" />}
          <button
            className={`semantic-toggle ${semanticMode ? 'active' : ''}`}
            onClick={() => setSemanticMode(v => !v)}
            title="Toggle AI Semantic Search"
            id="semantic-toggle-btn"
          >
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M12 2a8 8 0 0 1 8 8c0 5.25-8 12-8 12S4 15.25 4 10a8 8 0 0 1 8-8z"/><circle cx="12" cy="10" r="3"/></svg>
            AI
          </button>
          <button onClick={() => setIsOpen(false)} className="search-escape">ESC</button>
        </div>

        {semanticMode && (
          <div className="semantic-banner">
            <span>🔮</span>
            <span>AI Semantic mode — describe what you're looking for naturally</span>
          </div>
        )}

        {results.length > 0 && (
          <ul className="search-results">
            {results.map(contact => (
              <li key={contact.id}>
                <button className="search-result-item" onClick={() => handleSelect(contact)}>
                  <div className="search-avatar">
                    {contact.first_name[0]}{contact.last_name?.[0] ?? ''}
                  </div>
                  <div className="search-result-info">
                    <span className="search-result-name">{contact.first_name} {contact.last_name}</span>
                    {contact.email && <span className="search-result-sub">{contact.email}</span>}
                    {contact.company && <span className="search-result-company">{contact.company.name}</span>}
                  </div>
                  {isSemanticQuery && semanticMode && (
                    <span className="ai-badge">AI</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}

        {query && !loading && results.length === 0 && (
          <div className="search-empty">No results for "{query}"</div>
        )}
      </div>

      <style>{`
        .search-trigger {
          display: flex; align-items: center; gap: 6px; padding: 6px 12px;
          background: var(--muted, #f3f4f6); border: 1px solid var(--border, #e5e7eb);
          border-radius: 8px; cursor: pointer; color: #6b7280; font-size: 13px;
          transition: all 0.15s;
        }
        .search-trigger:hover { border-color: #6366f1; color: #6366f1; }
        .search-trigger kbd { margin-left: 8px; padding: 1px 5px; background: var(--background, #fff); border: 1px solid var(--border, #e5e7eb); border-radius: 4px; font-size: 11px; }

        .search-overlay {
          position: fixed; inset: 0; z-index: 2000;
          background: rgba(0,0,0,0.4); backdrop-filter: blur(2px);
          display: flex; align-items: flex-start; justify-content: center; padding-top: 80px;
          animation: fadeIn 0.15s ease;
        }
        @keyframes fadeIn { from { opacity: 0; } }

        .search-modal {
          width: 560px; background: var(--background, #fff); border-radius: 16px;
          box-shadow: 0 32px 80px rgba(0,0,0,0.25); overflow: hidden;
          animation: slideDown 0.15s ease;
        }
        @keyframes slideDown { from { transform: translateY(-8px); opacity: 0; } }

        .search-input-row { display: flex; align-items: center; gap: 10px; padding: 14px 16px; border-bottom: 1px solid var(--border, #e5e7eb); }
        .search-input { flex: 1; border: none; outline: none; font-size: 15px; background: transparent; color: var(--foreground, #111); }
        .search-escape { padding: 2px 6px; background: var(--muted, #f3f4f6); border: 1px solid var(--border, #e5e7eb); border-radius: 4px; font-size: 11px; color: #6b7280; cursor: pointer; }
        .search-spinner { width: 14px; height: 14px; border: 2px solid #e5e7eb; border-top-color: #6366f1; border-radius: 50%; animation: spin 0.6s linear infinite; flex-shrink: 0; }
        @keyframes spin { to { transform: rotate(360deg); } }

        .semantic-toggle {
          display: flex; align-items: center; gap: 4px; padding: 3px 8px;
          border: 1px solid var(--border, #e5e7eb); border-radius: 6px; background: transparent;
          cursor: pointer; font-size: 11px; color: #6b7280; transition: all 0.15s;
        }
        .semantic-toggle.active { background: #eef2ff; border-color: #6366f1; color: #6366f1; }

        .semantic-banner { display: flex; align-items: center; gap: 8px; padding: 8px 16px; background: #eef2ff; font-size: 12px; color: #4f46e5; }

        .search-results { list-style: none; margin: 0; padding: 8px; max-height: 360px; overflow-y: auto; }
        .search-result-item {
          display: flex; align-items: center; gap: 10px; width: 100%; padding: 8px 10px;
          border: none; border-radius: 10px; background: transparent; cursor: pointer; text-align: left;
          transition: background 0.1s;
        }
        .search-result-item:hover { background: var(--muted, #f3f4f6); }
        .search-avatar { width: 32px; height: 32px; border-radius: 10px; background: linear-gradient(135deg, #6366f1, #8b5cf6); color: white; font-size: 11px; font-weight: 700; display: flex; align-items: center; justify-content: center; flex-shrink: 0; }
        .search-result-info { flex: 1; display: flex; flex-direction: column; }
        .search-result-name { font-size: 13px; font-weight: 500; color: var(--foreground, #111); }
        .search-result-sub { font-size: 11px; color: #6b7280; }
        .search-result-company { font-size: 11px; color: #9ca3af; }
        .ai-badge { padding: 2px 6px; background: linear-gradient(135deg, #6366f1, #8b5cf6); color: white; border-radius: 99px; font-size: 9px; font-weight: 700; letter-spacing: 0.05em; }
        .search-empty { padding: 20px; text-align: center; color: #9ca3af; font-size: 13px; }
      `}</style>
    </div>
  );
}
