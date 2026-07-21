import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiError, apiFetch, parseJsonSafe } from '../../lib/api';
import { integrationKeys } from './queries';
import type { Connection, DiagnoseCheck, DiagnoseResult, PendingCandidates, ProviderForm, ProviderInfo } from './types';

// Provider-connection API + react-query layer (L5.2), sibling to the sources
// module. Same discipline: every call through apiFetch (bearer + 401→refresh) and
// parseJsonSafe (a proxy HTML error must not surface as "Unexpected token '<'"),
// and every list coerced off Go's nil-slice-marshals-to-null.

function asList<T>(data: unknown): T[] {
  return Array.isArray(data) ? (data as T[]) : [];
}

const PROVIDERS = '/api/integrations/providers';
const CONNECTIONS = '/api/integrations/connections';
const PENDING = '/api/integrations/pending';

/** The providers this deployment can connect. Empty when provider encryption is
 *  unconfigured (the backend omits providers it cannot serve), so the UI simply
 *  shows no connect buttons rather than a dead end. */
export async function listProviders(): Promise<ProviderInfo[]> {
  const res = await apiFetch(PROVIDERS);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load providers');
  return asList<ProviderInfo>(json.data);
}

export async function listConnections(): Promise<Connection[]> {
  const res = await apiFetch(CONNECTIONS);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load connections');
  return asList<Connection>(json.data);
}

/**
 * startConnect asks the server for the provider consent URL. The caller then does
 * a FULL-PAGE redirect to it — a fetch (which carries the bearer) rather than a
 * navigation (which would not), matching the Google-login precedent's shape but
 * on an authenticated route.
 */
