import React from 'react';
import { INPUT_CLASS } from './shared';

// ============================================================
// SelectDropdown — for fields with pre-defined options
// ============================================================

export interface SelectDropdownProps {
  options: string[];
  value: unknown;
  onChange: (v: string) => void;
}

export const SelectDropdown: React.FC<SelectDropdownProps> = ({ options, value, onChange }) => (
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
