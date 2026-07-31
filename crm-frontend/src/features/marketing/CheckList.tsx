import React from 'react';
import { AlertCircle, CheckCircle2, Info } from 'lucide-react';

/** One row of a readiness checklist. Shared by the per-campaign pre-send checklist
 *  and the org-level go-live preflight, which the backend deliberately emits in the
 *  same shape (domain.LaunchCheck) so one renderer serves both.
 *
 *  `severity` is optional and absent means BLOCKING — that keeps the campaign
 *  readiness payload byte-identical to what it was before the preflight existed. */
export interface CheckItem {
  key: string;
  label: string;
  ok: boolean;
  detail?: string;
  severity?: string;
}

const WARN = 'warn';

/** Renders a check list. A failing WARNING is visually distinct from a failing
 *  blocker: an operator reading "delivery webhook not configured" next to "no
 *  verified sending domain" needs to know only one of them stops a send. */
export const CheckList: React.FC<{ checks: CheckItem[] }> = ({ checks }) => (
  <ul className="space-y-2">
    {checks.map((c) => {
      const isWarn = c.severity === WARN;
      return (
        <li key={c.key} className="flex items-start gap-2 text-sm">
          {c.ok ? (
            <CheckCircle2 aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
          ) : isWarn ? (
            <Info aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
          ) : (
            <AlertCircle aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          )}
          <span className={c.ok ? 'text-foreground' : 'text-muted-foreground'}>
            {c.label}
            {!c.ok && isWarn ? ' (optional)' : ''}
            {!c.ok && c.detail ? ` — ${c.detail}` : ''}
          </span>
        </li>
      );
    })}
  </ul>
);

export default CheckList;
