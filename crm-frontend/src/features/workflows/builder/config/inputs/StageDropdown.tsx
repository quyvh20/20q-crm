import React, { useState, useRef, useEffect, useMemo } from 'react';
import { ChevronDown, Check } from 'lucide-react';
import type { SchemaStage } from '../../../api';

// ============================================================
// StageDropdown — colored dots with stage names, grouped by pipeline
// Emits stage ID (UUID), displays stage name.
// ============================================================

/** One pipeline's worth of stages, or the trailing catch-all when label is null. */
interface StageGroup {
  key: string;
  /** Pipeline name for the section header; null = the unlabelled trailing group. */
  label: string | null;
  stages: SchemaStage[];
}

export interface StageDropdownProps {
  stages: SchemaStage[];
  value: unknown;
  onChange: (v: string) => void;
  /** If true, prepend an "Any Stage" option with value "*" */
  allowAny?: boolean;
  /** Custom placeholder when nothing is selected */
  placeholder?: string;
}

export const StageDropdown: React.FC<StageDropdownProps> = ({ stages, value, onChange, allowAny, placeholder }) => {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const selectedValue = String(value ?? '');
  const isAnySelected = allowAny && selectedValue === '*';
  const selectedStage = isAnySelected ? null : stages.find((s) => s.id === selectedValue);

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
    onChange(stage.id);
    setIsOpen(false);
  };

  // Group by pipeline. Without this the list is unusable across boards: the row
  // renders name + colour + #order and every one of those can repeat, so two
  // pipelines each holding a "Closed Won" at order 4 produce two identical rows
  // and there is no way to tell which id you are about to emit.
  // Pipeline order follows first appearance in the schema payload (the backend
  // emits stages in pipeline-position order); stages sort by `order` within a
  // group, as they did before.
  const groups = useMemo<StageGroup[]>(() => {
    const byPipeline = new Map<string, StageGroup>();
    const ungrouped: SchemaStage[] = [];

    for (const stage of stages) {
      const label = stage.pipeline_name?.trim();
      if (!label) {
        // Pre-backfill stage — show it unlabelled rather than dropping it.
        ungrouped.push(stage);
        continue;
      }
      // Two pipelines may legitimately share a name, so identity is the id and
      // the name is only a fallback for a stage that carries one without the other.
      const key = stage.pipeline_id || `name:${label}`;
      const existing = byPipeline.get(key);
      if (existing) existing.stages.push(stage);
      else byPipeline.set(key, { key, label, stages: [stage] });
    }

    const result = [...byPipeline.values()];
    if (ungrouped.length > 0) {
      result.push({ key: '__ungrouped__', label: null, stages: ungrouped });
    }
    for (const group of result) group.stages.sort((a, b) => a.order - b.order);
    return result;
  }, [stages]);

  // A single-pipeline org gains nothing from a header repeated over every row —
  // headers only earn their space once there is something to disambiguate.
  const showGroupHeaders = groups.length > 1;

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
        {isAnySelected ? (
          <>
            <span className="w-2.5 h-2.5 rounded-full flex-shrink-0 bg-muted-foreground" />
            <span className="text-foreground flex-1 truncate">Any Stage</span>
          </>
        ) : selectedStage ? (
          <>
            <span
              className="w-2.5 h-2.5 rounded-full flex-shrink-0"
              style={{ backgroundColor: selectedStage.color || '#6B7280' }}
            />
            {/* Same ambiguity as the list: a closed picker showing only "Closed Won"
                does not say which board's stage the workflow is actually bound to. */}
            {showGroupHeaders && selectedStage.pipeline_name && (
              <span className="text-muted-foreground text-xs truncate">{selectedStage.pipeline_name} ›</span>
            )}
            <span className="text-foreground flex-1 truncate">{selectedStage.name}</span>
          </>
        ) : (
          <span className="text-muted-foreground flex-1">{placeholder || 'Select stage…'}</span>
        )}
        <ChevronDown
          className={`w-3.5 h-3.5 text-muted-foreground transition-transform flex-shrink-0 ${isOpen ? 'rotate-180' : ''}`}
        />
      </button>

      {/* Dropdown */}
      {isOpen && (
        <div className="absolute z-50 top-full left-0 right-0 mt-1 border border-border rounded-xl shadow-2xl shadow-black/50 overflow-hidden bg-popover text-popover-foreground">
          <div className="max-h-48 overflow-y-auto overscroll-contain">
            {stages.length === 0 && !allowAny ? (
              <div className="px-3 py-3 text-center text-xs text-muted-foreground">No stages found</div>
            ) : (
              <>
                {/* "Any Stage" option */}
                {allowAny && (
                  <button
                    type="button"
                    onClick={() => { onChange('*'); setIsOpen(false); }}
                    className={`w-full px-3 py-2.5 text-left flex items-center gap-2.5 text-sm transition-colors border-b border-border/60 ${
                      isAnySelected
                        ? 'bg-primary/10 text-primary'
                        : 'text-foreground hover:bg-accent hover:text-accent-foreground'
                    }`}
                  >
                    <span className="w-3 h-3 rounded-full flex-shrink-0 bg-muted-foreground" />
                    <span className="flex-1 truncate">Any Stage</span>
                    {isAnySelected && (
                      <Check className="w-3.5 h-3.5 text-primary flex-shrink-0" strokeWidth={2.5} />
                    )}
                  </button>
                )}
                {groups.map((group) => (
                  <div key={group.key}>
                    {/* Sticky so the board a row belongs to stays visible while the
                        list scrolls — bg-popover, not a translucent tint, or rows
                        scroll through the header. */}
                    {showGroupHeaders && (
                      <div className="sticky top-0 z-10 bg-popover px-3 py-1.5 border-b border-border/60 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground/80 truncate">
                        {group.label ?? 'Other stages'}
                      </div>
                    )}
                    {group.stages.map((stage) => {
                      const isSelected = stage.id === selectedValue;
                      return (
                        <button
                          key={stage.id}
                          type="button"
                          onClick={() => handleSelect(stage)}
                          className={`w-full px-3 py-2.5 text-left flex items-center gap-2.5 text-sm transition-colors ${
                            isSelected
                              ? 'bg-primary/10 text-primary'
                              : 'text-foreground hover:bg-accent hover:text-accent-foreground'
                          }`}
                        >
                          <span
                            className="w-3 h-3 rounded-full flex-shrink-0"
                            style={{
                              backgroundColor: stage.color || '#6B7280',
                              boxShadow: isSelected
                                ? `0 0 0 2px hsl(var(--popover)), 0 0 0 4px ${stage.color}60, 0 0 8px ${stage.color}40`
                                : 'none',
                            }}
                          />
                          <span className="flex-1 truncate">{stage.name}</span>
                          <span className="text-[10px] text-muted-foreground/70 tabular-nums">#{stage.order}</span>
                          {isSelected && (
                            <Check className="w-3.5 h-3.5 text-primary flex-shrink-0" strokeWidth={2.5} />
                          )}
                        </button>
                      );
                    })}
                  </div>
                ))}
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
