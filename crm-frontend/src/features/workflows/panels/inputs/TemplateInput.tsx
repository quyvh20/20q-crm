import React, { useState, useRef, useEffect, useMemo, useCallback } from 'react';
import { useBuilderStore } from '../../store';

/* ------------------------------------------------------------------ */
/*  TemplateInput — text input with {x} variable-insert button        */
/* ------------------------------------------------------------------ */

interface TemplateInputProps {
  /** Field label displayed above the input */
  label: string;
  /** Current text value */
  value: string;
  /** Called with the new string on every change or variable insertion */
  onChange: (v: string) => void;
  /** Placeholder text */
  placeholder?: string;
  /** HTML input type — defaults to 'text' */
  type?: string;
  /** If true, renders a <textarea> instead of <input> */
  multiline?: boolean;
  /** Textarea rows (only used when multiline) */
  rows?: number;
  /** If true, uses monospace font (for code/JSON) */
  mono?: boolean;
  /**
   * Optional filter for the variable picker.
   * When set, only fields whose path includes this substring are shown.
   * E.g. 'email' → only shows contact.email, not contact.first_name.
   */
  fieldFilter?: string;
}

export const TemplateInput: React.FC<TemplateInputProps> = ({
  label,
  value,
  onChange,
  placeholder,
  type = 'text',
  multiline = false,
  rows = 4,
  mono = false,
  fieldFilter,
}) => {
  const [showPicker, setShowPicker] = useState(false);
  const inputRef = useRef<HTMLInputElement | HTMLTextAreaElement>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  // Close picker on outside click
  useEffect(() => {
    if (!showPicker) return;
    const handler = (e: MouseEvent) => {
      if (
        pickerRef.current &&
        !pickerRef.current.contains(e.target as Node) &&
        buttonRef.current &&
        !buttonRef.current.contains(e.target as Node)
      ) {
        setShowPicker(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [showPicker]);

  // Insert variable at cursor position
  const insertVariable = useCallback(
    (path: string) => {
      const el = inputRef.current;
      const template = `{{${path}}}`;
      if (el) {
        const start = el.selectionStart ?? el.value.length;
        const end = el.selectionEnd ?? start;
        const before = el.value.slice(0, start);
        const after = el.value.slice(end);
        const newValue = before + template + after;
        onChange(newValue);
        // Restore cursor after React re-render
        requestAnimationFrame(() => {
          el.focus();
          const cursor = start + template.length;
          el.setSelectionRange(cursor, cursor);
        });
      } else {
        // Fallback — append
        onChange((value || '') + template);
      }
      setShowPicker(false);
    },
    [onChange, value],
  );

  const inputClass = [
    'w-full bg-gray-800 border border-gray-700 rounded-lg pl-3 pr-9 py-2 text-sm text-white',
    'focus:border-emerald-500 focus:outline-none',
    multiline ? 'resize-none' : '',
    mono ? 'font-mono' : '',
  ].join(' ');

  return (
    <div className="relative">
      <label className="block text-sm text-gray-400 mb-1">{label}</label>

      <div className="relative">
        {multiline ? (
          <textarea
            ref={inputRef as React.RefObject<HTMLTextAreaElement>}
            value={value || ''}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            rows={rows}
            className={inputClass}
          />
        ) : (
          <input
            ref={inputRef as React.RefObject<HTMLInputElement>}
            type={type}
            value={value || ''}
            onChange={(e) => onChange(e.target.value)}
            placeholder={placeholder}
            className={inputClass}
          />
        )}

        {/* {x} insert button */}
        <button
          ref={buttonRef}
          type="button"
          onClick={() => setShowPicker((p) => !p)}
          title="Insert template variable"
          className={[
            'absolute right-2 top-2 w-6 h-6 rounded flex items-center justify-center text-xs',
            'transition-all duration-150',
            showPicker
              ? 'bg-emerald-500/30 text-emerald-300 ring-1 ring-emerald-500/50'
              : 'bg-gray-700/60 text-gray-400 hover:bg-emerald-500/20 hover:text-emerald-300',
          ].join(' ')}
        >
          {'{x}'}
        </button>

        {/* Variable picker dropdown */}
        {showPicker && (
          <VariablePicker
            ref={pickerRef}
            onSelect={insertVariable}
            onClose={() => setShowPicker(false)}
            fieldFilter={fieldFilter}
          />
        )}
      </div>
    </div>
  );
};

/* ------------------------------------------------------------------ */
/*  VariablePicker — floating dropdown with search + grouped fields   */
/* ------------------------------------------------------------------ */

interface VariablePickerProps {
  onSelect: (path: string) => void;
  onClose: () => void;
  /** When set, only fields whose path includes this substring are shown */
  fieldFilter?: string;
}

const VariablePicker = React.forwardRef<HTMLDivElement, VariablePickerProps>(
  ({ onSelect, onClose, fieldFilter }, ref) => {
    const schema = useBuilderStore((s) => s.schema);
    const schemaLoading = useBuilderStore((s) => s.schemaLoading);
    const [search, setSearch] = useState('');
    const searchRef = useRef<HTMLInputElement>(null);
    const [focusedIdx, setFocusedIdx] = useState(0);

    // Auto-focus search on open
    useEffect(() => {
      searchRef.current?.focus();
    }, []);

    // Build grouped variables from schema, applying fieldFilter if set
    const groups = useMemo(() => {
      if (!schema) return [];
      const allEntities = [...schema.entities, ...(schema.custom_objects || [])];
      return allEntities
        .map((entity) => ({
          key: entity.key,
          label: entity.label,
          icon: entity.icon,
          fields: fieldFilter
            ? entity.fields.filter((f) => f.path.toLowerCase().includes(fieldFilter.toLowerCase()))
            : entity.fields,
        }))
        .filter((g) => g.fields.length > 0);
    }, [schema, fieldFilter]);

    // Filter by search
    const filteredGroups = useMemo(() => {
      if (!search.trim()) return groups;
      const q = search.toLowerCase();
      return groups
        .map((g) => ({
          ...g,
          fields: g.fields.filter(
            (f) =>
              f.label.toLowerCase().includes(q) ||
              f.path.toLowerCase().includes(q),
          ),
        }))
        .filter((g) => g.fields.length > 0);
    }, [groups, search]);

    // Flat list for keyboard navigation
    const flatFields = useMemo(
      () => filteredGroups.flatMap((g) => g.fields),
      [filteredGroups],
    );

    // Keyboard nav
    const handleKeyDown = (e: React.KeyboardEvent) => {
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setFocusedIdx((i) => Math.min(i + 1, flatFields.length - 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setFocusedIdx((i) => Math.max(i - 1, 0));
      } else if (e.key === 'Enter') {
        e.preventDefault();
        if (flatFields[focusedIdx]) {
          onSelect(flatFields[focusedIdx].path);
        }
      } else if (e.key === 'Escape') {
        onClose();
      }
    };

    // Reset focus when filter changes
    useEffect(() => {
      setFocusedIdx(0);
    }, [search]);

    return (
      <div
        ref={ref}
        onKeyDown={handleKeyDown}
        className="absolute right-0 top-full mt-1 z-50 w-72 max-h-72 flex flex-col rounded-xl border border-gray-700 bg-gray-900 shadow-2xl shadow-black/40 overflow-hidden animate-in fade-in slide-in-from-top-1 duration-150"
      >
        {/* Search bar */}
        <div className="px-3 py-2 border-b border-gray-800">
          <div className="relative">
            <svg
              className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-500"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth={2}
            >
              <circle cx="11" cy="11" r="8" />
              <path d="M21 21l-4.35-4.35" />
            </svg>
            <input
              ref={searchRef}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search fields…"
              className="w-full bg-gray-800/60 border border-gray-700/60 rounded-lg pl-8 pr-3 py-1.5 text-xs text-white placeholder-gray-500 focus:border-emerald-500/50 focus:outline-none"
            />
          </div>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto overscroll-contain py-1 scrollbar-thin">
          {schemaLoading ? (
            <div className="px-3 py-4 text-center">
              <div className="inline-block w-4 h-4 border-2 border-gray-600 border-t-emerald-400 rounded-full animate-spin" />
              <p className="text-xs text-gray-500 mt-2">Loading fields…</p>
            </div>
          ) : filteredGroups.length === 0 ? (
            <div className="px-3 py-4 text-center">
              <p className="text-xs text-gray-500">
                {search ? 'No matching fields' : 'No fields available'}
              </p>
            </div>
          ) : (
            filteredGroups.map((group) => (
              <div key={group.key}>
                {/* Group header */}
                <div className="px-3 py-1.5 text-[10px] uppercase tracking-wider text-gray-500 font-semibold flex items-center gap-1.5 sticky top-0 bg-gray-900/95 backdrop-blur-sm">
                  <span>{group.icon}</span>
                  <span>{group.label}</span>
                </div>

                {/* Fields */}
                {group.fields.map((field) => {
                  const globalIdx = flatFields.indexOf(field);
                  const isFocused = globalIdx === focusedIdx;
                  return (
                    <button
                      key={field.path}
                      type="button"
                      onClick={() => onSelect(field.path)}
                      onMouseEnter={() => setFocusedIdx(globalIdx)}
                      className={[
                        'w-full px-3 py-1.5 flex items-center gap-2 text-left transition-colors duration-75',
                        isFocused
                          ? 'bg-emerald-500/15 text-white'
                          : 'text-gray-300 hover:bg-gray-800/60',
                      ].join(' ')}
                    >
                      <span className="flex-1 text-xs truncate">{field.label}</span>
                      <code className="text-[10px] text-gray-500 font-mono shrink-0 px-1.5 py-0.5 rounded bg-gray-800/60">
                        {`{{${field.path}}}`}
                      </code>
                    </button>
                  );
                })}
              </div>
            ))
          )}
        </div>

        {/* Footer hint */}
        <div className="px-3 py-1.5 border-t border-gray-800 flex items-center gap-2 text-[10px] text-gray-600">
          <kbd className="px-1 py-0.5 rounded bg-gray-800 text-gray-500 font-mono">↑↓</kbd>
          <span>navigate</span>
          <kbd className="px-1 py-0.5 rounded bg-gray-800 text-gray-500 font-mono">↵</kbd>
          <span>insert</span>
          <kbd className="px-1 py-0.5 rounded bg-gray-800 text-gray-500 font-mono">esc</kbd>
          <span>close</span>
        </div>
      </div>
    );
  },
);

VariablePicker.displayName = 'VariablePicker';
