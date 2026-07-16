import { useCallback, useEffect, useState } from 'react';
import { KeyRound, Plus } from 'lucide-react';
import {
  listApiTokens, createApiToken, revokeApiToken, isTokenLive,
  ALL_API_TOKEN_SCOPES, API_TOKEN_SCOPE_LABELS, SCOPE_RECORDS_READ,
  MAX_API_TOKENS_PER_USER, DEFAULT_API_TOKEN_DAYS,
  type APIToken,
} from '../../lib/api';
import { usePermissions } from '../../lib/auth';
import { useConfirm } from '../../components/common/ConfirmDialog';
import SecretReveal from '../../components/settings/SecretReveal';
import { Badge, Button, EmptyState, Input, Label, Select, SpinnerBlock } from '@/components/ui';

// ApiTokensSection (U6.5) — personal access tokens for scripts and integrations.
//
// A token can only ever do a SUBSET of what its owner can: its scopes intersect
// their real permissions, so the form offers only scopes the caller actually holds
// (the server 403s anything else). records.read is the exception — it's a
// token-ONLY scope with no capability behind it, so everyone may grant it, and it
// confers nothing its owner didn't already have.

// Expiry choices. 0 days means "never expires" — allowed, but an explicit choice.
const EXPIRY_OPTIONS: { days: number; label: string }[] = [
  { days: 30, label: '30 days' },
  { days: DEFAULT_API_TOKEN_DAYS, label: '90 days' },
  { days: 180, label: '180 days' },
  { days: 365, label: '1 year' },
  { days: 0, label: 'Never' },
];

function fmtDate(iso?: string): string {
  return iso ? new Date(iso).toLocaleDateString() : '—';
}

