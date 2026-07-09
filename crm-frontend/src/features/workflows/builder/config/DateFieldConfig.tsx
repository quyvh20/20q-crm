import React, { useEffect, useMemo } from 'react';
import { ChevronDown, CalendarClock } from 'lucide-react';
import { useBuilderStore } from '../../store';
import type { WorkflowSchema, SchemaEntity } from '../../api';
import {
  type DateFieldParams,
  type OffsetDirection,
  offsetToDirection,
  directionToOffset,
  describeDateField,
  DEFAULT_AT_TIME,
} from '../../dateField';
import { browserTimeZone, allTimeZones } from '../../cron';

// ============================================================
// DateFieldConfig — the `date_field` trigger form (A4.3)
// ============================================================
//
// "Fire N days before/after <object>.<date field> at <time>". Object + date-field
// pickers read straight from the loaded workflow schema (every object carries its
// fields inline, date fields included). The trigger.params are the source of truth
// (no local draft needed — direction/days derive purely from the signed offset).

const selectClass =
  'bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground font-medium focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring cursor-pointer appearance-none transition-colors hover:border-muted-foreground/40';
const fieldLabelCls = 'text-xs text-muted-foreground font-medium uppercase tracking-wider';

const Select: React.FC<{
  value: string;
  onChange: (v: string) => void;
  invalid?: boolean;
  disabled?: boolean;
  children: React.ReactNode;
  'aria-label'?: string;
}> = ({ value, onChange, invalid, disabled, children, ...rest }) => (
  <div className="relative">
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      className={`${selectClass} w-full ${invalid ? '!border-destructive' : ''} ${disabled ? 'opacity-60 cursor-not-allowed' : ''}`}
      style={{ paddingRight: '2rem' }}
      aria-label={rest['aria-label']}
    >
      {children}
    </select>
    <ChevronDown className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
  </div>
);

// Objects that can carry a date field: real CRM objects only (no trigger pseudo-entity).
function objectEntities(schema: WorkflowSchema | null): SchemaEntity[] {
  if (!schema) return [];
  const builtins = schema.entities.filter((e) => e.key !== 'trigger');
  return [...builtins, ...(schema.custom_objects || [])];
}

const DIRECTION_OPTIONS: { value: OffsetDirection; label: string }[] = [
  { value: 'before', label: 'before' },
  { value: 'on', label: 'on' },
  { value: 'after', label: 'after' },
];

