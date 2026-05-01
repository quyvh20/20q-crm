import React, { useState, useRef, useEffect, useMemo } from 'react';
import { useBuilderStore } from '../store';
import { findFieldInSchema } from '../useSchema';
import type { SchemaTag, SchemaStage, SchemaUser } from '../api';

// ============================================================
// SmartValueInput — renders the right picker based on field metadata
// ============================================================

interface SmartValueInputProps {
  fieldPath: string;
  fieldType: string;
  value: unknown;
  onChange: (value: unknown) => void;
}

/**
 * Schema-aware value input for workflow conditions.
 *
 * Reads picker_type + options from the schema to determine which sub-component
 * to render:
 *   picker_type=tag   → TagMultiSelect (colored pills, real org tags)
 *   picker_type=stage → StageDropdown  (colored dots, real pipeline stages)
 *   picker_type=user  → UserDropdown   (avatar initials, real org members)
 *   type=boolean      → BooleanToggle  (Yes/No pill toggle)
 *   type=select       → SelectDropdown (options from schema field)
 *   type=number       → Number input
 *   type=date         → Date input
 *   default           → Plain text input
 */
export const SmartValueInput: React.FC<SmartValueInputProps> = ({
  fieldPath,
  fieldType,
  value,
  onChange,
}) => {
  const schema = useBuilderStore((s) => s.schema);
  const field = useMemo(() => findFieldInSchema(schema, fieldPath), [schema, fieldPath]);

  const pickerType = field?.picker_type;
  const options = field?.options;

  // 1. Picker-type based rendering (highest priority)
  if (pickerType === 'tag') {
    return (
      <TagMultiSelect
        tags={schema?.tags || []}
        value={value}
        onChange={onChange}
      />
    );
  }

  if (pickerType === 'stage') {
    return (
      <StageDropdown
        stages={schema?.stages || []}
        value={value}
        onChange={onChange}
      />
    );
  }

  if (pickerType === 'user') {
    return (
      <UserDropdown
        users={schema?.users || []}
        value={value}
        onChange={onChange}
      />
    );
  }

  // 2. Field-type based rendering
  if (fieldType === 'boolean') {
    return <BooleanToggle value={value} onChange={onChange} />;
  }

  if (fieldType === 'select' && options && options.length > 0) {
    return <SelectDropdown options={options} value={value} onChange={onChange} />;
  }

  if (fieldType === 'number') {
    return (
      <input
        type="number"
        value={String(value ?? '')}
        onChange={(e) => {
          const num = parseFloat(e.target.value);
          onChange(isNaN(num) ? '' : num);
        }}
        placeholder="Value"
        className={INPUT_CLASS}
      />
    );
  }

  if (fieldType === 'date') {
    return (
      <input
        type="date"
        value={String(value ?? '')}
        onChange={(e) => onChange(e.target.value)}
        className={INPUT_CLASS}
      />
    );
  }

  // 3. Default: plain text
  return (
    <input
      type="text"
      value={String(value ?? '')}
      onChange={(e) => onChange(e.target.value)}
      placeholder={fieldType === 'array' ? 'Value (e.g. tag name)' : 'Value'}
      className={INPUT_CLASS}
    />
  );
};

// ============================================================
// Shared styles
// ============================================================

const INPUT_CLASS =
  'flex-1 bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none';

// ============================================================
// BooleanToggle
// ============================================================

const BooleanToggle: React.FC<{ value: unknown; onChange: (v: boolean) => void }> = ({
  value,
  onChange,
}) => {
  const isTrue = value === true || value === 'true';
  return (
    <div className="flex items-center gap-1 bg-gray-800 border border-gray-700 rounded-lg p-0.5">
      <button
        type="button"
        onClick={() => onChange(true)}
        className={`px-3 py-1 rounded-md text-xs font-medium transition-all ${
          isTrue
            ? 'bg-emerald-500/20 text-emerald-400 shadow-sm'
            : 'text-gray-500 hover:text-gray-300'
        }`}
      >
        Yes
      </button>
      <button
        type="button"
        onClick={() => onChange(false)}
        className={`px-3 py-1 rounded-md text-xs font-medium transition-all ${
          !isTrue
            ? 'bg-red-500/20 text-red-400 shadow-sm'
            : 'text-gray-500 hover:text-gray-300'
        }`}
      >
        No
      </button>
    </div>
  );
};

