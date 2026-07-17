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
import { useCreateSource, useLeadSources } from '../../features/integrations/queries';
import {
  UPDATE_POLICY_HELP,
  UPDATE_POLICY_LABELS,
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

function relativeTime(iso?: string): string {
  if (!iso) return 'Never';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return 'Never';
  const mins = Math.floor((Date.now() - then) / 60000);
  if (mins < 1) return 'Just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

export default function IntegrationsSection() {
  const { data: sources, isLoading, error } = useLeadSources();
  const createSource = useCreateSource();

  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState('');
  const [policy, setPolicy] = useState<UpdatePolicy>('fill_blank_only');
  const [actionError, setActionError] = useState('');
  // The one-time key lives HERE, in component state, and nowhere else — never in
  // the query cache, a URL, or a log.
  const [newKey, setNewKey] = useState<string | null>(null);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    setActionError('');
    try {
      const { plaintext_key } = await createSource.mutateAsync({
        name: name.trim(),
        update_policy: policy,
      });
      setNewKey(plaintext_key);
      setShowForm(false);
      setName('');
      setPolicy('fill_blank_only');
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

      {/* The one-time key. Rendered above the list so it cannot be missed. */}
      {newKey && (
        <SecretReveal
          title="Your capture key"
          description="This is the only time you'll see it — copy it now. Paste it into the tool that will send you leads; the setup steps are below."
          value={newKey}
          onDone={() => setNewKey(null)}
        />
      )}
      {newKey && <SetupRecipe apiKey={newKey} />}

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
            {!showForm && (
              <Button size="sm" onClick={() => setShowForm(true)}>
                <Plus />
                New source
              </Button>
            )}
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

              <div className="flex items-center gap-2">
                {/* type="submit" is explicit: Button defaults to type="button", so
                    without it the form silently never submits. */}
                <Button type="submit" size="sm" disabled={createSource.isPending || !name.trim()}>
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
                          <code className="font-mono text-xs text-muted-foreground">
                            {s.token_prefix}…
                          </code>
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
        </>
      )}
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
