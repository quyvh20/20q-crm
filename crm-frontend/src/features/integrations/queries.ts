import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as api from './api';
import type {
  CreateSourceInput,
  EventLogFilters,
  EventPage,
  FieldMap,
  IntegrationEvent,
  LeadSource,
  UpdateSourceInput,
} from './types';

// React-query layer, mirroring features/workflows/queries.ts: one exported key
// factory with a lists() invalidation umbrella, plus hooks.

export const integrationKeys = {
  all: ['integrations'] as const,
  lists: () => [...integrationKeys.all, 'list'] as const,
  details: () => [...integrationKeys.all, 'detail'] as const,
  detail: (id: string) => [...integrationKeys.details(), id] as const,
  // Events are a SIBLING of detail, not nested under it. Nested, every
  // invalidateQueries(detail(id)) after a rotate/disable would prefix-match and
  // refetch the whole delivery log too.
  events: (id: string) => [...integrationKeys.all, 'events', id] as const,
  mapping: (id: string) => [...integrationKeys.all, 'mapping', id] as const,
  // The org-wide ledger. Filters are IN the key: without them a filtered page would
  // be served from the cache of a differently-filtered one, and the log would answer
  // a question nobody asked.
  stats: (id: string) => [...integrationKeys.all, 'stats', id] as const,
  eventLog: (f: EventLogFilters) => [...integrationKeys.all, 'event-log', f] as const,
} as const;

export function useLeadSources() {
  return useQuery({
    queryKey: integrationKeys.lists(),
    queryFn: api.listSources,
    // Returning from the detail page must not show a stale status/last-used.
    refetchOnMount: 'always',
  });
}

export function useLeadSource(id: string | undefined) {
  return useQuery({
    queryKey: integrationKeys.detail(id ?? ''),
    queryFn: () => api.getSource(id as string),
    enabled: Boolean(id),
  });
}

export function useSourceEvents(id: string | undefined) {
  return useQuery({
    queryKey: integrationKeys.events(id ?? ''),
    queryFn: () => api.listEvents(id as string),
    enabled: Boolean(id),
    refetchOnMount: 'always',
  });
}

export function useCreateSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateSourceInput) => api.createSource(input),
    // Only the source half reaches the cache. The plaintext key stays with the
    // caller's component state and dies when the reveal is dismissed.
    onSuccess: ({ source }) => {
      qc.setQueryData(integrationKeys.detail(source.id), source);
      qc.invalidateQueries({ queryKey: integrationKeys.lists() });
    },
  });
}

export function useUpdateSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpdateSourceInput }) =>
      api.updateSource(id, input),
    onSuccess: (source: LeadSource) => {
      qc.setQueryData(integrationKeys.detail(source.id), source);
      qc.invalidateQueries({ queryKey: integrationKeys.lists() });
    },
  });
}

/**
 * useRotateKey is deliberately NOT optimistic: the new secret comes from the
 * server, and fabricating one would flash a fake key in the reveal.
 */
export function useRotateKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.rotateKey(id),
    onSuccess: ({ source }) => {
      qc.setQueryData(integrationKeys.detail(source.id), source);
      qc.invalidateQueries({ queryKey: integrationKeys.lists() });
    },
  });
}

/** Same non-optimistic rule as useRotateKey, for the same reason. */
export function useRotateGoogleKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.rotateGoogleKey(id),
    onSuccess: ({ source }) => {
      qc.setQueryData(integrationKeys.detail(source.id), source);
      qc.invalidateQueries({ queryKey: integrationKeys.lists() });
    },
  });
}

export function useDeleteSource() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteSource(id),
    onSuccess: (_void, id) => {
      // removeQueries, not invalidate: invalidating would immediately refetch a
      // now-deleted id and flash a 404 on the detail page mid-navigate-away.
      qc.removeQueries({ queryKey: integrationKeys.detail(id) });
      qc.removeQueries({ queryKey: integrationKeys.events(id) });
      qc.invalidateQueries({ queryKey: integrationKeys.lists() });
    },
  });
}

/**
 * useSendTestLead invalidates the delivery log ONLY.
 *
 * Not lists()/detail(): a test lead deliberately does not touch the source's
 * last_used_at or clear its failure count, so refetching them would be a lie the
 * cache tells for free. The invalidation set is the honesty decision, in code.
 */
export function useSendTestLead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.sendTestLead(id),
    onSuccess: (_res, id) => {
      qc.invalidateQueries({ queryKey: integrationKeys.events(id) });
    },
  });
}

/** useEventLog reads one page of the org-wide ledger. */
export function useEventLog(filters: EventLogFilters) {
  return useQuery<EventPage>({
    queryKey: integrationKeys.eventLog(filters),
    queryFn: () => api.listEventLog(filters),
    refetchOnMount: 'always',
  });
}

/**
 * useRetryEvent queues one delivery for a provider re-fetch.
 *
 * Invalidates the WHOLE integrations umbrella rather than a precise key set, and that
 * is deliberate. The rows worth retrying are provider deliveries that failed before a
 * source was resolved, so their source_id is null — a targeted
 * `events(sourceId ?? '')` would build a key matching no live query, resolve
 * successfully, and leave the admin looking at a stale row they are about to click
 * again. A retry is rare; correctness is worth more here than the sibling-key
 * optimisation, which exists to stop a rotate from refetching the log.
 */
export function useRetryEvent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (eventId: string) => api.retryEvent(eventId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: integrationKeys.all });
    },
  });
}

/** useSourceStats reads the per-day delivery counts behind the sparkline. */
export function useSourceStats(id: string | undefined) {
  return useQuery({
    queryKey: integrationKeys.stats(id ?? ''),
    queryFn: () => api.listSourceStats(id as string),
    enabled: Boolean(id),
  });
}

export function useMapping(id: string | undefined) {
  return useQuery({
    queryKey: integrationKeys.mapping(id ?? ''),
    queryFn: () => api.getMapping(id as string),
    enabled: Boolean(id),
    refetchOnMount: 'always',
  });
}

export function useSaveMapping() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, field_map }: { id: string; field_map: FieldMap }) =>
      api.saveMapping(id, field_map),
    onSuccess: (source: LeadSource) => {
      qc.setQueryData(integrationKeys.detail(source.id), source);
      qc.invalidateQueries({ queryKey: integrationKeys.mapping(source.id) });
    },
  });
}

export type { IntegrationEvent, LeadSource };
