import { useEffect, useMemo, useState } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { AlertTriangle, ChevronDown, ChevronRight, Plug, RefreshCw, Trash2, X } from 'lucide-react';
import { Badge, Button, SpinnerBlock } from '@/components/ui';
import Modal from '../../components/common/Modal';
import { useConfirm } from '../../components/common/ConfirmDialog';
import {
  useConnectProvider,
  useConnectionForms,
  useConnections,
  useDisconnectConnection,
  useEnableForm,
  usePendingCandidates,
  useProviders,
  useSelectAccount,
} from '../../features/integrations/connections';
import type { Connection, ConnectionStatus, ProviderForm } from '../../features/integrations/types';

const STATUS_VARIANT: Record<ConnectionStatus, 'success' | 'secondary' | 'destructive' | 'warning'> = {
  connected: 'success',
  degraded: 'warning',
  error: 'destructive',
  revoked: 'secondary',
  disconnected: 'secondary',
};

// Short, non-reflected copy for the machine reasons the OAuth callback can send.
// Never render the raw provider error — the backend already reduced it to a code.
const CONNECT_ERROR_COPY: Record<string, string> = {
  denied: 'The connection was cancelled or declined. Nothing was changed.',
  connect_failed: "We couldn't complete the connection. Please try again.",
};

/**
 * ProviderConnections is the L5.2 connect surface: a button per available
 * provider, the account-picker interstitial (opened from the ?connect=…#selection=
 * the callback redirects to), and a card per connected account.
 *
 * It reads the selection token from the URL FRAGMENT (kept out of the redirect's
 * Referer/history) and clears the whole connect state off the URL once handled,
 * so a refresh does not re-open a spent picker.
 */
export default function ProviderConnections() {
  const { confirm, dialog } = useConfirm();
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();

  const providers = useProviders();
  const connections = useConnections();
  const connect = useConnectProvider();
  const disconnect = useDisconnectConnection();

  const [connectError, setConnectError] = useState('');
  const [startError, setStartError] = useState('');
  const [openForms, setOpenForms] = useState<string | null>(null);

  // The selection token rides in the fragment (#selection=…), the provider in the
  // query (?connect=…). Both present ⇒ open the picker.
  const selectionToken = useMemo(() => {
    return new URLSearchParams(location.hash.replace(/^#/, '')).get('selection') ?? '';
  }, [location.hash]);
  const connectingProvider = searchParams.get('connect') ?? '';
  const pickerOpen = Boolean(connectingProvider && selectionToken);

  useEffect(() => {
    const reason = searchParams.get('connect_error');
    if (reason) setConnectError(CONNECT_ERROR_COPY[reason] ?? 'The connection did not complete.');
  }, [searchParams]);

  // Strip the connect state off the URL (query + fragment) without a history entry.
  const clearConnectState = () => navigate('/settings/integrations', { replace: true });

  const handleConnect = async (providerKey: string) => {
    setStartError('');
    try {
      const { auth_url } = await connect.mutateAsync(providerKey);
      // Full-page redirect to the provider consent screen.
      window.location.href = auth_url;
    } catch (err) {
      setStartError(err instanceof Error ? err.message : 'Could not start the connection');
    }
  };

  const handleDisconnect = async (c: Connection) => {
    const ok = await confirm({
      title: 'Disconnect this account?',
      body: `New leads from "${c.external_account_label || c.external_account_id}" will stop arriving. You can reconnect it later.`,
      confirmLabel: 'Disconnect',
      tone: 'danger',
    });
    if (!ok) return;
    try {
      await disconnect.mutateAsync(c.id);
    } catch {
      // The list query stays as-is; a transient failure is retryable from the card.
    }
  };

  const available = providers.data ?? [];
  const conns = connections.data ?? [];

  // A load FAILURE must be distinguishable from a genuinely empty state: on error
  // both queries' data is undefined → [], which would otherwise collapse into the
  // "nothing configured" null-return below and silently hide live connections (an
  // admin could reconnect a page they wrongly believe was lost). Surface it, like
  // the sibling lead-sources list does.
  if (providers.isError || connections.isError) {
    const err = providers.error ?? connections.error;
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
        {err instanceof Error ? err.message : 'Failed to load connected accounts'}
      </div>
    );
  }

  // Nothing to show and nothing to offer: keep the section out of the way entirely
  // (a deployment with no provider configured shows no connect buttons — see
  // ConnectionService.Providers). BOTH queries must have settled — gating on
  // providers alone would briefly hide a live connection when providers resolves
  // empty (an unconfigured-but-previously-connected deployment) a tick before the
  // connections list returns.
  if (!providers.isLoading && !connections.isLoading && available.length === 0 && conns.length === 0) {
    return null;
  }

  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-sm font-medium text-foreground">Connected accounts</h3>
        <p className="text-xs text-muted-foreground mt-1">
          Connect an ad platform once and its lead forms flow in automatically.
        </p>
      </div>

      {connectError && (
        <div className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          <span className="flex-1">{connectError}</span>
          <button
            type="button"
            aria-label="Dismiss"
            onClick={() => {
              setConnectError('');
              clearConnectState();
            }}
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}
      {startError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {startError}
        </div>
      )}

      {providers.isLoading || connections.isLoading ? (
        <SpinnerBlock />
      ) : (
        <div className="rounded-xl border border-border divide-y divide-border">
          {conns.map((c) => (
            <div key={c.id} className="p-4">
              <div className="flex items-center gap-3">
                <Plug className="h-4 w-4 text-muted-foreground shrink-0" />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-foreground truncate">
                      {c.external_account_label || c.external_account_id}
                    </span>
                    <Badge variant={STATUS_VARIANT[c.status] ?? 'secondary'}>{c.status}</Badge>
                    {c.status !== 'connected' ? null : c.subscribed ? (
                      <Badge variant="success">receiving leads</Badge>
                    ) : (
                      <Badge variant="warning">not receiving yet</Badge>
                    )}
                  </div>
                  {/* The reason shows on ANY status. This used to also require
                      !c.subscribed, which is only ever set on a `connected`
                      connection — so an `error` or `degraded` account rendered a
                      red/amber pill with no explanation beside it, hiding the
                      message in exactly the state the admin needs it. last_error
                      is a fixed, token-free string the backend chose, never a
                      reflected provider payload, so it is safe to render as-is. */}
                  {c.last_error && (
                    <p className="mt-1 flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400">
                      <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
                      {c.last_error}
                    </p>
                  )}
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setOpenForms((cur) => (cur === c.id ? null : c.id))}
                >
                  {openForms === c.id ? <ChevronDown /> : <ChevronRight />}
                  Manage forms
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => handleDisconnect(c)}
                  disabled={disconnect.isPending}
                >
                  <Trash2 />
                  Disconnect
                </Button>
              </div>
              {openForms === c.id && <ConnectionForms connectionId={c.id} />}
            </div>
          ))}

          {available.map((p) => (
            <div key={p.key} className="flex items-center gap-3 p-4">
              <div className="flex-1">
                <p className="text-sm text-foreground">{p.label}</p>
              </div>
              <Button size="sm" onClick={() => handleConnect(p.key)} disabled={connect.isPending}>
                <RefreshCw />
                Connect
              </Button>
            </div>
          ))}
        </div>
      )}

      {pickerOpen && (
        <AccountPicker
          provider={connectingProvider}
          token={selectionToken}
          onClose={clearConnectState}
        />
      )}
      {dialog}
    </div>
  );
}

