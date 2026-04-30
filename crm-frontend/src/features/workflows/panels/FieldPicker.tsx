import React, { useState, useRef, useEffect, useMemo } from 'react';
import { useBuilderStore } from '../store';
import type { SchemaField, SchemaEntity } from '../api';

// Icons per entity key
const ENTITY_ICONS: Record<string, string> = {
  contact: '👤',
  deal: '💰',
  trigger: '⚡',
};

// Type badge colors
const TYPE_COLORS: Record<string, string> = {
  string: '#9CA3AF',
  number: '#60A5FA',
  boolean: '#F59E0B',
  array: '#A78BFA',
  select: '#34D399',
  date: '#FB923C',
};

// Type badge labels
const TYPE_LABELS: Record<string, string> = {
  string: 'Abc',
  number: '#',
  boolean: '✓/✗',
  array: '[ ]',
  select: '▾',
  date: '📅',
};

interface FieldPickerProps {
  /** Currently selected field path (e.g. "contact.tags"), or null if nothing selected */
  value: string | null;
  /** Called with the selected field path */
  onChange: (path: string) => void;
  /** Optional filter — only show these entity keys (e.g. ['contact', 'deal']) */
  entities?: string[];
  /** Disable the picker */
  disabled?: boolean;
  /** Placeholder text */
  placeholder?: string;
}