export default function ApiTokensSection() {
  const { can, isOwner } = usePermissions();
  const { confirm, dialog } = useConfirm();

  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [loading, setLoading] = useState(true);
  // A load failure replaces the list (there's nothing to show); an ACTION failure
  // renders above it so the list stays visible and retryable.
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  // Create form.
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>([SCOPE_RECORDS_READ]);
  const [expiryDays, setExpiryDays] = useState<number>(DEFAULT_API_TOKEN_DAYS);
  const [creating, setCreating] = useState(false);
  // The plaintext secret — it exists here for exactly one render, then it's gone.
  const [secret, setSecret] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setTokens(await listApiTokens());
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load API tokens');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  // Only scopes the caller holds are offered; the owner holds everything.
  const grantableScopes = ALL_API_TOKEN_SCOPES.filter(
    (s) => s === SCOPE_RECORDS_READ || isOwner || can(s),
  );

  const liveTokens = tokens.filter((t) => isTokenLive(t));
  const atLimit = liveTokens.length >= MAX_API_TOKENS_PER_USER;

  const toggleScope = (s: string) => {
    setScopes((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]));
  };

  const resetForm = () => {
    setShowForm(false);
    setName('');
    setScopes([SCOPE_RECORDS_READ]);
    setExpiryDays(DEFAULT_API_TOKEN_DAYS);
  };

  const submit = async () => {
    setCreating(true);
    setActionError(null);
    try {
      const created = await createApiToken({
        name: name.trim(),
        scopes,
        expires_in_days: expiryDays,
      });
      setSecret(created.secret);
      resetForm();
      await load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to create API token');
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async (t: APIToken) => {
    if (!(await confirm({
      title: 'Revoke token',
      body: `Anything using "${t.name}" stops working immediately. This can't be undone — you'd have to issue a new token.`,
      confirmLabel: 'Revoke',
      tone: 'danger',
    }))) return;
    setBusyId(t.id);
    setActionError(null);
    try {
      await revokeApiToken(t.id);
      await load();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Failed to revoke token');
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="space-y-5 max-w-2xl">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold flex items-center gap-2">
            <KeyRound className="w-5 h-5" /> API tokens
          </h2>
          <p className="text-sm text-muted-foreground mt-0.5">
            Personal tokens for scripts and integrations. A token acts as you and can never do more than you can —
            its scopes narrow your own access, they don't widen it.
          </p>
        </div>
        {!showForm && !secret && (
          <Button
            size="sm"
            onClick={() => { setShowForm(true); setActionError(null); }}
            disabled={atLimit}
            title={atLimit ? `You've reached the limit of ${MAX_API_TOKENS_PER_USER} live tokens — revoke one first.` : undefined}
            className="shrink-0"
          >
            <Plus aria-hidden /> New token
          </Button>
        )}
      </div>

      {actionError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{actionError}</div>
      )}

      {atLimit && !secret && (
        <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-sm text-amber-600 dark:text-amber-400">
          You have {liveTokens.length} live tokens — the maximum is {MAX_API_TOKENS_PER_USER}. Revoke one to create another.
        </div>
      )}

      {/* The one-time secret. Shown once, never recoverable. */}
      {secret && (
        <SecretReveal
          title="Your new API token"
          description="Copy it into your script or integration now. This is the only time it will ever be shown — if you lose it, revoke the token and issue a new one."
          value={secret}
          acknowledgeLabel="I've copied my token"
          onDone={() => setSecret(null)}
        />
      )}

      {/* Create form */}
      {showForm && (
        <div className="rounded-xl border border-border p-4 space-y-4">
          <div>
            <Label htmlFor="token-name" className="mb-1 block text-xs text-muted-foreground">
              What is this token for?
            </Label>
            <Input
              id="token-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={120}
              placeholder="e.g. Nightly export script"
              className="max-w-sm"
            />
          </div>

          <fieldset>
            <legend className="block text-xs font-medium text-muted-foreground mb-1.5">
              Scopes — what this token may do on your behalf
            </legend>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-1.5">
              {grantableScopes.map((s) => (
                <label key={s} className="flex items-start gap-2 text-sm text-foreground cursor-pointer">
                  <input
                    type="checkbox"
                    checked={scopes.includes(s)}
                    onChange={() => toggleScope(s)}
                    className="mt-0.5 rounded border-input"
                  />
                  <span>
                    {API_TOKEN_SCOPE_LABELS[s] ?? s}
                    <span className="block font-mono text-[11px] text-muted-foreground">{s}</span>
                  </span>
                </label>
              ))}
            </div>
          </fieldset>

          <div>
            <Label htmlFor="token-expiry" className="mb-1 block text-xs text-muted-foreground">Expires</Label>
            <Select
              id="token-expiry"
              value={expiryDays}
              onChange={(e) => setExpiryDays(Number(e.target.value))}
              className="w-auto"
            >
              {EXPIRY_OPTIONS.map((o) => (
                <option key={o.days} value={o.days}>{o.label}</option>
              ))}
            </Select>
            {expiryDays === 0 && (
              <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                A token that never expires is a credential nobody remembers to rotate. Prefer a date.
              </p>
            )}
          </div>

          <div className="flex gap-2">
            <Button onClick={submit} disabled={creating || !name.trim() || scopes.length === 0}>
              {creating ? 'Creating…' : 'Create token'}
            </Button>
            <Button variant="outline" onClick={resetForm} disabled={creating}>
              Cancel
            </Button>
          </div>
        </div>
      )}

      {/* List */}
      {error ? (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">{error}</div>
      ) : loading ? (
        <SpinnerBlock />
      ) : tokens.length === 0 ? (
        <EmptyState icon={KeyRound} title="You haven't created any API tokens." />
      ) : (
        <div className="space-y-2">
          {tokens.map((t) => {
            const live = isTokenLive(t);
            const expired = !t.revoked_at && !!t.expires_at && !live;
            return (
              <div
                key={t.id}
                className={`flex items-start justify-between gap-3 rounded-xl border border-border p-3 ${live ? '' : 'opacity-60'}`}
              >
                <div className="min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="font-medium text-foreground">{t.name}</span>
                    <code className="font-mono text-xs text-muted-foreground">{t.prefix}…</code>
                    {t.revoked_at && <Badge variant="destructive">Revoked</Badge>}
                    {expired && <Badge variant="warning">Expired</Badge>}
                  </div>
                  <div className="mt-0.5 text-xs text-muted-foreground">
                    Created {fmtDate(t.created_at)} · {t.expires_at ? `expires ${fmtDate(t.expires_at)}` : 'never expires'} ·{' '}
                    {t.last_used_at ? `last used ${fmtDate(t.last_used_at)}` : 'never used'}
                  </div>
                  <div className="mt-1.5 flex flex-wrap gap-1">
                    {t.scopes.map((s) => (
                      <span
                        key={s}
                        title={API_TOKEN_SCOPE_LABELS[s] ?? s}
                        className="inline-flex items-center rounded-full border border-border bg-muted px-2 py-0.5 font-mono text-[10px] text-muted-foreground"
                      >
                        {s}
                      </span>
                    ))}
                  </div>
                </div>
                {live && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleRevoke(t)}
                    disabled={busyId === t.id}
                    className="shrink-0 text-destructive hover:bg-destructive/10 hover:text-destructive"
                  >
                    {busyId === t.id ? 'Revoking…' : 'Revoke'}
                  </Button>
                )}
              </div>
            );
          })}
        </div>
      )}

      {dialog}
    </div>
  );
}
