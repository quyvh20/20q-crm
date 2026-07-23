import { useState } from 'react';
import { Download, FlaskConical } from 'lucide-react';
import { Button } from '@/components/ui';
import Modal from '../../components/common/Modal';
import { useBackfill } from '../../features/integrations/connections';
import type { LeadSource } from '../../features/integrations/types';

/**
 * TikTokFormCard is the per-form panel on a tiktok_form source's detail page.
 *
 * It is a sibling of FacebookFormCard rather than a generalization of it, because the
 * two providers' instructions have almost nothing in common: Meta has a Lead Ads
 * Testing Tool and a 90-day window, TikTok has a per-form test lead you must delete
 * before creating another and an import that runs as a background export task.
 */
export default function TikTokFormCard({ source }: { source: LeadSource }) {
  const [open, setOpen] = useState(false);
  const [enroll, setEnroll] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const backfill = useBackfill();

  const run = async () => {
    setError(null);
    try {
      await backfill.mutateAsync({ sourceId: source.id, enroll });
      setOpen(false);
      setNotice('Import started. Past leads will appear in the delivery log below as they arrive.');
    } catch (e) {
      // Show it rather than swallow it — the sibling FacebookFormCard did the same,
      // and a request that failed with no feedback looks like nothing happened.
      setError(e instanceof Error ? e.message : 'Could not start the import.');
    }
  };

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">TikTok Lead Generation</h3>
        <p className="text-xs text-muted-foreground mt-1">
          New leads from this Instant Form arrive automatically — there is no key to paste. To
          verify the connection, create a test lead for this form in TikTok Ads Manager. TikTok
          allows <span className="font-medium">one test lead per form at a time</span>, so delete
          the previous one first. It lands in the delivery log below within a minute.
        </p>
      </div>

      {notice && (
        <div className="rounded-lg border border-emerald-500/40 bg-emerald-500/10 p-3 text-sm text-emerald-700 dark:text-emerald-400">
          {notice}
        </div>
      )}

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}

      <div className="flex items-start gap-3">
        <FlaskConical className="h-4 w-4 text-muted-foreground shrink-0 mt-0.5" />
        <div className="flex-1">
          <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
            <Download />
            Import past leads
          </Button>
          {/* Two things worth saying that are specific to TikTok and would otherwise
              be discovered as bugs: the import is a background export TikTok builds on
              its side, and its file names columns in the FORM's language. */}
          <p className="text-xs text-muted-foreground mt-1">
            TikTok builds the export on its side, so this can take a few minutes before anything
            appears. Leads already received are skipped, so it is safe to run more than once.
          </p>
          <p className="text-xs text-muted-foreground mt-1">
            If your form is not in English, its answers arrive under the form&apos;s own column
            names and will show as unmapped in{' '}
            <span className="font-medium">Field mapping</span> above — map them once and later
            imports follow.
          </p>
        </div>
      </div>

      <Modal open={open} onClose={() => setOpen(false)} title="Import past leads" size="md">
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            This imports this form&apos;s history from TikTok. Contacts you already have are
            matched, not duplicated.
          </p>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={enroll}
              onChange={(e) => setEnroll(e.target.checked)}
            />
            {/* Off by default, and the reason is the whole point: importing months of
                history would otherwise enrol every one of those people into every
                contact_created workflow, and mail all of them. */}
            <span className="text-muted-foreground">
              Also run automations for imported leads. Leave this off unless you mean it — a
              year of history would enrol every one of those contacts into your workflows and
              email them.
            </span>
          </label>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button size="sm" onClick={run} disabled={backfill.isPending}>
              {backfill.isPending ? 'Starting…' : 'Start import'}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
