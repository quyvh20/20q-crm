import React from 'react';
import { INPUT_CLASS } from './shared';

// ============================================================
// NumberInput — numeric input with optional min/max constraints
// ============================================================

export interface NumberInputProps {
  value: unknown;
  onChange: (v: number | '') => void;
  min?: number;
  max?: number;
}

export const NumberInput: React.FC<NumberInputProps> = ({ value, onChange, min, max }) => {
  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const raw = e.target.value;
    if (raw === '') { onChange(''); return; }
    const num = parseFloat(raw);
    if (isNaN(num)) return;
    // Clamp to min/max if defined
    const clamped =
      min !== undefined && num < min ? min
      : max !== undefined && num > max ? max
      : num;
    onChange(clamped);
  };

  return (
    <input
      type="number"
      value={String(value ?? '')}
      onChange={handleChange}
      min={min}
      max={max}
      placeholder={min !== undefined && max !== undefined ? `${min}–${max}` : 'Value'}
      className={INPUT_CLASS}
    />
  );
};
