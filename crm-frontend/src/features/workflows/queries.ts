// React Query data layer for workflows (A3.4). Centralizes server-state fetching
// + cache keys + mutation/invalidation, mirroring the reports feature's use of
// @tanstack/react-query. The new builder (NextBuilder) and WorkflowList consume
// these hooks; the legacy dnd-kit builder still uses the store's own fetch methods
// until A8. Schema + object-fields deliberately stay on the zustand store — they're
// org-level builder config the store already caches and shares with both builders'
// config panels (with bespoke invalidateSchema semantics).

import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import {
  getWorkflows,
  getWorkflow,
  createWorkflow,
  updateWorkflow,
  deleteWorkflow,
  toggleWorkflow,
  testRunWorkflow,
} from './api';
import type { Workflow, WorkflowListResponse, SaveWorkflowPayload } from './types';

export interface WorkflowListParams {
  active?: boolean;
  q?: string;
  page?: number;
  size?: number;
}

// Single source of truth for cache keys so producers (mutations) and consumers
// (queries) can't drift. `lists()` is the invalidation umbrella over every
// page/filter/search variant.
export const workflowKeys = {
  all: ['workflows'] as const,
  lists: () => [...workflowKeys.all, 'list'] as const,
  list: (params: WorkflowListParams) => [...workflowKeys.lists(), params] as const,
  details: () => [...workflowKeys.all, 'detail'] as const,
  detail: (id: string) => [...workflowKeys.details(), id] as const,
};

/** Paginated/filtered workflow list. Keeps prior results visible while the next
 *  page/search loads so the list doesn't flash empty on every keystroke. */
export function useWorkflowsList(params: WorkflowListParams) {
  return useQuery<WorkflowListResponse>({
    queryKey: workflowKeys.list(params),
    queryFn: () => getWorkflows(params),
    placeholderData: keepPreviousData,
    // Always refetch on mount so returning to the list reflects saves made in the
    // builder. Required because the primary create/edit path still runs through the
    // legacy builder (store.save), which doesn't invalidate this cache — until A3.6
    // swaps the route to NextBuilder. keepPreviousData avoids an empty flash while
    // the refetch is in flight.
    refetchOnMount: 'always',
  });
}

/** A single workflow by id. Disabled for the "new"/unsaved case. */
export function useWorkflow(id: string | undefined, opts?: { enabled?: boolean }) {
  return useQuery<Workflow>({
    queryKey: workflowKeys.detail(id ?? ''),
    queryFn: () => getWorkflow(id!),
    enabled: Boolean(id) && id !== 'new' && (opts?.enabled ?? true),
  });
}

/** Create (id null) or update a workflow. On success primes the detail cache and
 *  invalidates every list variant so the index reflects the change. */
export function useSaveWorkflow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: string | null; payload: SaveWorkflowPayload }) =>
      vars.id ? updateWorkflow(vars.id, vars.payload) : createWorkflow(vars.payload),
    onSuccess: (wf) => {
      qc.setQueryData(workflowKeys.detail(wf.id), wf);
      qc.invalidateQueries({ queryKey: workflowKeys.lists() });
    },
  });
}

// Snapshot of every cached list variant, for optimistic rollback.
type ListSnapshot = [readonly unknown[], WorkflowListResponse | undefined][];

function snapshotLists(qc: ReturnType<typeof useQueryClient>): ListSnapshot {
  return qc.getQueriesData<WorkflowListResponse>({ queryKey: workflowKeys.lists() });
}

function restoreLists(qc: ReturnType<typeof useQueryClient>, snapshot: ListSnapshot | undefined) {
  snapshot?.forEach(([key, data]) => qc.setQueryData(key, data));
}

/** Toggle active/inactive with an optimistic in-place flip across all cached list
 *  variants; rolls back on error and reconciles on settle. */
export function useToggleWorkflow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (wf: Workflow) => toggleWorkflow(wf.id),
    onMutate: async (wf) => {
      await qc.cancelQueries({ queryKey: workflowKeys.lists() });
      const snapshot = snapshotLists(qc);
      qc.setQueriesData<WorkflowListResponse>({ queryKey: workflowKeys.lists() }, (old) =>
        old
          ? { ...old, workflows: old.workflows.map((w) => (w.id === wf.id ? { ...w, is_active: !w.is_active } : w)) }
          : old,
      );
      return { snapshot };
    },
    onError: (_e, _wf, ctx) => restoreLists(qc, ctx?.snapshot),
    onSettled: () => qc.invalidateQueries({ queryKey: workflowKeys.lists() }),
  });
}

/** Dry-run a workflow against a sample entity (A3.5). No cache interaction — the
 *  result drives an ephemeral canvas overlay in the builder. */
export function useTestRun() {
  return useMutation({
    mutationFn: (vars: { id: string; body: { contact_id?: string; deal_id?: string; context?: Record<string, unknown> } }) =>
      testRunWorkflow(vars.id, vars.body),
  });
}

/** Delete with optimistic row removal across all cached list variants. */
export function useDeleteWorkflow() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteWorkflow(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: workflowKeys.lists() });
      const snapshot = snapshotLists(qc);
      qc.setQueriesData<WorkflowListResponse>({ queryKey: workflowKeys.lists() }, (old) =>
        old
          ? { ...old, workflows: old.workflows.filter((w) => w.id !== id), total: Math.max(0, old.total - 1) }
          : old,
      );
      return { snapshot };
    },
    onError: (_e, _id, ctx) => restoreLists(qc, ctx?.snapshot),
    onSettled: () => qc.invalidateQueries({ queryKey: workflowKeys.lists() }),
  });
}
