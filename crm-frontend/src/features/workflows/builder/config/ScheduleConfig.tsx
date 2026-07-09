import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ChevronDown, CalendarClock } from 'lucide-react';
import { useBuilderStore } from '../../store';
import {
  type ScheduleFrequency,
  type ScheduleModel,
  parseCron,
  buildCron,
  describeCron,
  isValidCron,
  browserTimeZone,
  allTimeZones,
  WEEKDAYS,
  DEFAULT_CRON,
} from '../../cron';

// ============================================================
// ScheduleConfig — the `schedule` trigger form (A4.2)
// ============================================================
//
// Renders a friendly "frequency + time + day" editor on top of the backend's
// 5-field cron model (see cron.ts). The cron string lives on
// `trigger.params.cron`; `trigger.params.timezone` holds an IANA zone. A local
// draft mirrors the store so an explicit "Custom" choice isn't stomped back to a
// structured frequency just because the expression happens to match one.

const selectClass =
  'bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground font-medium focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring cursor-pointer appearance-none transition-colors hover:border-muted-foreground/40';

const FREQUENCY_OPTIONS: { value: ScheduleFrequency; label: string }[] = [
  { value: 'hourly', label: 'Hourly' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'monthly', label: 'Monthly' },
  { value: 'custom', label: 'Custom (cron)' },
];

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

/** A styled native <select> with the shared chevron affordance. */
const Select: React.FC<{
  value: string;
  onChange: (v: string) => void;
  invalid?: boolean;
  children: React.ReactNode;
  'aria-label'?: string;
}> = ({ value, onChange, invalid, children, ...rest }) => (
  <div className="relative">
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={`${selectClass} w-full ${invalid ? '!border-destructive' : ''}`}
      style={{ paddingRight: '2rem' }}
      aria-label={rest['aria-label']}
    >
      {children}
    </select>
    <ChevronDown className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
  </div>
);

const fieldLabel = 'text-xs text-muted-foreground font-medium uppercase tracking-wider';

