import React, { useState, useRef, useEffect, useMemo } from 'react';
import { ChevronDown, Check } from 'lucide-react';
import type { SchemaUser } from '../../../api';

// ============================================================
// UserDropdown — avatar initials with names + emails
// ============================================================

export interface UserDropdownProps {
  users: SchemaUser[];
  value: unknown;
  onChange: (v: string) => void;
}

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

export const UserDropdown: React.FC<UserDropdownProps> = ({ users, value, onChange }) => {
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

  return (
    <div ref={containerRef} className="relative flex-1">
      {/* Trigger */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className={`w-full min-h-[34px] bg-background border rounded-lg px-3 py-1.5 text-sm text-left flex items-center gap-2 transition-colors ${
          isOpen ? 'border-ring ring-1 ring-ring/40' : 'border-border hover:border-muted-foreground/40'
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
            <span className="text-foreground flex-1 truncate">{selectedUser.name}</span>
          </>
        ) : (
          <span className="text-muted-foreground flex-1">Select user…</span>
        )}
        <ChevronDown
          className={`w-3.5 h-3.5 text-muted-foreground transition-transform flex-shrink-0 ${isOpen ? 'rotate-180' : ''}`}
        />
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div className="absolute z-50 top-full left-0 right-0 mt-1 border border-border rounded-xl shadow-2xl shadow-black/50 overflow-hidden bg-popover text-popover-foreground">
          {/* Search */}
          {users.length > 5 && (
            <div className="p-2 border-b border-border/60">
              <input
                ref={searchRef}
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search by name or email…"
                className="w-full bg-background border border-border/60 rounded-lg px-3 py-1.5 text-sm text-foreground placeholder:text-muted-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
              />
            </div>
          )}

          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {filteredUsers.length === 0 ? (
              <div className="px-3 py-3 text-center text-xs text-muted-foreground">No matching users</div>
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
                        ? 'bg-primary/10 text-primary'
                        : 'text-foreground hover:bg-accent hover:text-accent-foreground'
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
                      <div className="text-[11px] text-muted-foreground truncate">{user.email}</div>
                    </div>
                    {isSelected && (
                      <Check className="w-3.5 h-3.5 text-primary flex-shrink-0" strokeWidth={2.5} />
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