export const FieldPicker: React.FC<FieldPickerProps> = ({
  value,
  onChange,
  entities: entityFilter,
  disabled = false,
  placeholder = 'Select field…',
}) => {
  const { schema, schemaLoading, schemaError } = useBuilderStore();
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // Close on outside click
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false);
        setSearch('');
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  // Focus search on open
  useEffect(() => {
    if (isOpen && searchRef.current) {
      searchRef.current.focus();
    }
  }, [isOpen]);

  // All entities (built-in + custom objects), optionally filtered by entity keys
  const allEntities = useMemo(() => {
    if (!schema) return [];
    const all = [...schema.entities, ...(schema.custom_objects || [])];
    if (entityFilter && entityFilter.length > 0) {
      return all.filter((e) => entityFilter.includes(e.key));
    }
    return all;
  }, [schema, entityFilter]);

  // Find the currently selected field info for display
  const selectedField = useMemo(() => {
    if (!value || !allEntities.length) return null;
    for (const entity of allEntities) {
      const field = entity.fields.find((f) => f.path === value);
      if (field) {
        return { entity, field };
      }
    }
    return null;
  }, [value, allEntities]);

  // Filter entities & fields by search term
  const filteredEntities = useMemo(() => {
    if (!allEntities.length) return [];
    const q = search.toLowerCase().trim();
    if (!q) return allEntities;

    return allEntities
      .map((entity) => {
        const matchedFields = entity.fields.filter(
          (f) =>
            f.label.toLowerCase().includes(q) ||
            f.path.toLowerCase().includes(q) ||
            entity.label.toLowerCase().includes(q)
        );
        if (matchedFields.length === 0) return null;
        return { ...entity, fields: matchedFields };
      })
      .filter(Boolean) as SchemaEntity[];
  }, [allEntities, search]);

  const handleSelect = (_entity: SchemaEntity, field: SchemaField) => {
    onChange(field.path);
    setIsOpen(false);
    setSearch('');
  };

  // --- Loading state ---
  if (schemaLoading) {
    return <div className="w-full h-[34px] bg-gray-800 border border-gray-700 rounded-lg animate-pulse" />;
  }

  // --- Error state (disabled, shown in parent banner) ---
  if (schemaError) {
    return (
      <button
        disabled
        className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-gray-500 text-left cursor-not-allowed opacity-50"
      >
        Schema unavailable
      </button>
    );
  }

  return (
    <div ref={containerRef} className="relative">
      {/* Trigger button */}
      <button
        type="button"
        onClick={() => {
          if (!disabled) setIsOpen(!isOpen);
        }}
        disabled={disabled}
        className={`
          w-full bg-gray-800 border rounded-lg px-3 py-1.5 text-sm text-left
          flex items-center gap-2 transition-all
          ${isOpen ? 'border-purple-500 ring-1 ring-purple-500/30' : 'border-gray-700 hover:border-gray-600'}
          ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}
        `}
      >
        {selectedField ? (
          <>
            <span className="text-base leading-none">
              {ENTITY_ICONS[selectedField.entity.key] || selectedField.entity.icon || '📦'}
            </span>
            <span className="text-gray-400 text-xs">{selectedField.entity.label}</span>
            <span className="text-gray-600">›</span>
            <span className="text-white truncate flex-1">{selectedField.field.label}</span>
            <span
              className="px-1.5 py-0.5 rounded text-[10px] font-medium uppercase tracking-wider"
              style={{
                backgroundColor: `${TYPE_COLORS[selectedField.field.type] || TYPE_COLORS.string}20`,
                color: TYPE_COLORS[selectedField.field.type] || TYPE_COLORS.string,
              }}
            >
              {selectedField.field.type}
            </span>
          </>
        ) : (
          <span className="text-gray-500 flex-1">{placeholder}</span>
        )}
        <svg
          className={`w-3.5 h-3.5 text-gray-500 transition-transform ${isOpen ? 'rotate-180' : ''}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div className="absolute z-50 top-full left-0 right-0 mt-1 bg-gray-850 border border-gray-700 rounded-xl shadow-2xl shadow-black/50 overflow-hidden"
          style={{ backgroundColor: '#1a1d27' }}
        >
          {/* Search */}
          <div className="p-2 border-b border-gray-700/50">
            <div className="relative">
              <svg
                className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-500"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2}
              >
                <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              <input
                ref={searchRef}
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search fields..."
                className="w-full bg-gray-800/80 border border-gray-700/50 rounded-lg pl-8 pr-3 py-1.5 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-purple-500/50"
              />
            </div>
          </div>

          {/* Field list grouped by entity */}
          <div className="max-h-64 overflow-y-auto overscroll-contain">
            {filteredEntities.length === 0 ? (
              <div className="px-3 py-4 text-center text-xs text-gray-500">
                {search ? 'No matching fields' : 'No fields available'}
              </div>
            ) : (
              filteredEntities.map((entity) => (
                <div key={entity.key}>
                  {/* Entity group header */}
                  <div className="px-3 py-1.5 text-[11px] font-semibold text-gray-500 uppercase tracking-wider bg-gray-800/30 sticky top-0 flex items-center gap-1.5">
                    <span>{ENTITY_ICONS[entity.key] || entity.icon || '📦'}</span>
                    <span>{entity.label}</span>
                  </div>
                  {/* Fields */}
                  {entity.fields.map((field) => {
                    const isSelected = field.path === value;
                    return (
                      <button
                        key={field.path}
                        type="button"
                        onClick={() => handleSelect(entity, field)}
                        className={`
                          w-full px-3 py-2 text-left flex items-center gap-2 text-sm transition-colors
                          ${isSelected
                            ? 'bg-purple-500/15 text-purple-300'
                            : 'text-gray-300 hover:bg-gray-800/60 hover:text-white'
                          }
                        `}
                      >
                        <span className="flex-1 truncate">{field.label}</span>
                        {field.picker_type && (
                          <span className="text-[10px] px-1.5 py-0.5 rounded bg-indigo-500/15 text-indigo-400 font-medium">
                            {field.picker_type}
                          </span>
                        )}
                        <span
                          className="px-1.5 py-0.5 rounded text-[10px] font-medium uppercase tracking-wider flex-shrink-0"
                          style={{
                            backgroundColor: `${TYPE_COLORS[field.type] || TYPE_COLORS.string}15`,
                            color: TYPE_COLORS[field.type] || TYPE_COLORS.string,
                          }}
                        >
                          {TYPE_LABELS[field.type] || field.type}
                        </span>
                        {isSelected && (
                          <svg className="w-3.5 h-3.5 text-purple-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
                            <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                          </svg>
                        )}
                      </button>
                    );
                  })}
                </div>
              ))
            )}
          </div>

          {/* Footer hint */}
          <div className="px-3 py-1.5 border-t border-gray-700/50 text-[10px] text-gray-600 flex items-center gap-1">
            <span>💡</span>
            <span>Type to search · Click to select</span>
          </div>
        </div>
      )}
    </div>
  );
};
