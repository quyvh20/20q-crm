import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Plug, Plus } from 'lucide-react';
import {
  Badge,
  Button,
  EmptyState,
  Input,
  Label,
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
import SecretReveal from '../../components/settings/SecretReveal';
import OwnerPicker from '../../components/records/OwnerPicker';
import ProviderConnections from './ProviderConnections';
import { relativeTime } from '../../lib/relativeTime';
import { useCreateSource, useLeadSources } from '../../features/integrations/queries';
import {
  UPDATE_POLICY_HELP,
  UPDATE_POLICY_LABELS,
  isKeylessKind,
  kindLabel,
  type LeadSource,
  type LeadSourceStatus,
  type UpdatePolicy,
} from '../../features/integrations/types';

// The Integrations section (L1.2): where an admin creates the credentials third
// parties use to send leads in, and where they go to find out what happened to one.

const STATUS_VARIANT: Record<LeadSourceStatus, 'success' | 'secondary' | 'destructive'> = {
  active: 'success',
  disabled: 'secondary',
  error: 'destructive',
};

export default function IntegrationsSection() {
  const { data: sources, isLoading, error } = useLeadSources();
  const createSource = useCreateSource();

  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState('');
  const [kind, setKind] = useState<'api' | 'google_ads' | 'form_embed'>('api');
  const [policy, setPolicy] = useState<UpdatePolicy>('fill_blank_only');
  // Routing is a REQUIRED choice, not a default. Creating a source that silently
  // produces unowned contacts is the failure this whole platform opens by naming —
  // an unowned contact is invisible to every own-scoped rep.
  const [routing, setRouting] = useState<'' | 'owner' | 'unassigned'>('');
  const [owner, setOwner] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  // The one-time secrets live HERE, in component state, and nowhere else — never
  // in the query cache, a URL, or a log. google_ads sources mint TWO: the bearer
  // key (batch recovery authenticates with it) and the Google webhook key.
  const [newKey, setNewKey] = useState<string | null>(null);
  const [newGoogleKey, setNewGoogleKey] = useState<string | null>(null);
  const [newSourceId, setNewSourceId] = useState<string | null>(null);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setActionError('');
    try {
      const { source, plaintext_key, google_key } = await createSource.mutateAsync({
        name: name.trim(),
        kind,
        update_policy: policy,
        default_owner_id: routing === 'owner' ? owner : null,
      });
      setNewKey(plaintext_key);
      setNewGoogleKey(google_key ?? null);
      setNewSourceId(source.id);
      setShowForm(false);
      setName('');
      setKind('api');
      setPolicy('fill_blank_only');
      setRouting('');
      setOwner(null);
    } catch (err) {
      setActionError(err instanceof Error ? err.message : 'Failed to create the lead source');
    }
  };

  return (
    <div className="space-y-5 max-w-3xl">
      <div>
        <h2 className="text-lg font-semibold text-foreground">Integrations</h2>
        <p className="text-sm text-muted-foreground mt-1">
          Connect the tools that send you leads. Each source gets its own key, so you can
          revoke one without touching the others.
        </p>
      </div>

      {/* Provider connections (L5.2): OAuth'd ad-platform accounts (Facebook pages),
          separate from the per-key sources below. Self-contained — its own queries,
          loading and the account-picker interstitial. */}
      <ProviderConnections />

      {/* The one-time secrets. Rendered above the list so they cannot be missed.
          A google_ads source shows its GOOGLE key here — the value the advertiser
          actually pastes — and points at the source page for the URL beside it.
          The bearer key still exists (batch recovery uses it) but showing two
          secrets at once buries the one that matters. */}
      {newGoogleKey ? (
        <SecretReveal
          title="Your Google webhook key"
          description={
            <>
              This is the only time you&apos;ll see it — copy it now. Paste it into Google&apos;s
              form editor next to the webhook URL, which is waiting on{' '}
              {newSourceId ? (
                <Link className="underline" to={`/settings/integrations/${newSourceId}`}>
                  the source&apos;s page
                </Link>
              ) : (
                'the source&apos;s page'
              )}
              .
            </>
          }
          value={newGoogleKey}
          onDone={() => {
            setNewGoogleKey(null);
            setNewKey(null);
          }}
        />
      ) : (
        <>
          {newKey && (
            <SecretReveal
              title="Your capture key"
              description="This is the only time you'll see it — copy it now. Paste it into the tool that will send you leads; the setup steps are below."
              value={newKey}
              onDone={() => setNewKey(null)}
            />
          )}
          {newKey && <SetupRecipe apiKey={newKey} />}
        </>
      )}

      {actionError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {actionError}
        </div>
      )}

      {/* Load error replaces the list; an action error (above) leaves it visible. */}
      {error ? (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error instanceof Error ? error.message : 'Failed to load lead sources'}
        </div>
      ) : isLoading ? (
        <SpinnerBlock />
      ) : (
        <>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium text-foreground">Lead sources</h3>
            <div className="flex items-center gap-2">
              {/* The org-wide ledger. Reachable from here because the failures worth
                  looking at are precisely the ones no single source can show: a
                  provider delivery that broke before we knew which form it belonged
                  to has no source to file it under. */}
              <Link
                to="/settings/integrations/deliveries"
                className="text-xs text-primary hover:underline"
              >
                View all deliveries
              </Link>
              {!showForm && (
                <Button size="sm" onClick={() => setShowForm(true)}>
                  <Plus />
                  New source
                </Button>
              )}
            </div>
          </div>

          {showForm && (
            <form onSubmit={handleCreate} className="rounded-xl border border-border p-4 space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="source-name">Name</Label>
                <Input
                  id="source-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Website contact form"
                  required
                />
                <p className="text-xs text-muted-foreground">
                  Something you'll recognize later — it's how this key is identified in the list.
                </p>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="source-kind">Where do these leads come from?</Label>
                <Select
                  id="source-kind"
                  value={kind}
                  onChange={(e) => setKind(e.target.value as 'api' | 'google_ads' | 'form_embed')}
                >
                  <option value="api">A tool that sends leads to a key (Make, Zapier, your own code)</option>
                  <option value="google_ads">A Google Ads lead form</option>
                  <option value="form_embed">A form on my own website</option>
                </Select>
                {kind === 'form_embed' && (
                  <p className="text-xs text-muted-foreground">
                    You'll get a snippet to paste into your page. Nothing is accepted until you
                    name the website it runs on — a new form turns every browser away.
                  </p>
                )}
                {kind === 'google_ads' && (
                  <p className="text-xs text-muted-foreground">
                    You'll get a webhook URL and a key to paste into Google's lead form editor —
                    no Google app or login needed. Leads from the form arrive as they're
                    submitted.
                  </p>
                )}
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="source-policy">When a lead matches an existing contact</Label>
                <Select
                  id="source-policy"
                  value={policy}
                  onChange={(e) => setPolicy(e.target.value as UpdatePolicy)}
                >
                  {(Object.keys(UPDATE_POLICY_LABELS) as UpdatePolicy[]).map((p) => (
                    <option key={p} value={p}>
                      {UPDATE_POLICY_LABELS[p]}
                    </option>
                  ))}
                </Select>
                <p className="text-xs text-muted-foreground">{UPDATE_POLICY_HELP[policy]}</p>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="source-routing">Who gets these leads</Label>
                <Select
                  id="source-routing"
                  value={routing}
                  onChange={(e) => setRouting(e.target.value as '' | 'owner' | 'unassigned')}
                >
                  <option value="">Choose…</option>
                  <option value="owner">Assign them to someone</option>
                  <option value="unassigned">Leave them unassigned</option>
                </Select>
                {routing === 'owner' && (
                  <div className="pt-1">
                    <OwnerPicker id="source-owner" value={owner} onChange={setOwner} />
                  </div>
                )}
                {routing === 'unassigned' && (
                  <p className="text-xs text-amber-700 dark:text-amber-400">
                    Reps who only see their own records will not see these leads. You can set up a
                    rotation later from the source's page.
                  </p>
                )}
                {routing === '' && (
                  <p className="text-xs text-muted-foreground">
                    Leads that land on nobody are the easiest ones to lose, so this is a required
                    choice.
                  </p>
                )}
              </div>

              <div className="flex items-center gap-2">
                {/* type="submit" is explicit: Button defaults to type="button", so
                    without it the form silently never submits. */}
                <Button
                  type="submit"
                  size="sm"
                  disabled={
                    createSource.isPending ||
                    !name.trim() ||
                    routing === '' ||
                    (routing === 'owner' && !owner)
                  }
                >
                  {createSource.isPending ? 'Creating…' : 'Create source'}
                </Button>
                <Button type="button" variant="ghost" size="sm" onClick={() => setShowForm(false)}>
                  Cancel
                </Button>
              </div>
            </form>
          )}

          {sources && sources.length === 0 && !showForm ? (
            <EmptyState
              icon={Plug}
              title="No lead sources yet"
              description="Create a source to get a key, then paste it into Make, Zapier, or your own website form to start sending leads in."
              action={
                <Button size="sm" onClick={() => setShowForm(true)}>
                  <Plus />
                  New source
                </Button>
              }
            />
          ) : (
            sources &&
            sources.length > 0 && (
              <TableShell>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Type</TableHead>
                      <TableHead>Key</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>Last lead</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {sources.map((s: LeadSource) => (
                      <TableRow key={s.id}>
                        <TableCell>
                          <Link
                            to={`/settings/integrations/${s.id}`}
                            className="font-medium text-foreground hover:text-primary"
                          >
                            {s.name}
                          </Link>
                        </TableCell>
                        <TableCell>
                          <span className="text-xs text-muted-foreground">{kindLabel(s.kind)}</span>
                        </TableCell>
                        <TableCell>
                          {/* A keyless kind has no bearer key — facebook_form is
                              credentialed by its connection, webhook_inbound by the org
                              token — so show a neutral placeholder, not a bare "…". */}
                          {isKeylessKind(s.kind) ? (
                            <span className="text-xs text-muted-foreground">—</span>
                          ) : (
                            <code className="font-mono text-xs text-muted-foreground">
                              {s.token_prefix}…
                            </code>
                          )}
                        </TableCell>
                        <TableCell>
                          <Badge variant={STATUS_VARIANT[s.status]}>{s.status}</Badge>
                        </TableCell>
                        <TableCell className="text-muted-foreground">
                          {relativeTime(s.last_used_at)}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TableShell>
            )
          )}

          <RelayRecipes />
        </>
      )}
    </div>
  );
}

/**
 * RelayRecipes answers the question the sources table provokes: "my platform isn't
 * in the list — is it supported?"
 *
 * It is prose on the page rather than a link to a doc because the app has no docs
 * route and nothing serves the repo's docs/ directory, so a link would be a 404 —
 * and it names LinkedIn explicitly because the alternative reading of an absent
 * entry is "this product cannot do LinkedIn", which is wrong.
 *
 * LinkedIn is deliberately not a native connector: Lead Sync API access is gated on
 * a verified company page and a per-app review, and its versions retire on a
 * schedule, so a native adapter is a maintenance commitment someone else can switch
 * off. The relay cannot be revoked.
 */
function RelayRecipes() {
  return (
    <div className="rounded-xl border border-border p-4 space-y-3">
      <div>
        <h3 className="text-sm font-semibold text-foreground">Platforms without a direct connection</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Anything Make or Zapier can read — <span className="font-medium">LinkedIn Lead Gen
          Forms</span>, Typeform, Webflow, a spreadsheet — reaches the CRM through a{' '}
          <span className="font-medium">Capture API</span> source. Create one above, then add an
          HTTP step that POSTs{' '}
          <code className="font-mono">{'{"fields": {…}}'}</code> to it.
        </p>
      </div>
      <ul className="space-y-1.5 text-xs text-muted-foreground">
        <li>
          <span className="font-medium text-foreground">Name the source for the channel</span>{' '}
          (&ldquo;LinkedIn&rdquo;), not for the relay tool — reports group by that name, and{' '}
          <code className="font-mono">lead_source</code> will read{' '}
          <code className="font-mono">integration:api</code> for every relayed lead regardless.
        </li>
        <li>
          <span className="font-medium text-foreground">Send the platform&apos;s own lead id as{' '}
          <code className="font-mono">Idempotency-Key</code></span>. Without it a re-run of the
          scenario relays the same person again, and there is nothing here to dedupe on.
        </li>
        <li>
          <span className="font-medium text-foreground">Alert on scenario failure in the relay
          tool.</span> A paused scenario or an expired connection stops leads arriving, and this
          delivery log only records what reached us — a relay that stopped relaying looks
          exactly like a quiet week.
        </li>
      </ul>
    </div>
  );
}

/**
 * SetupRecipe is the difference between a key and a working integration.
 *
 * Make is named first and Zapier second on purpose: Zapier's webhook action is a
 * paid-plan feature, so recommending it first sends people into a paywall to do
 * the thing we just told them to do.
 */
function SetupRecipe({ apiKey }: { apiKey: string }) {
  const url = `${window.location.origin}/api/capture/leads`;
  const curl = `curl -X POST ${url} \\
  -H "Authorization: Bearer ${apiKey}" \\
  -H "Content-Type: application/json" \\
  -d '{"fields":{"email":"ada@example.com","first_name":"Ada"}}'`;

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-semibold text-foreground">Send your first lead</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Point any tool at this URL with the key above. Leads arrive as contacts and trigger
          your workflows.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label>Endpoint</Label>
        <code className="block w-full break-all rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs text-foreground">
          POST {url}
        </code>
      </div>

      <div className="space-y-1.5">
        <Label>Try it now</Label>
        <pre className="w-full overflow-x-auto rounded-lg border border-border bg-background px-3 py-2 font-mono text-xs text-foreground">
          {curl}
        </pre>
        <p className="text-xs text-muted-foreground">
          <code className="font-mono">email</code> is required. Anything else you send that
          isn't a contact field is recorded but not saved — the delivery log shows exactly what
          was skipped.
        </p>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <p className="text-xs font-medium text-foreground">Make (free tier)</p>
          <p className="text-xs text-muted-foreground">
            Add an <span className="font-medium">HTTP → Make a request</span> module: method
            POST, the URL above, header <code className="font-mono">Authorization</code> set to{' '}
            <code className="font-mono">Bearer &lt;your key&gt;</code>, body type JSON.
          </p>
        </div>
        <div className="space-y-1">
          <p className="text-xs font-medium text-foreground">Zapier (paid plan)</p>
          <p className="text-xs text-muted-foreground">
            Add a <span className="font-medium">Webhooks by Zapier → POST</span> action with the
            same URL and header. Webhooks is a premium Zapier app, so this step needs a paid
            Zapier plan.
          </p>
        </div>
      </div>
    </div>
  );
}
