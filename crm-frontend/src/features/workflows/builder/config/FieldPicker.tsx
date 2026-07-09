import React, { useState, useRef, useEffect, useMemo, useCallback } from 'react';
import { ChevronDown, ChevronRight, ArrowLeft, Search, Check } from 'lucide-react';
import { useBuilderStore } from '../../store';
import type { SchemaField, SchemaEntity } from '../../api';

/** Metadata about the selected field — passed to onChange so consumers can react to type changes. */
export interface FieldMeta {
  label: string;
  type: SchemaField['type'];
  picker_type?: SchemaField['picker_type'];
  options?: string[];
}

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

// Virtualization threshold — if total items > this, we limit rendering
const VIRTUAL_LIMIT = 50;

interface FieldPickerProps {
  /** Currently selected field path (e.g. "contact.tags"), or null if nothing selected */
  value: string | null;
  /** Called with the selected field path and its metadata */
  onChange: (path: string, fieldMeta: FieldMeta) => void;
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
  const [activeCategory, setActiveCategory] = useState<string | null>(null);
  const [focusIndex, setFocusIndex] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Close on outside click
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false);
        setSearch('');
        setActiveCategory(null);
        setFocusIndex(-1);
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

  // Scroll focused item into view
  useEffect(() => {
    if (focusIndex >= 0 && listRef.current) {
      const items = listRef.current.querySelectorAll('[data-item]');
      const el = items[focusIndex] as HTMLElement | undefined;
      el?.scrollIntoView?.({ block: 'nearest' });
    }
  }, [focusIndex]);

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
      if (field) return { entity, field };
    }
    return null;
  }, [value, allEntities]);

  // The active drilled-in category entity
  const activeCategoryEntity = useMemo(
    () => (activeCategory ? allEntities.find((e) => e.key === activeCategory) || null : null),
    [activeCategory, allEntities]
  );

  // Is the user actively searching?
  const isSearching = search.trim().length > 0;

  // Search: filter fields across all entities (flattened results)
  const searchResults = useMemo(() => {
    const q = search.toLowerCase().trim();
    if (!q) return null; // null = not searching
    const results: { entity: SchemaEntity; field: SchemaField }[] = [];
    for (const entity of allEntities) {
      for (const field of entity.fields) {
        if (
          field.label.toLowerCase().includes(q) ||
          field.path.toLowerCase().includes(q) ||
          entity.label.toLowerCase().includes(q)
        ) {
          results.push({ entity, field });
        }
      }
    }
    return results;
  }, [allEntities, search]);

  // Build the current list of interactive items for keyboard navigation
  const currentItems = useMemo(() => {
    if (isSearching && searchResults !== null) {
      // Search results: each is a field
      return searchResults.map(({ entity, field }) => ({
        type: 'field' as const,
        entity,
        field,
      }));
    }
    if (activeCategory && activeCategoryEntity) {
      // Drilled-in: back button + fields
      return [
        { type: 'back' as const, entity: activeCategoryEntity, field: null },
        ...activeCategoryEntity.fields.map((field) => ({
          type: 'field' as const,
          entity: activeCategoryEntity,
          field,
        })),
      ];
    }
    // Category list
    return allEntities.map((entity) => ({
      type: 'category' as const,
      entity,
      field: null,
    }));
  }, [isSearching, searchResults, activeCategory, activeCategoryEntity, allEntities]);

  // Virtualization: limit rendered items if > VIRTUAL_LIMIT
  const totalItems = currentItems.length;
  const isVirtualized = totalItems > VIRTUAL_LIMIT;
  const visibleItems = isVirtualized ? currentItems.slice(0, VIRTUAL_LIMIT) : currentItems;
  const hiddenCount = isVirtualized ? totalItems - VIRTUAL_LIMIT : 0;

  const handleSelect = (_entity: SchemaEntity, field: SchemaField) => {
    onChange(field.path, {
      label: field.label,
      type: field.type,
      picker_type: field.picker_type,
      options: field.options,
    });
    setIsOpen(false);
    setSearch('');
    setActiveCategory(null);
    setFocusIndex(-1);
  };

  const handleOpen = () => {
    if (disabled) return;
    if (isOpen) {
      setIsOpen(false);
      setSearch('');
      setActiveCategory(null);
      setFocusIndex(-1);
    } else {
      setIsOpen(true);
      setFocusIndex(-1);
      // If a field is already selected, auto-drill into its category
      if (selectedField) {
        setActiveCategory(selectedField.entity.key);
      }
    }
  };

  const handleBack = () => {
    setActiveCategory(null);
    setSearch('');
    setFocusIndex(-1);
    // Re-focus search after going back
    setTimeout(() => searchRef.current?.focus(), 50);
  };

  // ── Keyboard handler ─────────────────────────────────────────────
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (!isOpen) {
        // Open on Enter/Space/ArrowDown when trigger is focused
        if (e.key === 'Enter' || e.key === ' ' || e.key === 'ArrowDown') {
          e.preventDefault();
          handleOpen();
        }
        return;
      }

      const maxIndex = visibleItems.length - 1;

      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setFocusIndex((prev) => (prev < maxIndex ? prev + 1 : 0));
          break;

        case 'ArrowUp':
          e.preventDefault();
          setFocusIndex((prev) => (prev > 0 ? prev - 1 : maxIndex));
          break;

        case 'Enter': {
          e.preventDefault();
          if (focusIndex < 0 || focusIndex > maxIndex) break;
          const item = visibleItems[focusIndex];
          if (item.type === 'category') {
            setActiveCategory(item.entity.key);
            setFocusIndex(-1);
          } else if (item.type === 'back') {
            handleBack();
          } else if (item.type === 'field' && item.field) {
            handleSelect(item.entity, item.field);
          }
          break;
        }

        case 'ArrowRight': {
          // Drill into category (like clicking →)
          if (focusIndex >= 0 && focusIndex <= maxIndex) {
            const item = visibleItems[focusIndex];
            if (item.type === 'category') {
              e.preventDefault();
              setActiveCategory(item.entity.key);
              setFocusIndex(-1);
            }
          }
          break;
        }

        case 'ArrowLeft':
        case 'Backspace': {
          // Go back from drilled-in category (when search is empty)
          if (activeCategory && !isSearching && (e.key === 'ArrowLeft' || (e.key === 'Backspace' && search === ''))) {
            e.preventDefault();
            handleBack();
          }
          break;
        }

        case 'Escape':
          e.preventDefault();
          if (isSearching) {
            setSearch('');
            setFocusIndex(-1);
          } else if (activeCategory) {
            handleBack();
          } else {
            setIsOpen(false);
            setSearch('');
            setActiveCategory(null);
            setFocusIndex(-1);
          }
          break;

        case 'Tab':
          // Close on tab-out
          setIsOpen(false);
          setSearch('');
          setActiveCategory(null);
          setFocusIndex(-1);
          break;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [isOpen, focusIndex, visibleItems, activeCategory, isSearching, search]
  );

  // Reset focus index when list changes
  useEffect(() => {
    setFocusIndex(-1);
  }, [activeCategory, search]);

  // --- Loading state ---
  if (schemaLoading) {
    return <div className="w-full h-[34px] bg-muted border border-border rounded-lg animate-pulse" />;
  }

  // --- Error state (disabled, shown in parent banner) ---
  if (schemaError) {
    return (
      <button
        disabled
        className="w-full bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-muted-foreground text-left cursor-not-allowed opacity-50"
      >
        Schema unavailable
      </button>
    );
  }

  return (
    <div ref={containerRef} className="relative" onKeyDown={handleKeyDown}>
      {/* Trigger button */}
      <button
        type="button"
        onClick={handleOpen}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={isOpen}
        className={`
          w-full bg-background border rounded-lg px-3 py-1.5 text-sm text-left
          flex items-center gap-2 transition-all
          ${isOpen ? 'border-ring ring-1 ring-ring/40' : 'border-border hover:border-muted-foreground/40'}
          ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}
        `}
      >
        {selectedField ? (
          <>
            <span className="text-base leading-none">
              {ENTITY_ICONS[selectedField.entity.key] || selectedField.entity.icon || '📦'}
            </span>
            <span className="text-muted-foreground text-xs">{selectedField.entity.label}</span>
            <span className="text-muted-foreground/70">›</span>
            <span className="text-foreground truncate flex-1">{selectedField.field.label}</span>
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
          <span className="text-muted-foreground flex-1">{placeholder}</span>
        )}
        <ChevronDown
          className={`h-3.5 w-3.5 text-muted-foreground transition-transform ${isOpen ? 'rotate-180' : ''}`}
        />
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div
          className="absolute z-50 top-full left-0 right-0 mt-1 border border-border rounded-xl shadow-2xl shadow-black/50 overflow-hidden bg-popover text-popover-foreground"
          role="listbox"
          aria-label="Field picker"
        >
          {/* Search bar */}
          <div className="p-2 border-b border-border/60">
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
              <input
                ref={searchRef}
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={activeCategory && !isSearching
                  ? `Search ${activeCategoryEntity?.label || ''} fields...`
                  : 'Search all fields...'}
                className="w-full bg-background border border-border/60 rounded-lg pl-8 pr-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
                role="searchbox"
                aria-label="Search fields"
              />
            </div>
          </div>

          {/* Content area */}
          <div ref={listRef} className="max-h-64 overflow-y-auto overscroll-contain">

            {/* ── State 1: Search results (flat, across all categories) ── */}
            {isSearching && searchResults !== null ? (
              searchResults.length === 0 ? (
                <div className="px-3 py-4 text-center text-xs text-muted-foreground">
                  No matching fields
                </div>
              ) : (
                <>
                  {visibleItems.map((item, idx) => item.field && (
                    <FieldRow
                      key={item.field.path}
                      entity={item.entity}
                      field={item.field}
                      isSelected={item.field.path === value}
                      isFocused={idx === focusIndex}
                      showCategory
                      onSelect={() => handleSelect(item.entity, item.field!)}
                    />
                  ))}
                  {hiddenCount > 0 && (
                    <div className="px-3 py-2 text-center text-xs text-muted-foreground border-t border-border/60">
                      +{hiddenCount} more — refine your search
                    </div>
                  )}
                </>
              )

            /* ── State 2: Category drilled in → show fields ── */
            ) : activeCategory && activeCategoryEntity ? (
              <>
                {/* Back button / breadcrumb */}
                <button
                  type="button"
                  onClick={handleBack}
                  data-item
                  className={`
                    w-full px-3 py-2 text-left flex items-center gap-2 text-xs transition-colors border-b border-border/60
                    ${focusIndex === 0 ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'}
                  `}
                  role="option"
                  aria-selected={false}
                >
                  <ArrowLeft className="h-3 w-3" />
                  <span className="text-base leading-none">
                    {ENTITY_ICONS[activeCategoryEntity.key] || activeCategoryEntity.icon || '📦'}
                  </span>
                  <span className="font-medium">{activeCategoryEntity.label}</span>
                  <span className="text-muted-foreground/70 ml-auto text-[10px]">{activeCategoryEntity.fields.length} fields</span>
                </button>

                {/* Fields in this category */}
                {activeCategoryEntity.fields.map((field, idx) => (
                  <FieldRow
                    key={field.path}
                    entity={activeCategoryEntity}
                    field={field}
                    isSelected={field.path === value}
                    isFocused={idx + 1 === focusIndex} /* +1 because back button is index 0 */
                    onSelect={() => handleSelect(activeCategoryEntity, field)}
                  />
                ))}
              </>

            /* ── State 3: Category list (initial) ── */
            ) : (
              allEntities.length === 0 ? (
                <div className="px-3 py-4 text-center text-xs text-muted-foreground">
                  No fields available
                </div>
              ) : (
                allEntities.map((entity, idx) => (
                  <button
                    key={entity.key}
                    type="button"
                    onClick={() => setActiveCategory(entity.key)}
                    data-item
                    role="option"
                    aria-selected={false}
                    className={`
                      w-full px-3 py-2.5 text-left flex items-center gap-3 text-sm transition-colors
                      ${idx === focusIndex ? 'bg-accent text-accent-foreground' : 'text-foreground hover:bg-accent hover:text-accent-foreground'}
                    `}
                  >
                    <span className="text-lg leading-none">
                      {ENTITY_ICONS[entity.key] || entity.icon || '📦'}
                    </span>
                    <span className="font-medium flex-1">{entity.label}</span>
                    <span className="text-[11px] text-muted-foreground/70">{entity.fields.length} fields</span>
                    <ChevronRight className="h-3.5 w-3.5 text-muted-foreground/70" />
                  </button>
                ))
              )
            )}
          </div>

          {/* Footer hint */}
          <div className="px-3 py-1.5 border-t border-border/60 text-[10px] text-muted-foreground/70 flex items-center gap-1">
            <span>💡</span>
            <span>
              {isSearching
                ? 'Showing results across all categories'
                : activeCategory
                  ? '← Back to categories · Type to search'
                  : 'Select a category · Type to search all'}
            </span>
            <span className="ml-auto text-muted-foreground/70">↑↓ navigate · Enter select · Esc close</span>
          </div>
        </div>
      )}
    </div>
  );
};

