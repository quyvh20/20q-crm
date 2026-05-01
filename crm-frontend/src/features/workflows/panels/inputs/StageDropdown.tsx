import React, { useState, useRef, useEffect, useMemo } from 'react';
import type { SchemaStage } from '../../api';

// ============================================================
// StageDropdown — colored dots with stage names
// ============================================================

export interface StageDropdownProps {
  stages: SchemaStage[];
  value: unknown;
  onChange: (v: string) => void;
}

export const StageDropdown: React.FC<StageDropdownProps> = ({ stages, value, onChange }) => {
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
