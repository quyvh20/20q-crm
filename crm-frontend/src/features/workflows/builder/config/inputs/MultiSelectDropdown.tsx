import React, { useState, useRef, useEffect } from 'react';
import { Check } from 'lucide-react';

export interface MultiSelectDropdownProps {
  options: string[];
  value: unknown;
  onChange: (v: string[]) => void;
}

export const MultiSelectDropdown: React.FC<MultiSelectDropdownProps> = ({ options, value, onChange }) => {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const selected: string[] = Array.isArray(value)
    ? value.map(String)
    : typeof value === 'string' && value
      ? value.split(',').map((s) => s.trim()).filter(Boolean)
      : [];

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setIsOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  const toggle = (opt: string) => {
    if (selected.includes(opt)) {
      onChange(selected.filter((s) => s !== opt));
    } else {
      onChange([...selected, opt]);
    }
  };

  return (
    <div ref={containerRef} className="relative flex-1">
      <div
        onClick={() => setIsOpen(!isOpen)}
        className={`min-h-[34px] bg-background border rounded-lg px-2 py-1 flex flex-wrap gap-1 items-center cursor-pointer transition-colors ${
          isOpen ? 'border-ring ring-1 ring-ring/40' : 'border-border hover:border-muted-foreground/40'
        }`}
      >
        {selected.length === 0 && (
          <span className="text-muted-foreground text-sm px-1">Select values…</span>
        )}
        {selected.map((opt) => (
          <span
            key={opt}
            className="inline-flex items-center gap-0.5 px-2 py-0.5 rounded-md text-xs font-medium bg-primary/10 text-primary border border-primary/40"
          >
            {opt}
            <button
              type="button"
              onClick={(e) => { e.stopPropagation(); toggle(opt); }}
              className="ml-0.5 hover:text-foreground transition-colors"
            >
              ×
            </button>
          </span>
        ))}
      </div>

      {isOpen && (
        <div className="absolute z-50 top-full left-0 right-0 mt-1 bg-popover text-popover-foreground border border-border rounded-xl shadow-2xl shadow-black/50 overflow-hidden">
          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {options.map((opt) => {
              const isSelected = selected.includes(opt);
              return (
                <button
                  key={opt}
                  type="button"
                  onClick={() => toggle(opt)}
                  className={`w-full px-3 py-2 text-left flex items-center gap-2.5 text-sm transition-colors ${
                    isSelected
                      ? 'bg-primary/10 text-primary'
                      : 'text-foreground hover:bg-accent hover:text-accent-foreground'
                  }`}
                >
                  <span className="flex-1 truncate">{opt}</span>
                  {isSelected && (
                    <Check className="w-3.5 h-3.5 text-primary flex-shrink-0" />
                  )}
                </button>
              );
            })}
          </div>
          <div className="px-3 py-1.5 border-t border-border/60 text-[10px] text-muted-foreground/70">
            {selected.length} selected · Click to toggle
          </div>
        </div>
      )}
    </div>
  );
};
