import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, Check, FlaskConical, Inbox, KeyRound, Minus, Trash2 } from 'lucide-react';
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
import FieldMappingTable from './FieldMappingTable';
import OwnerRoutingCard from './OwnerRoutingCard';
import DeliveryLimitsCard from './DeliveryLimitsCard';
import LeadDealCard from './LeadDealCard';
import GoogleAdsSetupCard from './GoogleAdsSetupCard';
import FormEmbedSetupCard from './FormEmbedSetupCard';
import FacebookFormCard from './FacebookFormCard';
import { DocumentTitle } from '../../lib/useDocumentTitle';
import IngestSparkline from './IngestSparkline';
import { relativeTime } from '../../lib/relativeTime';
import {
  useDeleteSource,
  useLeadSource,
  useRotateKey,
  useSendTestLead,
  useSourceStats,
  useSourceEvents,
  useUpdateSource,
} from '../../features/integrations/queries';
import {
  UPDATE_POLICY_LABELS,
  isKeylessKind,
  isManagedKind,
  kindLabel,
  type EventStatus,
  type IntegrationEvent,
  type LeadSourceStatus,
  type TestLeadResult,
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

// What the button actually exercised, enumerated from the real call graph — and what
// it did not. Both lists are rendered with equal weight on purpose.
//
// The button is an in-process call: it skips the network, the capture key, and every
// gate that hangs off them. That bypass is what makes it safe to hand an admin (no
// credential in the browser, no public write path exercised from a session), so it is
// not a flaw to engineer away — it is a boundary to publish. The dishonesty risk lives
// entirely in the copy, which is why the second column is not decoration.
const TEST_PROVED = [
  'Your field mapping — the payload was keyed the way your provider keys it',
  'Which fields are skipped, and why',
  'Matching on email, and your update policy for an existing contact',
  'Owner assignment and attribution (lead source, UTMs)',
  'Who the next real lead goes to — a test does not consume anyone’s turn in the rotation',
  'The contact write itself, and the delivery-log entry',
];

const TEST_NOT_PROVED = [
  'Your capture key — this ran in-process, so it cannot tell you whether the outside world can reach you',
  'The network path from your provider, and the rate and daily limits',
  'Phone matching — a test lead never sends a phone number',
];

/**
 * TestLeadPanel is the result of one click: what happened, and what it did not prove.
 *
 * liveStatus is the source's CURRENT status, not result.source_status (a snapshot
 * taken at test time). The "rejected right now" warning makes a present-tense claim
 * about live state, so it must read live state — otherwise an admin who tests a
 * disabled source and then clicks Enable is left with the badge reading "active" and
 * this box still insisting the source is disabled.
 */
function TestLeadPanel({
  result,
  liveStatus,
  onDismiss,
}: {
  result: TestLeadResult;
  liveStatus: LeadSourceStatus;
  onDismiss: () => void;
}) {
  return (
    <div className="rounded-xl border border-border bg-muted/30 p-4 space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <p className="text-sm font-medium text-foreground">
            {/* "matched" not "updated": create_only (and a repeat click that finds
                nothing to fill) writes nothing yet still reports outcome=updated, so
                claiming an update would contradict the policy shown below. */}
            Test lead {result.outcome === 'created' ? 'created a contact' : 'matched your existing test contact'}
          </p>
          <p className="text-xs text-muted-foreground mt-0.5">
            It went through the same pipeline your real leads take. No workflows were triggered.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Link
            to={`/contacts/${result.record_id}`}
            className="text-sm text-primary hover:underline whitespace-nowrap"
          >
            View contact
          </Link>
          <Button variant="ghost" size="sm" onClick={onDismiss}>
            Dismiss
          </Button>
        </div>
      </div>

      {liveStatus === 'disabled' && (
        // The test does not use the capture key, so it succeeds on a source that is
        // rejecting every real lead right now. Saying so is the whole price of
        // letting an admin test before enabling.
        //
        // Gated on `disabled` alone, not on `!== 'active'`. `error` is the other
        // non-active status and it rejects nothing (see below), so the broader gate
        // put a false outage warning on a source that was working.
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-800 dark:text-amber-300">
          This source is <strong>disabled</strong>, so real leads sent to it are being
          rejected right now. The test ran anyway, because it does not use your capture key.
        </div>
      )}

      {liveStatus === 'error' && (
        // `error` is a self-healing BADGE, not a gate: the backend's IsLive() is
        // `status != 'disabled'`, so a flagged source is still accepting deliveries
        // and still writing every one of them to the log. The admin's actual next
        // move is to read the failures, not to re-enable anything.
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-800 dark:text-amber-300">
          This source is flagged <strong>error</strong> because its recent deliveries failed.
          It is <strong>still accepting leads</strong> and recording every one of them, and the
          flag clears itself as soon as one succeeds. The delivery log below says what went
          wrong.
        </div>
      )}

      {result.note && (
        <div className="rounded-lg border border-border bg-background p-3 text-xs text-foreground">
          {result.note}
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <p className="text-xs font-medium text-foreground">What this proved</p>
          <ul className="mt-1.5 space-y-1">
            {TEST_PROVED.map((item) => (
              <li key={item} className="flex gap-1.5 text-xs text-muted-foreground">
                <Check className="w-3 h-3 mt-0.5 shrink-0 text-emerald-600 dark:text-emerald-400" />
                <span>{item}</span>
              </li>
            ))}
          </ul>
        </div>
        <div>
          <p className="text-xs font-medium text-foreground">What this did not prove</p>
          <ul className="mt-1.5 space-y-1">
            {TEST_NOT_PROVED.map((item) => (
              <li key={item} className="flex gap-1.5 text-xs text-muted-foreground">
                <Minus className="w-3 h-3 mt-0.5 shrink-0" />
                <span>{item}</span>
              </li>
            ))}
            {(result.uncovered ?? []).map((item) => (
              <li key={item} className="flex gap-1.5 text-xs text-muted-foreground">
                <Minus className="w-3 h-3 mt-0.5 shrink-0" />
                <span>{item} — not covered by this test</span>
              </li>
            ))}
          </ul>
        </div>
      </div>

      {(result.quarantined ?? []).length > 0 && (
        <p className="text-xs text-amber-700 dark:text-amber-400">
          Recorded but not saved: {(result.quarantined ?? []).join(', ')}
        </p>
      )}

      <p className="text-xs text-muted-foreground border-t border-border pt-3">
        The contact this made is a <strong>real contact</strong> in your CRM — a workflow that
        searches your contacts can still find it. Delete it from its contact page when you're done.
      </p>
    </div>
  );
}


/**
 * ConsentBlock shows the consent a delivery carried — and, just as importantly, what
 * that record does NOT do.
 *
 * Nothing in this app consults a stored consent value before sending an email or
 * enrolling a workflow. Recording a legal basis while acting on none of it is a
 * compliance illusion unless the product says so plainly, so the disclosure is part
 * of the feature rather than a caption on it. A customer who believes they have
 * opt-out enforcement they do not have is the failure this copy exists to prevent.
 */
function ConsentBlock({ consent }: { consent: Record<string, unknown> }) {
  const meta = (consent._crm ?? {}) as Record<string, unknown>;
  const redacted = meta.redacted === true;

  return (
    <div className="space-y-1.5">
      <p className="text-xs font-medium text-foreground">Consent as reported by the source</p>
      {redacted ? (
        <p className="text-xs text-muted-foreground">
          This was erased when the contact was deleted. The delivery is kept so the log still
          explains what happened; what the person supplied is gone.
        </p>
      ) : (
        <>
          <pre className="w-full overflow-x-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs">
            {JSON.stringify(consent, null, 2)}
          </pre>
          <p className="text-xs text-amber-700 dark:text-amber-400">
            Recorded only — nothing in this CRM checks it before sending. It is evidence of what
            the source told us, not an opt-out list, and no email or workflow is filtered by it.
          </p>
          {/* "There is no automatic deletion yet" was true when consent shipped and is
              not any more: deliveries that never became a record are erased after 90
              days. This envelope is not one of those — consent is only ever stored on a
              delivery that DID produce a record — so the contact-keyed promise still
              holds for it, and the sentence now says which rule applies rather than
              denying that any rule exists. */}
          <p className="text-xs text-muted-foreground">
            Kept with this delivery until you delete the contact, which erases it. Deliveries
            that never became a record are erased automatically after 90 days; this one did,
            so it is kept until you say otherwise.
          </p>
        </>
      )}
    </div>
  );
}

/**
 * eventLabel renders a delivery's result.
 *
 * A test delivery carries BOTH status='test' and outcome='created', and the outcome
 * alone would render "created" — leaving a made-up lead indistinguishable from a real
 * one in the log, with only the badge colour differing. The status has to lead.
 */
function eventLabel(ev: IntegrationEvent): string {
  if (ev.status === 'test') return ev.outcome ? `test · ${ev.outcome}` : 'test';
  return ev.outcome || ev.status;
}

/**
 * Where a delivery's record lives.
 *
 * Routed through result_slug rather than a hardcoded /contacts/ path. The slug has
 * always been on the ledger row for exactly this, and hardcoding it meant the link
 * would silently point at a contact page for anything that was not a contact —
 * already wrong for any other target slug, not only for deals.
 */
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

function recordLabel(slug: string | undefined): string {
  switch (slug) {
    case 'deal':
      return 'View deal';
    case 'company':
      return 'View company';
    default:
      return 'View contact';
  }
}

export default function IntegrationSourceDetailSection() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { confirm, dialog } = useConfirm();

  const { data: source, isLoading, error } = useLeadSource(id);
  const { data: events, isLoading: eventsLoading } = useSourceEvents(id);
  const stats = useSourceStats(id);
  const updateSource = useUpdateSource();
  const rotateKey = useRotateKey();
  const deleteSource = useDeleteSource();

  const [actionError, setActionError] = useState('');
  const [newKey, setNewKey] = useState<string | null>(null);
  const [inspecting, setInspecting] = useState<IntegrationEvent | null>(null);
  const [testResult, setTestResult] = useState<TestLeadResult | null>(null);
  const [testError, setTestError] = useState('');

  const sendTestLead = useSendTestLead();

  const handleTestLead = async () => {
    if (!id) return;
    setTestError('');
    setTestResult(null);
    try {
      setTestResult(await sendTestLead.mutateAsync(id));
    } catch (err) {
      setTestError(err instanceof Error ? err.message : 'Failed to send the test lead');
    }
  };

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
    // `disabled` is the only status this button turns ON — keyed off `disabled`
    // rather than `!== active` because `error` is a live source, and treating it as
    // off made this a hidden "clear the failure counter" button: PATCHing status to
    // active runs SetSourceStatus, which also zeroes consecutive_failures. An admin
    // who just read the error banner would have silently erased the evidence.
    const next = source.status === 'disabled' ? 'active' : 'disabled';
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
    // facebook_form is keyless and webhook-fed: deleting it does not break a "sender"
    // (Facebook keeps posting to the connection; those leads are simply dropped), so
    // its copy must not talk about a key or the sender getting errors.
    const body =
      isKeylessKind(source.kind)
        ? // A keyless source has no key to stop working, and the provider is not told:
          // it keeps posting to the connection and those leads are simply dropped. Keyed
          // on the PREDICATE rather than the facebook_form literal, which is what left
          // a TikTok source promising key behaviour it does not have.
          'This stops importing leads from this form. The provider is not notified — its leads for this form will simply stop being recorded. The delivery log is kept.'
        : recentlyUsed
          ? 'This key has been receiving leads. Deleting it stops them immediately, and whatever is sending them will start getting errors. The delivery log is kept.'
          : 'The key stops working immediately. The delivery log is kept.';
    const ok = await confirm({
      title: `Delete ${source.name}?`,
      body,
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
                {/* `error` renders destructive HERE, on the page where the admin
                    acts — a gray badge on a broken source is how nobody notices. */}
                <Badge
                  variant={
                    source.status === 'active'
                      ? 'success'
                      : source.status === 'error'
                        ? 'destructive'
                        : 'secondary'
                  }
                >
                  {source.status}
                </Badge>
                <Badge variant="secondary">{kindLabel(source.kind)}</Badge>
                {/* A keyless kind has no bearer key to show. */}
                {!isKeylessKind(source.kind) && (
                  <code className="font-mono text-xs text-muted-foreground">
                    {source.token_prefix}…
                  </code>
                )}
              </div>
              {/* last_used_at was already fetched, and already read here — but only
                  as a boolean, to pick the delete confirm's wording. It is the one
                  fact that answers "is this actually receiving anything" without
                  scrolling to the log, and next to an `error` badge it is what
                  separates a source that broke an hour ago from one that never
                  worked. Absent renders as a statement, not a blank. */}
              <p className="text-xs text-muted-foreground mt-1.5">
                {source.last_used_at
                  ? `Last lead received ${relativeTime(source.last_used_at)}`
                  : 'No leads received yet'}
              </p>
            </div>
            <div className="flex items-center gap-2">
              {/* google_ads has no button: Google's own "Send test data" IS the
                  test path, and it arrives in the log below badged `test`. */}
              {source.kind === 'api' && (
                <Button size="sm" onClick={handleTestLead} disabled={sendTestLead.isPending}>
                  <FlaskConical />
                  {sendTestLead.isPending ? 'Sending…' : 'Send test lead'}
                </Button>
              )}
              {/* A keyless kind has no bearer key to rotate: facebook_form is rotated
                  by reconnecting the account, webhook_inbound in the workflow builder.
                  The server refuses both, so a button here would only produce an error. */}
              {!isKeylessKind(source.kind) && (
                <Button variant="outline" size="sm" onClick={handleRotate} disabled={rotateKey.isPending}>
                  <KeyRound />
                  {rotateKey.isPending ? 'Rotating…' : 'Rotate key'}
                </Button>
              )}
              {/* A managed kind is a VIEW onto an endpoint that lives elsewhere, so
                  neither control would do what it says: disabling refuses no delivery
                  (the endpoint never consults this row) and deleting is undone by the
                  next one. The server refuses both; showing them would just surface an
                  error message where a button used to be. */}
              {!isManagedKind(source.kind) && (
                <>
                  <Button variant="outline" size="sm" onClick={handleToggle} disabled={updateSource.isPending}>
                    {/* Mirrors handleToggle: an `error` source is already live, so
                        offering "Enable" on it would be a lie about the current state. */}
                    {source.status === 'disabled' ? 'Enable' : 'Disable'}
                  </Button>
                  <Button variant="destructive" size="sm" onClick={handleDelete} disabled={deleteSource.isPending}>
                    <Trash2 />
                    Delete
                  </Button>
                </>
              )}
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

          {testError && (
            <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
              <p className="font-medium">The test lead did not go through.</p>
              <p className="mt-0.5">{testError}</p>
            </div>
          )}

          {testResult && (
            <TestLeadPanel
              result={testResult}
              liveStatus={source.status}
              onDismiss={() => setTestResult(null)}
            />
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

            </dl>
          </div>

          {source.kind === 'google_ads' && <GoogleAdsSetupCard source={source} />}

          {source.kind === 'form_embed' && <FormEmbedSetupCard source={source} />}

          {source.kind === 'facebook_form' && <FacebookFormCard source={source} />}
          {source.kind === 'webhook_inbound' && <LegacyWebhookCard />}

          {/* Routing is the one platform capability the legacy webhook actually
              honours, so this card stays for every kind. */}
          <OwnerRoutingCard source={source} />

          {/* The other three are hidden for a managed kind because its write path
              reads none of them — it has its own upsert, its own email match and no
              cap. Offering them would store settings the product then ignores, which
              is worse than offering nothing: they look like they took effect. The
              server rejects them too, so this is not the only guard. */}
          {!isManagedKind(source.kind) && (
            <>
              <DeliveryLimitsCard source={source} />

              <LeadDealCard source={source} />

              <FieldMappingTable sourceId={source.id} />
            </>
          )}

          <div>
            <h3 className="text-sm font-medium text-foreground">Recent deliveries</h3>
            <p className="text-xs text-muted-foreground mt-0.5">
              Every lead this source sent, and what became of it.
            </p>
          </div>

          {stats.data && stats.data.length > 0 && (
            <div className="mb-4">
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                Deliveries, last 30 days
              </p>
              <IngestSparkline data={stats.data} />
            </div>
          )}

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
                            {eventLabel(ev)}
                          </Badge>
                          {ev.note && (
                            <p className="text-xs text-muted-foreground mt-1 max-w-xs">{ev.note}</p>
                          )}
                        </TableCell>
                        <TableCell>
                          {ev.result_record_id ? (
                            <Link
                              to={recordPath(ev.result_slug, ev.result_record_id)}
                              className="text-primary hover:underline"
                            >
                              {recordLabel(ev.result_slug)}
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
            {inspecting.note && (
              // Information, not a failure: this delivery worked, and the note
              // explains a decision the pipeline made on purpose.
              <div className="rounded-lg border border-border bg-muted/40 p-3 text-sm text-foreground">
                {inspecting.note}
              </div>
            )}
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
            {inspecting.consent && <ConsentBlock consent={inspecting.consent} />}
            {Object.keys(inspecting.context ?? {}).length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-foreground">Delivery context</p>
                <p className="text-xs text-muted-foreground">
                  Where this lead came from — for Google Ads deliveries, the campaign and click
                  ids Google sent alongside it; for Meta deliveries, the form, the ad and the{' '}
                  <span className="font-mono">platform</span> it was submitted from (
                  <span className="font-mono">fb</span> or <span className="font-mono">ig</span>).
                </p>
                <pre className="w-full overflow-x-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs">
                  {JSON.stringify(inspecting.context, null, 2)}
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

/**
 * LegacyWebhookCard explains a source the admin did not create and cannot delete.
 *
 * Without it this page is confusing in a specific way: every other source has a
 * credential shown here and a delete button, and this one has neither. The card says
 * where the endpoint actually lives, and what this row is FOR — the parts of the lead
 * platform that could be added to legacy traffic without changing what the workflows
 * built on it receive.
 */
function LegacyWebhookCard() {
  return (
    <div className="rounded-xl border border-border p-4 space-y-2">
      <h3 className="text-sm font-medium text-foreground">Workflow webhook</h3>
      <p className="text-xs text-muted-foreground">
        This is the workspace&apos;s original inbound webhook, the one that predates lead sources.
        Its URL and signing secret are managed in the workflow builder — open any workflow, choose
        the <span className="font-medium">Webhook</span> trigger, and the setup panel is there.
      </p>
      <p className="text-xs text-muted-foreground">
        It appears here so its deliveries show up in the log below, so its leads can be routed to an
        owner like every other channel, and so it raises a health alert when it starts failing.
        Everything else about it is unchanged: the same URL, the same signature, and the same data
        handed to your workflows.
      </p>
      <p className="text-xs text-muted-foreground">
        There is no key to rotate here and no way to switch it off from this page — rotating the
        signing secret in the builder is what stops it accepting deliveries.
      </p>
    </div>
  );
}
