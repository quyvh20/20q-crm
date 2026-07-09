import React from 'react';

// ============================================================
// BooleanToggle — Yes/No pill toggle, emits boolean
// ============================================================

export interface BooleanToggleProps {
  value: unknown;
  onChange: (v: boolean) => void;
}

export const BooleanToggle: React.FC<BooleanToggleProps> = ({ value, onChange }) => {
  const isTrue = value === true || value === 'true';
  return (
    <div className="flex items-center gap-1 bg-background border border-border rounded-lg p-0.5">
      <button
        type="button"
        onClick={() => onChange(true)}
        className={`px-3 py-1 rounded-md text-xs font-medium transition-all ${
          isTrue
            ? 'bg-primary/10 text-primary shadow-sm'
            : 'text-muted-foreground hover:text-foreground'
        }`}
      >
        Yes
      </button>
      <button
        type="button"
        onClick={() => onChange(false)}
        className={`px-3 py-1 rounded-md text-xs font-medium transition-all ${
          !isTrue
            ? 'bg-destructive/10 text-destructive shadow-sm'
            : 'text-muted-foreground hover:text-foreground'
        }`}
      >
        No
      </button>
    </div>
  );
};
