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
