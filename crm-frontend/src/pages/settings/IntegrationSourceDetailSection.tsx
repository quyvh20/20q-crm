import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, Inbox, KeyRound, Trash2 } from 'lucide-react';
import {
  Badge,
  Button,
  EmptyState,
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
import { useConfirm } from '../../components/common/ConfirmDialog';
import SecretReveal from '../../components/settings/SecretReveal';
import { DocumentTitle } from '../../lib/useDocumentTitle';
import {
  useDeleteSource,
  useLeadSource,
  useRotateKey,
  useSourceEvents,
  useUpdateSource,
} from '../../features/integrations/queries';
import {
  UPDATE_POLICY_LABELS,
  type EventStatus,
  type IntegrationEvent,
} from '../../features/integrations/types';

// The detail page for one lead source: its config, and the delivery log that
// answers "what happened to the lead John submitted on Tuesday".

const EVENT_VARIANT: Record<EventStatus, 'success' | 'secondary' | 'destructive' | 'warning' | 'outline'> = {
  processed: 'success',
  test: 'outline',
  duplicate: 'secondary',
  pending: 'secondary',
  processing: 'secondary',
  failed: 'destructive',
  quarantined: 'warning',
};

export default function IntegrationSourceDetailSection() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { confirm, dialog } = useConfirm();

  const { data: source, isLoading, error } = useLeadSource(id);
  const { data: events, isLoading: eventsLoading } = useSourceEvents(id);
  const updateSource = useUpdateSource();
  const rotateKey = useRotateKey();
  const deleteSource = useDeleteSource();

  const [actionError, setActionError] = useState('');
  const [newKey, setNewKey] = useState<string | null>(null);
  const [inspecting, setInspecting] = useState<IntegrationEvent | null>(null);

  const handleRotate = async () => {
    if (!id || !source) return;
    const ok = await confirm({
      title: 'Rotate this key?',
      body: `The current key stops working immediately. Anything still using it — ${source.name} — will start failing until you paste in the new one.`,
      confirmLabel: 'Rotate key',
    });
    if (!ok) return;
    setActionError('');
    try {
      const { plaintext_key } = await rotateKey.mutateAsync(id);
      setNewKey(plaintext_key);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to rotate the key');
    }
  };

  const handleToggle = async () => {
    if (!id || !source) return;
    setActionError('');
    const next = source.status === 'active' ? 'disabled' : 'active';
    try {
      await updateSource.mutateAsync({ id, input: { status: next } });
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to update the source');
    }
  };

  const handleDelete = async () => {
    if (!id || !source) return;
    // Warn when the source is live: the external side (a Make scenario, a website
    // form) has no idea we deleted this and will just start failing.
    const recentlyUsed = Boolean(source.last_used_at);
    const ok = await confirm({
      title: `Delete ${source.name}?`,
      body: recentlyUsed
        ? 'This key has been receiving leads. Deleting it stops them immediately, and whatever is sending them will start getting errors. The delivery log is kept.'
        : 'The key stops working immediately. The delivery log is kept.',
      confirmLabel: 'Delete source',
    });
    if (!ok) return;
    setActionError('');
    try {
      await deleteSource.mutateAsync(id);
      navigate('/settings/integrations');
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to delete the source');
    }
  };

  // The tab title is this page's own job — the settings layout deliberately writes
  // none for nested paths, because a parent's effect runs after its children's and
  // would overwrite it. Title from the LOADED source, and null (not a placeholder)
  // while loading, so the tab never flashes a wrong name.
  const title = source ? `${source.name} · Integrations` : null;

  return (
    <div className="space-y-5 max-w-3xl">
      <DocumentTitle title={title} />

      <Link
        to="/settings/integrations"
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="w-4 h-4" />
        Integrations
      </Link>

      {error ? (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error instanceof Error ? error.message : 'Failed to load the lead source'}
        </div>
      ) : isLoading || !source ? (
        <SpinnerBlock />
      ) : (
        <>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h2 className="text-lg font-semibold text-foreground">{source.name}</h2>
              <div className="flex items-center gap-2 mt-1">
                <Badge variant={source.status === 'active' ? 'success' : 'secondary'}>
                  {source.status}
                </Badge>
                <code className="font-mono text-xs text-muted-foreground">
                  {source.token_prefix}…
                </code>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={handleRotate} disabled={rotateKey.isPending}>
                <KeyRound />
                {rotateKey.isPending ? 'Rotating…' : 'Rotate key'}
              </Button>
              <Button variant="outline" size="sm" onClick={handleToggle} disabled={updateSource.isPending}>
                {source.status === 'active' ? 'Disable' : 'Enable'}
              </Button>
              <Button variant="destructive" size="sm" onClick={handleDelete} disabled={deleteSource.isPending}>
                <Trash2 />
                Delete
              </Button>
            </div>
          </div>

          {newKey && (
            <SecretReveal
              title="Your new capture key"
              description="The old key has stopped working. Copy this and paste it into whatever sends you leads."
              value={newKey}
              onDone={() => setNewKey(null)}
            />
          )}

          {actionError && (
            <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              {actionError}
            </div>
          )}

          <div className="rounded-xl border border-border p-4">
            <dl className="grid gap-3 sm:grid-cols-2 text-sm">
              <div>
                <dt className="text-xs text-muted-foreground">When a lead matches an existing contact</dt>
                <dd className="text-foreground mt-0.5">{UPDATE_POLICY_LABELS[source.update_policy]}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Leads become</dt>
                <dd className="text-foreground mt-0.5 capitalize">{source.target_slug}s</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Matched on</dt>
                <dd className="text-foreground mt-0.5">{source.match_fields.join(', ')}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Daily limit</dt>
                <dd className="text-foreground mt-0.5">
                  {source.daily_cap > 0 ? `${source.daily_cap} new contacts/day` : 'None'}
                </dd>
              </div>
            </dl>
          </div>

          <div>
            <h3 className="text-sm font-medium text-foreground">Recent deliveries</h3>
            <p className="text-xs text-muted-foreground mt-0.5">
              Every lead this source sent, and what became of it.
            </p>
          </div>

          {eventsLoading ? (
            <SpinnerBlock />
          ) : !events || events.length === 0 ? (
            <EmptyState
              icon={Inbox}
              title="No deliveries yet"
              description="Once something sends a lead to this source's URL, it shows up here — including anything that was skipped."
            />
          ) : (
            <TableShell>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>When</TableHead>
                    <TableHead>Result</TableHead>
                    <TableHead>Contact</TableHead>
                    <TableHead>Skipped fields</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {events.map((ev) => {
                    const skipped = Object.keys(ev.quarantined_fields ?? {});
                    return (
                      <TableRow key={ev.id}>
                        <TableCell className="text-muted-foreground whitespace-nowrap">
                          {new Date(ev.created_at).toLocaleString()}
                        </TableCell>
                        <TableCell>
                          <Badge variant={EVENT_VARIANT[ev.status] ?? 'secondary'}>
                            {ev.outcome || ev.status}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          {ev.result_record_id ? (
                            <Link
                              to={`/contacts/${ev.result_record_id}`}
                              className="text-primary hover:underline"
                            >
                              View contact
                            </Link>
                          ) : (
                            <span className="text-muted-foreground">—</span>
                          )}
                        </TableCell>
                        <TableCell>
                          {skipped.length > 0 ? (
                            <span className="text-amber-700 dark:text-amber-400 text-xs">
                              {skipped.join(', ')}
                            </span>
                          ) : (
                            <span className="text-muted-foreground">—</span>
                          )}
                        </TableCell>
                        <TableCell>
                          <Button variant="ghost" size="sm" onClick={() => setInspecting(ev)}>
                            Details
                          </Button>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </TableShell>
          )}
        </>
      )}

      <Modal
        open={Boolean(inspecting)}
        onClose={() => setInspecting(null)}
        title="Delivery details"
        description={inspecting ? new Date(inspecting.created_at).toLocaleString() : undefined}
        size="lg"
      >
        {inspecting && (
          <div className="space-y-4">
            {inspecting.error && (
              <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                {inspecting.error}
              </div>
            )}
            {Object.keys(inspecting.quarantined_fields ?? {}).length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-foreground">Recorded but not saved</p>
                <p className="text-xs text-muted-foreground">
                  These aren't contact fields, so they were kept here rather than written to the
                  record.
                </p>
                <pre className="w-full overflow-x-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs">
                  {JSON.stringify(inspecting.quarantined_fields, null, 2)}
                </pre>
              </div>
            )}
            <div className="space-y-1.5">
              <p className="text-xs font-medium text-foreground">What was sent</p>
              <pre className="w-full overflow-x-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs">
                {JSON.stringify(inspecting.raw_payload, null, 2)}
              </pre>
            </div>
          </div>
        )}
      </Modal>

      {/* Must be in the tree: without it confirm() never settles and the handler
          hangs forever with no error. */}
      {dialog}
    </div>
  );
}
