import React, { useEffect, useRef, useState } from 'react';
import { getContacts, getDeals } from '../../lib/api';
import type { Contact, Deal } from '../../lib/api';

/**
 * A normalized candidate entity surfaced by the EntityPicker. Downstream
 * consumers (e.g. RunNowModal) use `id` to build the `contact_id` / `deal_id`
 * payload and `label`/`sublabel` for display.
 */
export interface EntityCandidate {
  /** Entity id (contact or deal). */
  id: string;
  /** Primary display label — contact full name or deal title. */
  label: string;
  /** Secondary display — contact email/phone or deal stage/value. */
  sublabel?: string;
}

export interface EntityPickerProps {
  /** Which entity type to search for; determines the search endpoint. */
  kind: 'contact' | 'deal';
  /**
   * Called with the candidate the user explicitly clicks. Never invoked
   * automatically — selection always requires an explicit user action.
   */
  onSelect: (entity: EntityCandidate) => void;
}

const DEBOUNCE_MS = 300;
const RESULT_LIMIT = 10;

function contactToCandidate(c: Contact): EntityCandidate {
  const name = `${c.first_name ?? ''} ${c.last_name ?? ''}`.trim();
  return {
    id: c.id,
    label: name || c.email || c.id,
    sublabel: c.email || c.phone || undefined,
  };
}

function dealToCandidate(d: Deal): EntityCandidate {
  const parts: string[] = [];
  if (d.stage?.name) parts.push(d.stage.name);
  if (typeof d.value === 'number') {
    parts.push(d.value.toLocaleString(undefined, { style: 'currency', currency: 'USD' }));
  }
  return {
    id: d.id,
    label: d.title || d.id,
    sublabel: parts.length ? parts.join(' · ') : undefined,
  };
}

/**
 * EntityPicker — free-text, debounced search-and-select control for choosing a
 * single sample contact or deal. The selectable entity type is fixed by `kind`
 * (contact-triggered vs deal-triggered workflows). Results render as a clickable
 * list; selection is always an explicit click and is never pre-populated.
 */
export const EntityPicker: React.FC<EntityPickerProps> = ({ kind, onSelect }) => {
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [results, setResults] = useState<EntityCandidate[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Highlight only — starts unselected so nothing is ever pre-selected.
  const [selectedId, setSelectedId] = useState<string | null>(null);

  // Tracks the latest in-flight request so out-of-order responses are ignored.
  const reqIdRef = useRef(0);

  // Reset state when switching entity kind so a stale contact selection can't
  // carry over to a deal search (or vice versa).
  useEffect(() => {
    setQuery('');
    setDebouncedQuery('');
    setResults([]);
    setError(null);
    setSelectedId(null);
  }, [kind]);

  // Debounce the free-text query (matches the 300ms convention used elsewhere).
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(query.trim()), DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [query]);

  // Run the search whenever the debounced query (or kind) changes.
  useEffect(() => {
    if (!debouncedQuery) {
      setResults([]);
      setLoading(false);
      setError(null);
      return;
    }

    const reqId = ++reqIdRef.current;
    setLoading(true);
    setError(null);

    (async () => {
      try {
        let candidates: EntityCandidate[];
        if (kind === 'contact') {
          const { contacts } = await getContacts({ q: debouncedQuery, limit: RESULT_LIMIT });
          candidates = (contacts || []).map(contactToCandidate);
        } else {
          const { deals } = await getDeals({ q: debouncedQuery, limit: RESULT_LIMIT });
          candidates = (deals || []).map(dealToCandidate);
        }
        // Ignore responses superseded by a newer request.
        if (reqId !== reqIdRef.current) return;
        setResults(candidates);
      } catch (e) {
        if (reqId !== reqIdRef.current) return;
        setError(e instanceof Error ? e.message : `Failed to search ${kind}s`);
        setResults([]);
      } finally {
        if (reqId === reqIdRef.current) setLoading(false);
      }
    })();
  }, [debouncedQuery, kind]);

  const handlePick = (candidate: EntityCandidate) => {
    setSelectedId(candidate.id);
    onSelect(candidate);
  };

  const placeholder = kind === 'contact' ? 'Search contacts by name or email…' : 'Search deals by title…';

  return (
    <div className="space-y-3">
      <input
        type="text"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={placeholder}
        autoComplete="off"
        aria-label={`Search ${kind}s`}
        className="w-full px-3 py-2 rounded-lg bg-gray-900 border border-gray-700 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-indigo-500 transition-colors"
      />

      <div className="min-h-[3rem]">
        {loading ? (
          <div className="flex items-center gap-2 py-3 text-sm text-gray-400">
            <span className="w-4 h-4 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
            Searching…
          </div>
        ) : error ? (
          <p className="py-3 text-sm text-red-400">{error}</p>
        ) : !debouncedQuery ? (
          <p className="py-3 text-sm text-gray-500">
            Type to search for a {kind} to run this workflow against.
          </p>
        ) : results.length === 0 ? (
          <p className="py-3 text-sm text-gray-500">No {kind}s match “{debouncedQuery}”.</p>
        ) : (
          <ul className="space-y-1.5" role="listbox" aria-label={`${kind} results`}>
            {results.map((candidate) => {
              const isSelected = candidate.id === selectedId;
              return (
                <li key={candidate.id}>
                  <button
                    type="button"
                    role="option"
                    aria-selected={isSelected}
                    onClick={() => handlePick(candidate)}
                    className={`w-full text-left px-3 py-2 rounded-lg border transition-colors ${
                      isSelected
                        ? 'border-indigo-500 bg-indigo-500/10'
                        : 'border-gray-700 bg-gray-800/60 hover:border-gray-600 hover:bg-gray-800'
                    }`}
                  >
                    <span className="block text-sm font-medium text-white truncate">
                      {candidate.label}
                    </span>
                    {candidate.sublabel && (
                      <span className="block text-xs text-gray-400 truncate">{candidate.sublabel}</span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
};

export default EntityPicker;