// --- Field row sub-component ---

interface FieldRowProps {
  entity: SchemaEntity;
  field: SchemaField;
  isSelected: boolean;
  isFocused?: boolean;
  showCategory?: boolean;
  onSelect: () => void;
}

const FieldRow: React.FC<FieldRowProps> = ({ entity, field, isSelected, isFocused = false, showCategory, onSelect }) => (
  <button
    type="button"
    onClick={onSelect}
    data-item
    role="option"
    aria-selected={isSelected}
    className={`
      w-full px-3 py-2 text-left flex items-center gap-2 text-sm transition-colors
      ${isSelected
        ? 'bg-primary/10 text-primary'
        : isFocused
          ? 'bg-accent text-accent-foreground'
          : 'text-foreground hover:bg-accent hover:text-accent-foreground'
      }
    `}
  >
    {/* Category prefix (shown in search results) */}
    {showCategory && (
      <>
        <span className="text-xs leading-none">
          {ENTITY_ICONS[entity.key] || entity.icon || '📦'}
        </span>
        <span className="text-muted-foreground text-xs">{entity.label}</span>
        <span className="text-muted-foreground/70 text-xs">›</span>
      </>
    )}
    <span className="flex-1 truncate">{field.label}</span>
    {field.picker_type && (
      <span className="text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">
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
      <Check className="h-3.5 w-3.5 text-primary flex-shrink-0" />
    )}
  </button>
);