// ============================================================
// SelectDropdown — for fields with pre-defined options
// ============================================================

const SelectDropdown: React.FC<{
  options: string[];
  value: unknown;
  onChange: (v: string) => void;
}> = ({ options, value, onChange }) => (
  <select
    value={String(value ?? '')}
    onChange={(e) => onChange(e.target.value)}
    className={`${INPUT_CLASS} min-w-[120px]`}
  >
    <option value="" disabled>
      Select…
    </option>
    {options.map((opt) => (
      <option key={opt} value={opt}>
        {opt}
      </option>
    ))}
  </select>
);

// ============================================================
// TagMultiSelect — colored pill tags with popover picker
// ============================================================

interface TagMultiSelectProps {
  tags: SchemaTag[];
  value: unknown;
  onChange: (v: string[]) => void;
}

const TagMultiSelect: React.FC<TagMultiSelectProps> = ({ tags, value, onChange }) => {
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // Normalize value to string array
  const selected: string[] = Array.isArray(value)
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

  const toggle = (tagName: string) => {
    if (selected.includes(tagName)) {
      onChange(selected.filter((s) => s !== tagName));
    } else {
      onChange([...selected, tagName]);
    }
  };

  const remove = (tagName: string) => {
    onChange(selected.filter((s) => s !== tagName));
  };

  // Resolve tag colors
  const tagMap = useMemo(() => {
    const m = new Map<string, SchemaTag>();
    tags.forEach((t) => m.set(t.name, t));
    return m;
  }, [tags]);

  return (
    <div ref={containerRef} className="relative flex-1">
      {/* Trigger area */}
      <div
        onClick={() => setIsOpen(!isOpen)}
        className={`min-h-[34px] bg-gray-800 border rounded-lg px-2 py-1 flex flex-wrap gap-1 items-center cursor-pointer transition-colors ${
          isOpen ? 'border-purple-500 ring-1 ring-purple-500/30' : 'border-gray-700 hover:border-gray-600'
        }`}
      >
        {selected.length === 0 && (
          <span className="text-gray-500 text-sm px-1">Select tags…</span>
        )}
        {selected.map((tagName) => {
          const tag = tagMap.get(tagName);
          const color = tag?.color || '#6B7280';
          return (
            <span
              key={tagName}
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
              {tagName}
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  remove(tagName);
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
        <div
          className="absolute z-50 top-full left-0 right-0 mt-1 border border-gray-700 rounded-xl shadow-2xl shadow-black/50 overflow-hidden"
          style={{ backgroundColor: '#1a1d27' }}
        >
          {/* Search */}
          <div className="p-2 border-b border-gray-700/50">
            <input
              ref={searchRef}
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search tags…"
              className="w-full bg-gray-800/80 border border-gray-700/50 rounded-lg px-3 py-1.5 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-purple-500/50"
            />
          </div>

          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {filteredTags.length === 0 ? (
              <div className="px-3 py-3 text-center text-xs text-gray-500">
                No matching tags
              </div>
            ) : (
              filteredTags.map((tag) => {
                const isSelected = selected.includes(tag.name);
                return (
                  <button
                    key={tag.id}
                    type="button"
                    onClick={() => toggle(tag.name)}
                    className={`w-full px-3 py-2 text-left flex items-center gap-2.5 text-sm transition-colors ${
                      isSelected
                        ? 'bg-purple-500/10 text-white'
                        : 'text-gray-300 hover:bg-gray-800/60 hover:text-white'
                    }`}
                  >
                    <span
                      className="w-2.5 h-2.5 rounded-full flex-shrink-0"
                      style={{ backgroundColor: tag.color || '#6B7280' }}
                    />
                    <span className="flex-1 truncate">{tag.name}</span>
                    {isSelected && (
                      <svg
                        className="w-3.5 h-3.5 text-purple-400 flex-shrink-0"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2.5}
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </button>
                );
              })
            )}
          </div>

          {/* Footer */}
          <div className="px-3 py-1.5 border-t border-gray-700/50 text-[10px] text-gray-600">
            {selected.length} selected · Click to toggle
          </div>
        </div>
      )}
    </div>
  );
};

// ============================================================
// StageDropdown — colored dots with stage names
// ============================================================

interface StageDropdownProps {
  stages: SchemaStage[];
  value: unknown;
  onChange: (v: string) => void;
}

const StageDropdown: React.FC<StageDropdownProps> = ({ stages, value, onChange }) => {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const selectedValue = String(value ?? '');
  const selectedStage = stages.find((s) => s.name === selectedValue);

  // Close on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const handleSelect = (stage: SchemaStage) => {
    onChange(stage.name);
    setIsOpen(false);
  };

  // Sort by order
  const sortedStages = useMemo(
    () => [...stages].sort((a, b) => a.order - b.order),
    [stages],
  );

  return (
    <div ref={containerRef} className="relative flex-1">
      {/* Trigger */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className={`w-full min-h-[34px] bg-gray-800 border rounded-lg px-3 py-1.5 text-sm text-left flex items-center gap-2 transition-colors ${
          isOpen ? 'border-purple-500 ring-1 ring-purple-500/30' : 'border-gray-700 hover:border-gray-600'
        }`}
      >
        {selectedStage ? (
          <>
            <span
              className="w-2.5 h-2.5 rounded-full flex-shrink-0"
              style={{ backgroundColor: selectedStage.color || '#6B7280' }}
            />
            <span className="text-white flex-1 truncate">{selectedStage.name}</span>
          </>
        ) : (
          <span className="text-gray-500 flex-1">Select stage…</span>
        )}
        <svg
          className={`w-3.5 h-3.5 text-gray-500 transition-transform flex-shrink-0 ${isOpen ? 'rotate-180' : ''}`}
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
        <div
          className="absolute z-50 top-full left-0 right-0 mt-1 border border-gray-700 rounded-xl shadow-2xl shadow-black/50 overflow-hidden"
          style={{ backgroundColor: '#1a1d27' }}
        >
          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {sortedStages.length === 0 ? (
              <div className="px-3 py-3 text-center text-xs text-gray-500">No stages found</div>
            ) : (
              sortedStages.map((stage) => {
                const isSelected = stage.name === selectedValue;
                return (
                  <button
                    key={stage.id}
                    type="button"
                    onClick={() => handleSelect(stage)}
                    className={`w-full px-3 py-2.5 text-left flex items-center gap-2.5 text-sm transition-colors ${
                      isSelected
                        ? 'bg-purple-500/10 text-white'
                        : 'text-gray-300 hover:bg-gray-800/60 hover:text-white'
                    }`}
                  >
                    <span
                      className="w-3 h-3 rounded-full flex-shrink-0"
                      style={{
                        backgroundColor: stage.color || '#6B7280',
                        boxShadow: isSelected
                          ? `0 0 0 2px #1a1d27, 0 0 0 4px ${stage.color}60, 0 0 8px ${stage.color}40`
                          : 'none',
                      }}
                    />
                    <span className="flex-1 truncate">{stage.name}</span>
                    <span className="text-[10px] text-gray-600 tabular-nums">#{stage.order}</span>
                    {isSelected && (
                      <svg
                        className="w-3.5 h-3.5 text-purple-400 flex-shrink-0"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2.5}
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
};

// ============================================================
// UserDropdown — avatar initials with names + emails
// ============================================================

interface UserDropdownProps {
  users: SchemaUser[];
  value: unknown;
  onChange: (v: string) => void;
}

const UserDropdown: React.FC<UserDropdownProps> = ({ users, value, onChange }) => {
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  const selectedValue = String(value ?? '');
  const selectedUser = users.find((u) => u.id === selectedValue);

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

  const filteredUsers = useMemo(() => {
    const q = search.toLowerCase().trim();
    if (!q) return users;
    return users.filter(
      (u) => u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q),
    );
  }, [users, search]);

  const handleSelect = (user: SchemaUser) => {
    onChange(user.id);
    setIsOpen(false);
    setSearch('');
  };

  const getInitials = (name: string) => {
    return name
      .split(' ')
      .map((w) => w[0])
      .slice(0, 2)
      .join('')
      .toUpperCase();
  };

  // Generate consistent pastel color from name
  const getAvatarColor = (name: string) => {
    let hash = 0;
    for (let i = 0; i < name.length; i++) {
      hash = name.charCodeAt(i) + ((hash << 5) - hash);
    }
    const hue = Math.abs(hash) % 360;
    return `hsl(${hue}, 60%, 45%)`;
  };

  return (
    <div ref={containerRef} className="relative flex-1">
      {/* Trigger */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className={`w-full min-h-[34px] bg-gray-800 border rounded-lg px-3 py-1.5 text-sm text-left flex items-center gap-2 transition-colors ${
          isOpen ? 'border-purple-500 ring-1 ring-purple-500/30' : 'border-gray-700 hover:border-gray-600'
        }`}
      >
        {selectedUser ? (
          <>
            <span
              className="w-5 h-5 rounded-full flex items-center justify-center text-[9px] font-bold text-white flex-shrink-0"
              style={{ backgroundColor: getAvatarColor(selectedUser.name) }}
            >
              {getInitials(selectedUser.name)}
            </span>
            <span className="text-white flex-1 truncate">{selectedUser.name}</span>
          </>
        ) : (
          <span className="text-gray-500 flex-1">Select user…</span>
        )}
        <svg
          className={`w-3.5 h-3.5 text-gray-500 transition-transform flex-shrink-0 ${isOpen ? 'rotate-180' : ''}`}
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
        <div
          className="absolute z-50 top-full left-0 right-0 mt-1 border border-gray-700 rounded-xl shadow-2xl shadow-black/50 overflow-hidden"
          style={{ backgroundColor: '#1a1d27' }}
        >
          {/* Search */}
          {users.length > 5 && (
            <div className="p-2 border-b border-gray-700/50">
              <input
                ref={searchRef}
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search by name or email…"
                className="w-full bg-gray-800/80 border border-gray-700/50 rounded-lg px-3 py-1.5 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-purple-500/50"
              />
            </div>
          )}

          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {filteredUsers.length === 0 ? (
              <div className="px-3 py-3 text-center text-xs text-gray-500">No matching users</div>
            ) : (
              filteredUsers.map((user) => {
                const isSelected = user.id === selectedValue;
                const color = getAvatarColor(user.name);
                return (
                  <button
                    key={user.id}
                    type="button"
                    onClick={() => handleSelect(user)}
                    className={`w-full px-3 py-2.5 text-left flex items-center gap-2.5 text-sm transition-colors ${
                      isSelected
                        ? 'bg-purple-500/10 text-white'
                        : 'text-gray-300 hover:bg-gray-800/60 hover:text-white'
                    }`}
                  >
                    <span
                      className="w-6 h-6 rounded-full flex items-center justify-center text-[10px] font-bold text-white flex-shrink-0"
                      style={{ backgroundColor: color }}
                    >
                      {getInitials(user.name)}
                    </span>
                    <div className="flex-1 min-w-0">
                      <div className="text-sm truncate">{user.name}</div>
                      <div className="text-[11px] text-gray-500 truncate">{user.email}</div>
                    </div>
                    {isSelected && (
                      <svg
                        className="w-3.5 h-3.5 text-purple-400 flex-shrink-0"
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2.5}
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
};

// Re-export sub-components for use in ActionConfigPanel (P10, P16, etc.)
export { TagMultiSelect, StageDropdown, UserDropdown, BooleanToggle, SelectDropdown };
