import React, { useMemo } from 'react';
import { INPUT_CLASS } from './shared';

export interface DateInputProps {
  value: unknown;
  onChange: (v: string) => void;
}

export const DateInput: React.FC<DateInputProps> = ({ value, onChange }) => {
  const displayValue = useMemo(() => {
    const str = String(value ?? '');
    if (!str) return '';
    if (/^\d{4}-\d{2}-\d{2}$/.test(str)) return str;
    const d = new Date(str);
    if (isNaN(d.getTime())) return '';
    return d.toISOString().slice(0, 10);
  }, [value]);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const dateStr = e.target.value;
    if (!dateStr) { onChange(''); return; }
    onChange(new Date(dateStr + 'T00:00:00.000Z').toISOString());
  };

  return (
    <input type="date" value={displayValue} onChange={handleChange} className={INPUT_CLASS} />
  );
};