export async function startConnect(provider: string): Promise<{ auth_url: string }> {
  const res = await apiFetch(`${PROVIDERS}/${encodeURIComponent(provider)}/connect`, {
    method: 'POST',
    body: JSON.stringify({ return_to: '/settings/integrations' }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not start the connection');
  const data = (json.data ?? {}) as { auth_url?: string };
  if (!data.auth_url) throw new Error('The server did not return a connection URL');
  return { auth_url: data.auth_url };
}

/** getPendingCandidates loads the account picker's token-free choices. Owner-scoped
 *  server-side, so a token that is not this caller's returns a 404. */
export async function getPendingCandidates(token: string): Promise<PendingCandidates> {
  const res = await apiFetch(`${PENDING}/${encodeURIComponent(token)}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'This connection request has expired — start again');
  const data = (json.data ?? {}) as Partial<PendingCandidates>;
  return { provider: data.provider ?? '', accounts: asList(data.accounts) };
}

export async function selectAccount(token: string, accountId: string): Promise<Connection> {
  const res = await apiFetch(`${PENDING}/${encodeURIComponent(token)}/select`, {
    method: 'POST',
    body: JSON.stringify({ account_id: accountId }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not connect that account');
  return json.data as Connection;
}

export async function disconnectConnection(id: string): Promise<void> {
  const res = await apiFetch(`${CONNECTIONS}/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (res.status === 204) return;
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not disconnect');
}

/** listForms discovers a connection's provider lead forms and which are enabled. */
export async function listForms(connectionId: string): Promise<ProviderForm[]> {
  const res = await apiFetch(`${CONNECTIONS}/${encodeURIComponent(connectionId)}/forms`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load forms');
  return asList<ProviderForm>(json.data);
}

/** enableForm creates the facebook_form source for a provider form (idempotent). */
export async function enableForm(
  connectionId: string,
  form: { form_id: string; form_name: string },
): Promise<{ source_id: string }> {
  const res = await apiFetch(`${CONNECTIONS}/${encodeURIComponent(connectionId)}/forms`, {
    method: 'POST',
    body: JSON.stringify(form),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not enable the form');
  const data = (json.data ?? {}) as { source_id?: string };
  return { source_id: data.source_id ?? '' };
}

/** startBackfill imports a facebook_form source's historical leads. enroll opts the
 *  imported leads into automation (off by default — see the confirm dialog copy). */
export async function startBackfill(sourceId: string, enroll: boolean): Promise<void> {
  const res = await apiFetch(`/api/integrations/sources/${encodeURIComponent(sourceId)}/backfill`, {
    method: 'POST',
    body: JSON.stringify({ enroll }),
  });
  if (res.status === 202) return;
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not start the import');
}

// ── react-query ──────────────────────────────────────────────────────────────

/**
 * diagnoseConnection probes which layer of a connection is actually broken.
 *
 * POST because it makes live calls to the provider — it is an action with a cost, not
 * a cacheable read. The server returns keys and statuses only; every sentence the
 * admin reads is chosen here (see DIAGNOSE_COPY), which is both the house posture and
 * a security control: a provider error embeds the request URL, and a page token rides
 * in that query string.
 */
export async function diagnoseConnection(id: string): Promise<DiagnoseResult> {
  const res = await apiFetch(`/api/integrations/connections/${id}/diagnose`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not run the check');
  const data = json?.data ?? {};
  return {
    checks: asList<DiagnoseCheck>(data.checks),
    healthy: data.healthy === true,
  };
}

export const connectionKeys = {
  diagnose: (id: string) => [...integrationKeys.all, 'diagnose', id] as const,
  providers: () => [...integrationKeys.all, 'providers'] as const,
  connections: () => [...integrationKeys.all, 'connections'] as const,
  pending: (token: string) => [...integrationKeys.all, 'pending', token] as const,
  forms: (connectionId: string) => [...integrationKeys.all, 'forms', connectionId] as const,
};

export function useProviders() {
  return useQuery({ queryKey: connectionKeys.providers(), queryFn: listProviders });
}

export function useConnections() {
  return useQuery({
    queryKey: connectionKeys.connections(),
    queryFn: listConnections,
    // Returning from the OAuth round trip must not show a stale list.
    refetchOnMount: 'always',
  });
}

export function usePendingCandidates(token: string | undefined) {
  return useQuery({
    queryKey: connectionKeys.pending(token ?? ''),
    queryFn: () => getPendingCandidates(token as string),
    enabled: Boolean(token),
    // A picker interstitial is short-lived; do not silently retry a consumed/expired
    // token into repeated 404s.
    retry: false,
  });
}

/** useConnectProvider is NOT optimistic and does no cache work — the success path
 *  is a full-page redirect to the provider, so there is no post-mutation state to
 *  reconcile here. */
export function useConnectProvider() {
  return useMutation({ mutationFn: (provider: string) => startConnect(provider) });
}

export function useSelectAccount() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ token, accountId }: { token: string; accountId: string }) =>
      selectAccount(token, accountId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: connectionKeys.connections() });
    },
  });
}

export function useDisconnectConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => disconnectConnection(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: connectionKeys.connections() });
    },
  });
}

export function useConnectionForms(connectionId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: connectionKeys.forms(connectionId ?? ''),
    queryFn: () => listForms(connectionId as string),
    enabled: Boolean(connectionId) && enabled,
    // A Graph call every render would be wasteful; the picker opens on demand.
    refetchOnWindowFocus: false,
  });
}

export function useEnableForm() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ connectionId, form }: { connectionId: string; form: { form_id: string; form_name: string } }) =>
      enableForm(connectionId, form),
    onSuccess: (_res, { connectionId }) => {
      qc.invalidateQueries({ queryKey: connectionKeys.forms(connectionId) });
      // The new facebook_form source appears in the source list.
      qc.invalidateQueries({ queryKey: integrationKeys.lists() });
    },
  });
}

/** useBackfill fires the import. The delivery log picks up the imported leads, so it
 *  invalidates that source's events on success. */
export function useBackfill() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ sourceId, enroll }: { sourceId: string; enroll: boolean }) =>
      startBackfill(sourceId, enroll),
    onSuccess: (_res, { sourceId }) => {
      qc.invalidateQueries({ queryKey: integrationKeys.events(sourceId) });
    },
  });
}

/** useDiagnoseConnection runs the layered check for one connection. */
export function useDiagnoseConnection() {
  return useMutation({
    mutationFn: (id: string) => diagnoseConnection(id),
  });
}
