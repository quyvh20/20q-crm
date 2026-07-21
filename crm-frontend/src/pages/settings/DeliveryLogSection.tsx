import { useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { AlertTriangle, RefreshCw } from 'lucide-react';
import {
  Badge,
  Button,
  EmptyState,
  PageHeader,
  Select,
  SpinnerBlock,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  TableShell,
} from '@/components/ui';
import Modal from '../../components/common/Modal';
import { DocumentTitle } from '../../lib/useDocumentTitle';
import { relativeTime } from '../../lib/relativeTime';
import { useEventLog, useRetryEvent } from '../../features/integrations/queries';
import { RetryRefusedError } from '../../features/integrations/api';
import type { EventStatus, IntegrationEvent } from '../../features/integrations/types';

// The org-wide delivery ledger.
//
// It exists because the per-source log cannot answer the question an operator
// actually arrives with. A provider delivery is inserted before a source is
// resolved, so every failure that happens first — a dead token, an unfetched lead, a
// form nobody enabled yet — has no source_id, forever, and is invisible to a
// per-source view. The same is true of a deleted source's whole history. This page
// scopes on the workspace and treats source and connection as filters.

const EVENT_VARIANT: Record<EventStatus, 'success' | 'secondary' | 'destructive' | 'warning' | 'outline'> = {
  processed: 'success',
  test: 'outline',
  duplicate: 'secondary',
  pending: 'secondary',
  processing: 'secondary',
  failed: 'destructive',
  quarantined: 'warning',
};

// The statuses a human can filter on.
//
// `duplicate` is missing on purpose and its absence is the point: the status is
// declared and badged, but nothing in the pipeline ever writes it, so offering it
// would be a filter that always answers empty — which teaches an operator the log is
// broken at the moment they are depending on it.
const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: '', label: 'All results' },
  { value: 'processed', label: 'Written' },
  { value: 'failed', label: 'Failed' },
  { value: 'quarantined', label: 'Skipped' },
  { value: 'pending', label: 'Waiting' },
  { value: 'processing', label: 'In progress' },
  { value: 'test', label: 'Test' },
];

/**
 * Why a delivery cannot be retried, in the admin's language.
 *
 * A copy table keyed on the server's machine-readable reason, never the server's own
 * sentence — the same posture the connect-error banner takes. The server decides
 * WHAT is true; this file decides how to say it.
 */
const RETRY_REASON_COPY: Record<string, string> = {
  already_written:
    'This delivery already created a record, so there is nothing to retry.',
  incomplete:
    'This delivery was marked finished but no record was ever linked to it. Retrying cannot fix that — open the payload below and re-send the lead through your integration.',
  in_flight: 'This delivery is queued — it will be attempted again shortly.',
  already_done: 'This delivery finished. There is nothing to retry.',
  sync_channel:
    'This lead was posted to us rather than fetched, so we have nothing to fetch again. Re-send it from the system that sent it, using the same Idempotency-Key, and it will be picked up in place.',
  write_failure:
    'This delivery already reached your CRM once and failed while writing, and it was retried automatically at the time. Retrying by hand would repeat the same attempt.',
  form_closed:
    'This lead’s form is not enabled, so retrying now would fail again. Enable the form first.',
};

function recordPath(slug: string | undefined, id: string): string {
  switch (slug) {
    case 'deal':
      return `/deals/${id}`;
    case 'company':
      return `/companies/${id}`;
    default:
      return `/contacts/${id}`;
  }
}

