import React from 'react';
import { INPUT_CLASS } from './shared';

export interface StringInputProps {
  value: unknown;
  onChange: (v: string) => void;
  operator: string;
  fieldType: string;
}

export const StringInput: React.FC<StringInputProps> = ({ value, onChange, operator, fieldType }) => {
  const placeholder =
    fieldType === 'array' ? 'Value (e.g. tag name)'
    : operator === 'contains' || operator === 'not_contains' ? 'Search text…'
    : operator === 'starts_with' ? 'Prefix…'
    : operator === 'ends_with' ? 'Suffix…'
    : 'Value';

  return (
    <input
      type="text"
      value={String(value ?? '')}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className={INPUT_CLASS}
    />
  );
};