/**
 * AccountPicker lets the admin choose which provider account (a Facebook page) to
 * connect, after the OAuth round trip. It never sees a token — only {id,label}.
 */
function AccountPicker({
  provider,
  token,
  onClose,
}: {
  provider: string;
  token: string;
  onClose: () => void;
}) {
  const candidates = usePendingCandidates(token);
  const select = useSelectAccount();
  const [chosen, setChosen] = useState('');
  const [error, setError] = useState('');

  const accounts = candidates.data?.accounts ?? [];

  const handleSelect = async () => {
    if (!chosen) return;
    setError('');
    try {
      await select.mutateAsync({ token, accountId: chosen });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not connect that account');
    }
  };

  return (
    <Modal open onClose={onClose} title={`Choose an account to connect`} size="md">
      <div className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Pick which {provider === 'facebook' ? 'Facebook page' : 'account'} should send its leads
          into this workspace.
        </p>

        {candidates.isLoading ? (
          <SpinnerBlock />
        ) : candidates.isError ? (
          <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            {candidates.error instanceof Error
              ? candidates.error.message
              : 'This connection request has expired — start again.'}
          </div>
        ) : accounts.length === 0 ? (
          <div className="rounded-lg border border-border p-3 text-sm text-muted-foreground">
            No accounts were available to connect. Check that your login has access to at least one
            page, then try again.
          </div>
        ) : (
          <div className="rounded-lg border border-border divide-y divide-border max-h-72 overflow-y-auto">
            {accounts.map((a) => (
              <label
                key={a.id}
                className="flex items-center gap-3 p-3 cursor-pointer hover:bg-muted/40"
              >
                <input
                  type="radio"
                  name="account"
                  value={a.id}
                  checked={chosen === a.id}
                  onChange={() => setChosen(a.id)}
                />
                <span className="text-sm text-foreground">{a.label || a.id}</span>
              </label>
            ))}
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            {error}
          </div>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSelect}
            disabled={!chosen || select.isPending || candidates.isLoading}
          >
            {select.isPending ? 'Connecting…' : 'Connect'}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

/**
 * ConnectionForms lists a connection's provider lead forms and lets the admin
 * enable one. Enabling creates a facebook_form source, which then appears in the
 * lead-source list below with its own mapping, delivery log and import action.
 */
function ConnectionForms({ connectionId }: { connectionId: string }) {
  const forms = useConnectionForms(connectionId);
  const enable = useEnableForm();
  const [error, setError] = useState('');

  const handleEnable = async (f: ProviderForm) => {
    setError('');
    try {
      await enable.mutateAsync({ connectionId, form: { form_id: f.id, form_name: f.name } });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not enable the form');
    }
  };

  return (
    <div className="mt-3 border-t border-border pt-3 pl-7">
      {forms.isLoading ? (
        <SpinnerBlock />
      ) : forms.isError ? (
        <p className="text-sm text-destructive">
          {forms.error instanceof Error ? forms.error.message : 'Could not load forms'}
        </p>
      ) : (forms.data ?? []).length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No lead forms were found on this account.
        </p>
      ) : (
        <div className="space-y-2">
          {error && <p className="text-sm text-destructive">{error}</p>}
          {(forms.data ?? []).map((f) => (
            <div key={f.id} className="flex items-center gap-3">
              <span className="flex-1 text-sm text-foreground truncate">{f.name}</span>
              {f.enabled ? (
                <Badge variant="success">Enabled</Badge>
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => handleEnable(f)}
                  disabled={enable.isPending}
                >
                  Enable
                </Button>
              )}
            </div>
          ))}
          <p className="text-xs text-muted-foreground pt-1">
            Enabling a form starts capturing its new leads. Configure its field mapping and import
            past leads from its entry in the lead-source list below.
          </p>
        </div>
      )}
    </div>
  );
}
