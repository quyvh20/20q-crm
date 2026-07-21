import { useState } from 'react';
import { Download, FlaskConical } from 'lucide-react';
import { Button } from '@/components/ui';
import Modal from '../../components/common/Modal';
import { useBackfill } from '../../features/integrations/connections';
import type { LeadSource } from '../../features/integrations/types';

/**
 * FacebookFormCard is the per-form panel on a facebook_form source's detail page:
 * how to test it (Meta's Lead Ads Testing Tool) and how to import its history
 * (backfill, suppressed by default). New leads flow in automatically via the
 * connection's webhook — there is no key to paste.
 */
export default function FacebookFormCard({ source }: { source: LeadSource }) {
  const backfill = useBackfill();
  const [open, setOpen] = useState(false);
  const [enroll, setEnroll] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');

  const run = async () => {
    setError('');
    setNotice('');
    try {
      await backfill.mutateAsync({ sourceId: source.id, enroll });
      setOpen(false);
      setNotice('Import started. Past leads will appear in the delivery log below as they arrive.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not start the import');
    }
  };

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">Facebook &amp; Instagram Lead Ads</h3>
        <p className="text-xs text-muted-foreground mt-1">
          New leads from this form arrive automatically — there is no key to paste. To verify the
          connection, open Meta&apos;s{' '}
          <span className="font-medium">Lead Ads Testing Tool</span>, pick this page and form, and
          send a test lead. It lands in the delivery log below within a minute.
        </p>
        {/* Instagram placements need no separate setup and no separate connection — the
            same form serves both — so the only honest thing to add is where to LOOK,
            since the two are otherwise indistinguishable in the log. */}
        <p className="text-xs text-muted-foreground mt-1">
          Instagram placements on the same ad run through this form too. Open a delivery below to
          see which placement it came from — <span className="font-mono">platform</span> reads{' '}
          <span className="font-mono">ig</span> for Instagram and <span className="font-mono">fb</span>{' '}
          for Facebook.
        </p>
      </div>

      {notice && (
        <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3 text-sm text-emerald-700 dark:text-emerald-400">
          {notice}
        </div>
      )}

      <div className="flex items-start gap-3">
        <FlaskConical className="h-4 w-4 text-muted-foreground shrink-0 mt-0.5" />
        <div className="flex-1">
          <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
            <Download />
            Import past leads
          </Button>
          <p className="text-xs text-muted-foreground mt-1">
            Pull up to 90 days of this form&apos;s history (Meta&apos;s limit). Leads already
            received are skipped, so this is safe to run more than once.
          </p>
        </div>
      </div>

      <Modal open={open} onClose={() => setOpen(false)} title="Import past leads" size="md">
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            This imports up to 90 days of leads from this form. Duplicates are skipped.
          </p>
          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="checkbox"
              className="mt-1"
              checked={enroll}
              onChange={(e) => setEnroll(e.target.checked)}
            />
            <span className="text-sm text-foreground">
              Also run automations for imported leads
              <span className="block text-xs text-muted-foreground">
                Off by default — leave it off unless you want workflows (welcome emails, etc.) to
                fire for months of historical leads.
              </span>
            </span>
          </label>
          {error && (
            <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button size="sm" onClick={run} disabled={backfill.isPending}>
              {backfill.isPending ? 'Starting…' : 'Import'}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
