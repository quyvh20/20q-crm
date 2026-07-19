import { useEffect, useState } from 'react';
import { Button, Input } from '@/components/ui';
import { useUpdateSource } from '../../features/integrations/queries';
import type { LeadSource } from '../../features/integrations/types';

// The two settings that decide how much a single source can do to a workspace in one
// day, and whether a bulk recovery run wakes anybody up.
//
// Both exist server-side already; both would be invisible without this card. That is
// the mistake this feature has now made twice — default_owner_id and daily_cap both
// shipped as backend fields no screen ever wrote — so the controls ship with the
// behaviour rather than after it.

interface Props {
  source: LeadSource;
}

export default function DeliveryLimitsCard({ source }: Props) {
  const updateSource = useUpdateSource();
  const [cap, setCap] = useState(String(source.daily_cap ?? 0));
  const [error, setError] = useState('');

  useEffect(() => {
    setCap(String(source.daily_cap ?? 0));
  }, [source.id, source.daily_cap]);

  const parsed = Number.parseInt(cap, 10);
  const capValid = Number.isFinite(parsed) && parsed >= 0;
  const capDirty = capValid && parsed !== source.daily_cap;

  const save = async (input: Parameters<typeof updateSource.mutateAsync>[0]['input']) => {
    setError('');
    try {
      await updateSource.mutateAsync({ id: source.id, input });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    }
  };

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">Delivery limits</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          What this source is allowed to do in a day, and how bulk deliveries behave.
        </p>
      </div>

      <div className="space-y-1.5">
        <label className="text-xs text-muted-foreground" htmlFor="daily-cap">
          New contacts per day
        </label>
        <div className="flex items-center gap-2">
          <Input
            id="daily-cap"
            type="number"
            min={0}
            value={cap}
            onChange={(e) => setCap(e.target.value)}
            className="w-40"
          />
          {capDirty && (
            <Button size="sm" onClick={() => void save({ daily_cap: parsed })} disabled={updateSource.isPending}>
              {updateSource.isPending ? 'Saving…' : 'Save'}
            </Button>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          {parsed === 0
            ? 'Unlimited. If this key ever leaks, nothing bounds how many records, workflow runs and emails it can produce in a day.'
            : `Stops at ${parsed} new contacts a day. Updates to existing contacts don't count.`}
        </p>
        {!capValid && (
          <p className="text-xs text-destructive">Enter a whole number (0 means unlimited).</p>
        )}
      </div>

      <div className="border-t border-border pt-4">
        <label className="flex items-start gap-2.5 cursor-pointer">
          <input
            type="checkbox"
            className="mt-0.5"
            checked={source.batch_enroll_automation}
            onChange={(e) => void save({ batch_enroll_automation: e.target.checked })}
            disabled={updateSource.isPending}
          />
          <span>
            <span className="text-sm text-foreground">Let bulk deliveries trigger workflows</span>
            <span className="block text-xs text-muted-foreground mt-0.5">
              Off by default. Leads sent to the batch endpoint — usually a recovery run after an
              outage — are recorded and assigned normally, but do not start workflows, so importing
              100 old leads cannot send 100 welcome emails at once. Leads sent one at a time are
              unaffected either way.
            </span>
          </span>
        </label>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}
    </div>
  );
}
