import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Check, ChevronDown, Loader2, Search, UserRound, Users } from 'lucide-react';
import { getContacts, type Contact } from '../../../lib/api';

const PAGE = 25;

/** contactLabel is the display name, falling back to the email. */
export function contactLabel(c: Contact): string {
  return [c.first_name, c.last_name].filter(Boolean).join(' ') || c.email || 'Unnamed contact';
}

interface Props {
  value: Contact | null;
  onChange: (c: Contact | null) => void;
  busy?: boolean; // the resolved preview is being fetched
}

/** ContactPicker is a searchable picklist of contacts ("view as recipient").
 *  Opening it lists contacts immediately — typing filters server-side. Follows
 *  the FieldPicker combobox idiom: roving focusIndex, listbox/option roles,
 *  Enter/Arrow/Escape/Tab handling, outside-click close. */
export const ContactPicker: React.FC<Props> = ({ value, onChange, busy }) => {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [items, setItems] = useState<Contact[]>([]);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [focusIndex, setFocusIndex] = useState(-1);

  const rootRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Load on open and on every query change — an EMPTY query is a real request
  // here (that's what makes this a picklist rather than a search box).
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setLoading(true);
    setFailed(false);
    const t = setTimeout(() => {
      getContacts({ q: search.trim() || undefined, limit: PAGE })
        .then((r) => { if (!cancelled) setItems(r.contacts ?? []); })
        .catch(() => { if (!cancelled) { setItems([]); setFailed(true); } })
        .finally(() => { if (!cancelled) setLoading(false); });
    }, search.trim() === '' ? 0 : 250); // instant first list, debounced typing
    return () => { cancelled = true; clearTimeout(t); };
  }, [open, search]);

  // Outside click closes.
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) close();
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  // Keep the focused row scrolled into view.
  useEffect(() => {
    if (focusIndex < 0) return;
    listRef.current?.querySelector(`[data-idx="${focusIndex}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [focusIndex]);

  const close = () => { setOpen(false); setSearch(''); setFocusIndex(-1); };

  const openMenu = () => {
    setOpen(true);
    setFocusIndex(-1);
    setTimeout(() => searchRef.current?.focus(), 50);
  };

  const choose = (c: Contact | null) => { onChange(c); close(); };

  // Row 0 is the "sample data" reset when a contact is selected.
  const hasReset = value !== null;
  const rowCount = items.length + (hasReset ? 1 : 0);
  const rowAt = (i: number): Contact | null | undefined => {
    if (hasReset) return i === 0 ? null : items[i - 1];
    return items[i];
  };

  const onKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (!open) {
      if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
        e.preventDefault();
        openMenu();
      }
      return;
    }
    const max = rowCount - 1;
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setFocusIndex((p) => (p < max ? p + 1 : 0));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setFocusIndex((p) => (p > 0 ? p - 1 : max));
        break;
      case 'Enter': {
        e.preventDefault();
        if (focusIndex < 0 || focusIndex > max) break;
        choose(rowAt(focusIndex) ?? null);
        break;
      }
      case 'Escape':
        e.preventDefault();
        e.stopPropagation(); // don't let the modal close too
        close();
        break;
      case 'Tab':
        close();
        break;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, focusIndex, rowCount, items, hasReset]);

  // A changed result set invalidates the highlighted row.
  useEffect(() => { setFocusIndex(-1); }, [search, items]);

  return (
    <div className="relative" ref={rootRef} onKeyDown={onKeyDown}>
      <button
        type="button"
        role="combobox"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-controls="contact-picker-list"
        aria-label="View the preview as a contact"
        onClick={() => (open ? close() : openMenu())}
        className={`flex h-8 w-52 items-center gap-1.5 rounded-lg border px-2.5 text-xs transition-colors ${
          value ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:border-ring hover:text-foreground'
        }`}
      >
        <UserRound className="h-3.5 w-3.5 shrink-0" />
        <span className="flex-1 truncate text-left font-medium">
          {value ? contactLabel(value) : 'View as contact…'}
        </span>
        {busy ? <Loader2 className="h-3 w-3 shrink-0 animate-spin" /> : <ChevronDown className="h-3.5 w-3.5 shrink-0 opacity-60" />}
      </button>

      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 flex max-h-80 w-72 flex-col overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-2xl">
          <div className="border-b border-border p-2">
            <div className="relative">
              <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                ref={searchRef}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search contacts…"
                aria-label="Search contacts"
                className="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-2 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>

          <div ref={listRef} id="contact-picker-list" role="listbox" aria-label="Contacts" className="flex-1 overflow-y-auto py-1">
            {hasReset && (
              <Row
                idx={0}
                focused={focusIndex === 0}
                onSelect={() => choose(null)}
                onHover={() => setFocusIndex(0)}
              >
                <Users className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <span className="flex-1 truncate text-xs">Sample data (no contact)</span>
              </Row>
            )}

            {loading ? (
              <p className="flex items-center justify-center gap-2 px-3 py-4 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading contacts…
              </p>
            ) : failed ? (
              <p className="px-3 py-4 text-center text-xs text-destructive">Couldn’t load contacts.</p>
            ) : items.length === 0 ? (
              <p className="px-3 py-4 text-center text-xs text-muted-foreground">
                {search.trim() ? 'No matching contacts' : 'No contacts yet'}
              </p>
            ) : (
              items.map((c, i) => {
                const idx = hasReset ? i + 1 : i;
                const selected = value?.id === c.id;
                return (
                  <Row
                    key={c.id}
                    idx={idx}
                    focused={focusIndex === idx}
                    selected={selected}
                    onSelect={() => choose(c)}
                    onHover={() => setFocusIndex(idx)}
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-xs font-medium">{contactLabel(c)}</span>
                      {c.email && <span className="block truncate text-[11px] text-muted-foreground">{c.email}</span>}
                    </span>
                    {selected && <Check className="h-3.5 w-3.5 shrink-0 text-primary" />}
                  </Row>
                );
              })
            )}
          </div>

          {!loading && items.length >= PAGE && (
            <p className="border-t border-border px-3 py-1.5 text-[11px] text-muted-foreground">
              Showing the first {PAGE} — type to narrow it down.
            </p>
          )}
        </div>
      )}
    </div>
  );
};

const Row: React.FC<{
  idx: number;
  focused: boolean;
  selected?: boolean;
  onSelect: () => void;
  onHover: () => void;
  children: React.ReactNode;
}> = ({ idx, focused, selected, onSelect, onHover, children }) => (
  <button
    type="button"
    role="option"
    aria-selected={!!selected}
    data-idx={idx}
    onClick={onSelect}
    onMouseEnter={onHover}
    className={`flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors ${
      focused ? 'bg-accent text-accent-foreground' : 'hover:bg-accent hover:text-accent-foreground'
    }`}
  >
    {children}
  </button>
);

export default ContactPicker;