export const DateFieldConfig: React.FC = () => {
  const { trigger, setTrigger, schema, errors } = useBuilderStore();

  const params = (trigger?.params || {}) as Partial<DateFieldParams>;
  const object = params.object || '';
  const field = params.field || '';
  const atTime = params.at_time || DEFAULT_AT_TIME;
  const timezone = params.timezone || browserTimeZone();
  const { direction, days } = offsetToDirection(params.offset_days ?? 0);

  const objects = useMemo(() => objectEntities(schema), [schema]);

  // Date fields (type === 'date') of the selected object.
  const selectedEntity = useMemo(() => objects.find((e) => e.key === object) || null, [objects, object]);
  const dateFields = useMemo(
    () => (selectedEntity ? selectedEntity.fields.filter((f) => f.type === 'date') : []),
    [selectedEntity],
  );
  const fieldLabel = useMemo(() => dateFields.find((f) => f.path === field)?.label, [dateFields, field]);

  const emit = (patch: Partial<DateFieldParams>) => {
    const next: DateFieldParams = {
      object,
      field,
      offset_days: params.offset_days ?? 0,
      at_time: atTime,
      timezone,
      ...patch,
    };
    setTrigger({ type: 'date_field', params: { ...next } });
  };

  // Auto-select the first object that has a date field, then its first date field,
  // so the form lands on a usable default instead of two empty pickers.
  useEffect(() => {
    if (object || objects.length === 0) return;
    const firstWithDate = objects.find((e) => e.fields.some((f) => f.type === 'date'));
    if (firstWithDate) {
      const firstDate = firstWithDate.fields.find((f) => f.type === 'date');
      emit({ object: firstWithDate.key, field: firstDate?.path || '' });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [objects, object]);

  const onObjectChange = (newObject: string) => {
    // Switching object invalidates the field — auto-pick the new object's first date field.
    const ent = objects.find((e) => e.key === newObject);
    const firstDate = ent?.fields.find((f) => f.type === 'date');
    emit({ object: newObject, field: firstDate?.path || '' });
  };

  const onDirectionChange = (dir: OffsetDirection) => {
    // Going to before/after from "on" needs a positive magnitude — default to 1.
    const mag = dir === 'on' ? 0 : Math.max(1, days);
    emit({ offset_days: directionToOffset(dir, mag) });
  };

  const onDaysChange = (val: string) => {
    const n = Math.max(0, Math.floor(Number(val) || 0));
    emit({ offset_days: directionToOffset(direction === 'on' ? 'before' : direction, n) });
  };

  const zones = useMemo(() => {
    const list = allTimeZones();
    return list.includes(timezone) ? list : [timezone, ...list];
  }, [timezone]);

  const objectError = errors['trigger.params.object']?.[0];
  const fieldError = errors['trigger.params.field']?.[0];
  const noDateFields = Boolean(object) && dateFields.length === 0;

  return (
    <div className="p-3 rounded-xl border border-border/60 bg-muted/40 space-y-3 mt-3">
      <div className="flex items-center gap-2">
        <CalendarClock className="h-4 w-4 text-indigo-600 dark:text-indigo-400" />
        <p className="text-xs text-foreground font-medium">Date field</p>
      </div>

      {/* Object */}
      <div className="space-y-1.5">
        <label className={fieldLabelCls}>Object</label>
        <Select value={object} onChange={onObjectChange} invalid={Boolean(objectError)} aria-label="Object">
          <option value="" disabled>Select object…</option>
          {objects.map((e) => (
            <option key={e.key} value={e.key}>{e.icon || '📦'} {e.label}</option>
          ))}
        </Select>
      </div>

      {/* Date field */}
      <div className="space-y-1.5">
        <label className={fieldLabelCls}>Date field</label>
        <Select
          value={field}
          onChange={(v) => emit({ field: v })}
          invalid={Boolean(fieldError)}
          disabled={!object || noDateFields}
          aria-label="Date field"
        >
          <option value="" disabled>Select date field…</option>
          {dateFields.map((f) => (
            <option key={f.path} value={f.path}>{f.label}</option>
          ))}
        </Select>
        {noDateFields && (
          <p className="text-[10px] text-amber-600 dark:text-amber-400">This object has no date fields to trigger on.</p>
        )}
      </div>

      {/* Offset: N days before/on/after */}
      <div className="space-y-1.5">
        <label className={fieldLabelCls}>When</label>
        <div className="flex items-center gap-2">
          <input
            type="number"
            min={0}
            value={direction === 'on' ? '' : days}
            onChange={(e) => onDaysChange(e.target.value)}
            disabled={direction === 'on'}
            aria-label="Offset days"
            placeholder="0"
            className="w-16 bg-background border border-border rounded-lg px-2 py-1.5 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring disabled:opacity-60"
          />
          <span className="text-xs text-muted-foreground">day(s)</span>
          <div className="flex-1">
            <Select value={direction} onChange={(v) => onDirectionChange(v as OffsetDirection)} aria-label="Direction">
              {DIRECTION_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>{o.label}</option>
              ))}
            </Select>
          </div>
        </div>
        <p className="text-[10px] text-muted-foreground/70">the selected date. "on" fires on the date itself.</p>
      </div>

      {/* At time */}
      <div className="space-y-1.5">
        <label className={fieldLabelCls}>At</label>
        <input
          type="time"
          value={atTime}
          onChange={(e) => emit({ at_time: e.target.value })}
          aria-label="Time of day"
          className="bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground w-full focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        />
      </div>

      {/* Timezone */}
      <div className="space-y-1.5">
        <label className={fieldLabelCls}>Timezone</label>
        <Select value={timezone} onChange={(v) => emit({ timezone: v })} aria-label="Timezone">
          {zones.map((z) => (
            <option key={z} value={z}>{z}</option>
          ))}
        </Select>
      </div>

      {(objectError || fieldError) && (
        <p className="text-[11px] text-destructive">⚠ {objectError || fieldError}</p>
      )}

      {/* Human preview */}
      <div className="px-3 py-2 rounded-lg bg-primary/5 border border-primary/20">
        <p className="text-xs text-primary/70">
          <span className="text-primary font-medium">Preview: </span>
          {field
            ? describeDateField({ field, offset_days: params.offset_days ?? 0, at_time: atTime, timezone }, fieldLabel)
            : 'Pick a date field to preview.'}
        </p>
      </div>
    </div>
  );
};