export default function DeliveryLogSection() {
  const [searchParams, setSearchParams] = useSearchParams();
  // Filter state lives in the URL, matching WorkflowList: an error banner that
  // deep-links to "the failed deliveries for this connection" has to be a link, and
  // a reload must not silently widen the question back to everything.
  const status = searchParams.get('status') ?? '';
  const sourceId = searchParams.get('source_id') ?? '';
  const connectionId = searchParams.get('connection_id') ?? '';
  const unresolved = searchParams.get('unresolved') === '1';

  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [inspectingId, setInspectingId] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');

  const filters = {
    status: status ? ([status] as EventStatus[]) : undefined,
    source_id: sourceId || undefined,
    connection_id: connectionId || undefined,
    unresolved: unresolved || undefined,
    cursor,
  };
  const { data, isLoading } = useEventLog(filters);
  const retry = useRetryEvent();

  const events = data?.events ?? [];
  const sources = data?.sources ?? {};
  const filtered = Boolean(status || sourceId || connectionId || unresolved);

  // Derived from the query data, never a captured snapshot: the modal holds the row
  // the admin is acting on, so a stale copy would keep showing a live Retry button
  // after the retry succeeded — and they cannot see the table underneath to know
  // better.
  const inspecting = events.find(e => e.id === inspectingId) ?? null;

  const setParam = (key: string, value: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(key, value);
    else next.delete(key);
    setSearchParams(next, { replace: true });
    setCursor(undefined); // a new question starts at the first page
  };

  const handleRetry = async (eventId: string) => {
    setActionError('');
    try {
      await retry.mutateAsync(eventId);
    } catch (err: any) {
      // The server's 409 reason maps through our own copy table; the raw message is
      // the fallback, not the default.
      const reason = err instanceof RetryRefusedError ? err.reason : undefined;
      setActionError((reason && RETRY_REASON_COPY[reason]) || err?.message || 'Could not queue the retry');
    }
  };

  return (
    <div>
      <DocumentTitle title="Delivery log" />
      <PageHeader
        title="Delivery log"
        description="Every lead any integration has sent this workspace, including the ones that never became a record."
      />

      {actionError && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{actionError}</span>
        </div>
      )}

      <div className="mb-3 flex flex-wrap items-center gap-2">
        <Select
          aria-label="Filter by result"
          value={status}
          onChange={e => setParam('status', e.target.value)}
          className="w-auto"
        >
          {STATUS_OPTIONS.map(o => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </Select>
        <label className="flex items-center gap-2 text-sm text-muted-foreground">
          <input
            type="checkbox"
            checked={unresolved}
            onChange={e => setParam('unresolved', e.target.checked ? '1' : '')}
          />
          Only deliveries that made no record
        </label>
        {filtered && (
          <Button variant="ghost" size="sm" onClick={() => { setSearchParams({}, { replace: true }); setCursor(undefined); }}>
            Clear filters
          </Button>
        )}
      </div>

      {isLoading ? (
        <SpinnerBlock />
      ) : events.length === 0 ? (
        // Two different empty states, because one of them would be a lie. The shipped
        // copy asserts nothing has ever been sent — true for a quiet workspace, false
        // and misleading on a filtered view of a busy one.
        filtered ? (
          <EmptyState
            title="No deliveries match these filters"
            description="Nothing here fits what you asked for. Widen the filters to see more."
            action={
              <Button variant="outline" size="sm" onClick={() => { setSearchParams({}, { replace: true }); setCursor(undefined); }}>
                Clear filters
              </Button>
            }
          />
        ) : (
          <EmptyState
            title="No deliveries yet"
            description="Once an integration sends a lead to this workspace, it shows up here — including anything that was skipped."
          />
        )
      ) : (
        <>
          <TableShell>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>When</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead>Result</TableHead>
                  <TableHead>Record</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map(ev => {
                  const label = ev.source_id ? sources[ev.source_id] : undefined;
                  return (
                    <TableRow key={ev.id}>
                      <TableCell className="whitespace-nowrap text-muted-foreground">
                        {relativeTime(ev.created_at)}
                      </TableCell>
                      <TableCell>
                        {label ? (
                          <span>
                            {label.name}
                            {label.deleted && (
                              <span className="ml-1 text-xs text-muted-foreground">(deleted)</span>
                            )}
                          </span>
                        ) : (
                          // Not an error and not a gap: the delivery failed before we
                          // could tell which form it belonged to. Saying so is the
                          // whole reason this page can show the row at all.
                          <span className="text-xs text-muted-foreground">
                            before a source was matched
                          </span>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant={EVENT_VARIANT[ev.status] ?? 'secondary'}>{ev.status}</Badge>
                      </TableCell>
                      <TableCell>
                        {ev.result_record_id ? (
                          <Link
                            className="text-primary hover:underline"
                            to={recordPath(ev.result_slug, ev.result_record_id)}
                          >
                            View
                          </Link>
                        ) : (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1">
                          {ev.retry?.mode === 'refetch' && (
                            <Button
                              variant="ghost"
                              size="sm"
                              disabled={retry.isPending}
                              onClick={() => handleRetry(ev.id)}
                            >
                              <RefreshCw />
                              Fetch again
                            </Button>
                          )}
                          <Button variant="ghost" size="sm" onClick={() => setInspectingId(ev.id)}>
                            Details
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </TableShell>

          {data?.next_cursor && (
            <div className="mt-3">
              <Button variant="outline" size="sm" onClick={() => setCursor(data.next_cursor)}>
                Load older
              </Button>
            </div>
          )}
        </>
      )}

      <Modal
        open={Boolean(inspecting)}
        onClose={() => setInspectingId(null)}
        title="Delivery details"
        size="lg"
      >
        {inspecting && <DeliveryDetails ev={inspecting} onRetry={handleRetry} pending={retry.isPending} />}
      </Modal>
    </div>
  );
}

function DeliveryDetails({
  ev,
  onRetry,
  pending,
}: {
  ev: IntegrationEvent;
  onRetry: (id: string) => void;
  pending: boolean;
}) {
  return (
    <div className="space-y-3 text-sm">
      {ev.note && (
        <div className="rounded-lg border border-border bg-background p-3 text-xs">{ev.note}</div>
      )}
      {ev.error && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-xs text-destructive">
          {ev.error}
        </div>
      )}

      {ev.retry?.mode === 'refetch' ? (
        <Button variant="outline" size="sm" disabled={pending} onClick={() => onRetry(ev.id)}>
          <RefreshCw />
          Fetch this lead again
        </Button>
      ) : (
        // No disabled button. A greyed-out control invites a click and explains
        // nothing; the reason is what the admin came for.
        ev.retry?.reason && (
          <p className="text-xs text-muted-foreground">{RETRY_REASON_COPY[ev.retry.reason]}</p>
        )
      )}

      <div>
        <p className="mb-1 text-xs font-medium text-muted-foreground">What arrived</p>
        {ev.redacted_at ? (
          // An empty payload with no explanation is indistinguishable from a delivery
          // that stored nothing — which is what a bug looks like. Say which it is.
          <p className="rounded-lg border border-border bg-background p-3 text-xs text-muted-foreground">
            The lead this delivery carried was erased {relativeTime(ev.redacted_at)}. The
            delivery is kept so the log still explains what happened; what the person
            supplied is gone. Deliveries that never became a record are erased
            automatically after 90 days.
          </p>
        ) : (
          <pre className="w-full overflow-x-auto rounded-lg bg-muted p-3 text-xs">
            {JSON.stringify(ev.raw_payload, null, 2)}
          </pre>
        )}
      </div>
    </div>
  );
}