export const ScheduleConfig: React.FC = () => {
  const { trigger, setTrigger, errors } = useBuilderStore();

  const params = trigger?.params || {};
  const cron = (params.cron as string) || DEFAULT_CRON;
  const timezone = (params.timezone as string) || browserTimeZone();

  // Local draft: source of truth for the *selected* frequency (so choosing Custom
  // sticks even when the expression matches a structured shape). Kept in sync with
  // the store on external changes only (workflow load / trigger swap) via lastCronRef.
  const [draft, setDraft] = useState<ScheduleModel>(() => parseCron(cron));
  const lastCronRef = useRef<string>(cron);

  useEffect(() => {
    if (cron !== lastCronRef.current) {
      lastCronRef.current = cron;
      setDraft(parseCron(cron));
    }
  }, [cron]);

  const emit = useCallback(
    (next: ScheduleModel, tz: string) => {
      const nextCron = buildCron(next);
      lastCronRef.current = nextCron;
      setDraft(next);
      setTrigger({ type: 'schedule', params: { cron: nextCron, timezone: tz } });
    },
    [setTrigger],
  );

  const onFrequency = (f: ScheduleFrequency) => {
    if (f === draft.frequency) return;
    // Switching to custom seeds the text box with the current derived cron so the
    // user tweaks a valid expression rather than starting from an empty field.
    const next: ScheduleModel = f === 'custom' ? { ...draft, frequency: f, cron: buildCron(draft) } : { ...draft, frequency: f };
    emit(next, timezone);
  };

  const onTime = (val: string) => {
    // <input type="time"> value is "HH:MM".
    const [h, m] = val.split(':');
    const hour = Number(h);
    const minute = Number(m);
    if (Number.isNaN(hour) || Number.isNaN(minute)) return;
    emit({ ...draft, hour, minute }, timezone);
  };

  const zones = useMemo(() => {
    const list = allTimeZones();
    return list.includes(timezone) ? list : [timezone, ...list];
  }, [timezone]);

  // Day-of-month: cap the picker at 28 so a monthly schedule fires every month.
  // If a saved cron used 29–31, keep it selectable so it round-trips.
  const domOptions = useMemo(() => {
    const opts = Array.from({ length: 28 }, (_, i) => i + 1);
    if (draft.dayOfMonth > 28 && !opts.includes(draft.dayOfMonth)) opts.push(draft.dayOfMonth);
    return opts;
  }, [draft.dayOfMonth]);

  const cronError = errors['trigger.params.cron']?.[0];
  const liveInvalid = draft.frequency === 'custom' && draft.cron.trim() !== '' && !isValidCron(draft.cron);
  const timeValue = `${pad2(draft.hour)}:${pad2(draft.minute)}`;

  return (
    <div className="p-3 rounded-xl border border-border/60 bg-muted/40 space-y-3 mt-3">
      <div className="flex items-center gap-2">
        <CalendarClock className="h-4 w-4 text-indigo-600 dark:text-indigo-400" />
        <p className="text-xs text-foreground font-medium">Schedule</p>
      </div>

      {/* Frequency */}
      <div className="space-y-1.5">
        <label className={fieldLabel}>Runs</label>
        <Select value={draft.frequency} onChange={(v) => onFrequency(v as ScheduleFrequency)} aria-label="Frequency">
          {FREQUENCY_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>
      </div>

      {/* Hourly: minute-of-hour */}
      {draft.frequency === 'hourly' && (
        <div className="space-y-1.5">
          <label className={fieldLabel}>At minute</label>
          <Select
            value={String(draft.minute)}
            onChange={(v) => emit({ ...draft, minute: Number(v) }, timezone)}
            aria-label="Minute"
          >
            {Array.from({ length: 60 }, (_, i) => i).map((m) => (
              <option key={m} value={m}>
                :{pad2(m)}
              </option>
            ))}
          </Select>
        </div>
      )}

      {/* Weekly: day-of-week */}
      {draft.frequency === 'weekly' && (
        <div className="space-y-1.5">
          <label className={fieldLabel}>On</label>
          <Select
            value={String(draft.dayOfWeek)}
            onChange={(v) => emit({ ...draft, dayOfWeek: Number(v) }, timezone)}
            aria-label="Day of week"
          >
            {WEEKDAYS.map((d, i) => (
              <option key={d} value={i}>
                {d}
              </option>
            ))}
          </Select>
        </div>
      )}

      {/* Monthly: day-of-month */}
      {draft.frequency === 'monthly' && (
        <div className="space-y-1.5">
          <label className={fieldLabel}>On day</label>
          <Select
            value={String(draft.dayOfMonth)}
            onChange={(v) => emit({ ...draft, dayOfMonth: Number(v) }, timezone)}
            aria-label="Day of month"
          >
            {domOptions.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </Select>
          <p className="text-[10px] text-muted-foreground/70">Days are capped at 28 so the schedule fires every month.</p>
        </div>
      )}

      {/* Daily/Weekly/Monthly: time-of-day */}
      {(draft.frequency === 'daily' || draft.frequency === 'weekly' || draft.frequency === 'monthly') && (
        <div className="space-y-1.5">
          <label className={fieldLabel}>At</label>
          <input
            type="time"
            value={timeValue}
            onChange={(e) => onTime(e.target.value)}
            aria-label="Time of day"
            className="bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground w-full focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
          />
        </div>
      )}

      {/* Custom cron */}
      {draft.frequency === 'custom' && (
        <div className="space-y-1.5">
          <label className={fieldLabel}>Cron expression</label>
          <input
            type="text"
            value={draft.cron}
            onChange={(e) => emit({ ...draft, cron: e.target.value }, timezone)}
            placeholder="0 9 * * 1"
            spellCheck={false}
            aria-label="Cron expression"
            className={`bg-background border rounded-lg px-3 py-1.5 text-sm text-foreground font-mono w-full focus:outline-none focus:ring-1 focus:ring-ring ${
              cronError || liveInvalid ? 'border-destructive focus:border-destructive' : 'border-border focus:border-ring'
            }`}
          />
          <p className="text-[10px] text-muted-foreground/70">
            5 fields: <code>minute hour day-of-month month day-of-week</code>. Presets pick these for you.
          </p>
        </div>
      )}

      {/* Timezone */}
      <div className="space-y-1.5">
        <label className={fieldLabel}>Timezone</label>
        <Select value={timezone} onChange={(v) => emit(draft, v)} aria-label="Timezone">
          {zones.map((z) => (
            <option key={z} value={z}>
              {z}
            </option>
          ))}
        </Select>
      </div>

      {/* Inline error (save-time or live) */}
      {(cronError || liveInvalid) && (
        <p className="text-[11px] text-destructive">⚠ {cronError || 'That doesn’t look like a valid cron expression.'}</p>
      )}

      {/* Human preview */}
      <div className="px-3 py-2 rounded-lg bg-primary/5 border border-primary/20">
        <p className="text-xs text-primary/70">
          <span className="text-primary font-medium">Preview: </span>
          {liveInvalid ? 'Enter a valid cron expression to preview.' : describeCron(cron, timezone)}
        </p>
      </div>
    </div>
  );
};
