import React, { useState, useRef, useEffect, useMemo } from 'react';
import { Check } from 'lucide-react';
import type { SchemaTag } from '../../../api';

// ============================================================
// TagMultiSelect — colored pill tags with popover picker
// Emits array of tag IDs (UUIDs), displays tag names.
// ============================================================

export interface TagMultiSelectProps {
  tags: SchemaTag[];
  value: unknown;
  onChange: (v: string[]) => void;
}

export const TagMultiSelect: React.FC<TagMultiSelectProps> = ({ tags, value, onChange }) => {
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // Normalize value to string array of IDs
  const selectedIds: string[] = Array.isArray(value)
    ? value.map(String)
    : typeof value === 'string' && value
      ? value.split(',').map((s) => s.trim()).filter(Boolean)
      : [];

  // Close on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false);
        setSearch('');
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  // Focus search on open
  useEffect(() => {
    if (isOpen && searchRef.current) searchRef.current.focus();
  }, [isOpen]);

  const filteredTags = useMemo(() => {
    const q = search.toLowerCase().trim();
    if (!q) return tags;
    return tags.filter((t) => t.name.toLowerCase().includes(q));
  }, [tags, search]);

  const toggle = (tagId: string) => {
    if (selectedIds.includes(tagId)) {
      onChange(selectedIds.filter((s) => s !== tagId));
    } else {
      onChange([...selectedIds, tagId]);
    }
  };

  const remove = (tagId: string) => {
    onChange(selectedIds.filter((s) => s !== tagId));
  };

  // Resolve tag by ID for display
  const tagById = useMemo(() => {
    const m = new Map<string, SchemaTag>();
    tags.forEach((t) => m.set(t.id, t));
    return m;
  }, [tags]);

  return (
    <div ref={containerRef} className="relative flex-1">
      {/* Trigger area */}
      <div
        onClick={() => setIsOpen(!isOpen)}
        className={`min-h-[34px] bg-background border rounded-lg px-2 py-1 flex flex-wrap gap-1 items-center cursor-pointer transition-colors ${
          isOpen ? 'border-ring ring-1 ring-ring/40' : 'border-border hover:border-muted-foreground/40'
        }`}
      >
        {selectedIds.length === 0 && (
          <span className="text-muted-foreground text-sm px-1">Select tags…</span>
        )}
        {selectedIds.map((tagId) => {
          const tag = tagById.get(tagId);
          const color = tag?.color || '#6B7280';
          const name = tag?.name || tagId;
          return (
            <span
              key={tagId}
              className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium"
              style={{
                backgroundColor: `${color}20`,
                color: color,
                border: `1px solid ${color}40`,
              }}
            >
              <span
                className="w-1.5 h-1.5 rounded-full flex-shrink-0"
                style={{ backgroundColor: color }}
              />
              {name}
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  remove(tagId);
                }}
                className="ml-0.5 hover:opacity-70 transition-opacity"
              >
                ×
              </button>
            </span>
          );
        })}
      </div>

      {/* Dropdown */}
      {isOpen && (
        <div className="absolute z-50 top-full left-0 right-0 mt-1 bg-popover text-popover-foreground border border-border rounded-xl shadow-2xl shadow-black/50 overflow-hidden">
          {/* Search */}
          <div className="p-2 border-b border-border/60">
            <input
              ref={searchRef}
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search tags…"
              className="w-full bg-background border border-border/60 rounded-lg px-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {filteredTags.length === 0 ? (
              <div className="px-3 py-3 text-center text-xs text-muted-foreground">
                No matching tags
              </div>
            ) : (
              filteredTags.map((tag) => {
                const isSelected = selectedIds.includes(tag.id);
                return (
                  <button
                    key={tag.id}
                    type="button"
                    onClick={() => toggle(tag.id)}
                    className={`w-full px-3 py-2 text-left flex items-center gap-2.5 text-sm transition-colors ${
                      isSelected
                        ? 'bg-primary/10 text-primary'
                        : 'text-foreground hover:bg-accent hover:text-accent-foreground'
                    }`}
                  >
                    <span
                      className="w-2.5 h-2.5 rounded-full flex-shrink-0"
                      style={{ backgroundColor: tag.color || '#6B7280' }}
                    />
                    <span className="flex-1 truncate">{tag.name}</span>
                    {isSelected && (
                      <Check className="w-3.5 h-3.5 text-primary flex-shrink-0" />
                    )}
                  </button>
                );
              })
            )}
          </div>

          {/* Footer */}
          <div className="px-3 py-1.5 border-t border-border/60 text-[10px] text-muted-foreground/70">
            {selectedIds.length} selected · Click to toggle
          </div>
        </div>
      )}
    </div>
  );
};
