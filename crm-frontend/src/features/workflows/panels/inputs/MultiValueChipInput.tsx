import React, { useState, useRef } from 'react';

export interface MultiValueChipInputProps {
  value: unknown;
  onChange: (v: string[]) => void;
  placeholder?: string;
}

export const MultiValueChipInput: React.FC<MultiValueChipInputProps> = ({
  value, onChange, placeholder = 'Type value + Enter',
}) => {
  const [inputValue, setInputValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  const chips: string[] = Array.isArray(value)
    ? value.map(String)
    : typeof value === 'string' && value
      ? value.split(',').map((s) => s.trim()).filter(Boolean)
      : [];

  const addChip = () => {
    const trimmed = inputValue.trim();
    if (trimmed && !chips.includes(trimmed)) {
      onChange([...chips, trimmed]);
    }
    setInputValue('');
  };

  const removeChip = (chip: string) => {
    onChange(chips.filter((c) => c !== chip));
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      addChip();
    } else if (e.key === 'Backspace' && !inputValue && chips.length > 0) {
      onChange(chips.slice(0, -1));
    }
  };

  return (
    <div
      className="flex-1 min-h-[34px] bg-gray-800 border border-gray-700 rounded-lg px-2 py-1 flex flex-wrap gap-1 items-center cursor-text focus-within:border-purple-500 focus-within:ring-1 focus-within:ring-purple-500/30 transition-colors"
      onClick={() => inputRef.current?.focus()}
    >
      {chips.map((chip) => (
        <span
          key={chip}
          className="inline-flex items-center gap-0.5 px-2 py-0.5 rounded-md text-xs font-medium bg-purple-500/15 text-purple-300 border border-purple-500/25"
        >
          {chip}
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); removeChip(chip); }}
            className="ml-0.5 hover:text-white transition-colors"
          >
            ×
          </button>
        </span>
      ))}
      <input
        ref={inputRef}
        type="text"
        value={inputValue}
        onChange={(e) => setInputValue(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={() => { if (inputValue.trim()) addChip(); }}
        placeholder={chips.length === 0 ? placeholder : ''}
        className="flex-1 min-w-[60px] bg-transparent text-sm text-white placeholder-gray-500 outline-none"
      />
    </div>
  );
};
