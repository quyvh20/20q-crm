// React Query data layer for workflows (A3.4). Centralizes server-state fetching
// + cache keys + mutation/invalidation, mirroring the reports feature's use of
// @tanstack/react-query. The builder (NextBuilder) and WorkflowList consume these
// hooks. Schema + object-fields deliberately stay on the zustand store — they're
// org-level builder config the store already caches and shares with the builder's
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
  getEmailTemplates,
  getEmailTemplate,
  createEmailTemplate,
  updateEmailTemplate,
  deleteEmailTemplate,
  testSendEmailTemplate,
  draftWorkflow,
  type WorkflowEditContext,
  type EmailTemplate,
  type EmailTemplateListResponse,
  type SaveEmailTemplateInput,
  type AIDraftResponse,
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
    // Always refetch on mount so returning to the list reflects the latest server
    // state. The save mutation already invalidates this cache, so this is a harmless
    // safety refetch; keepPreviousData avoids an empty flash while it's in flight.
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

// ── Email templates (A5) ──────────────────────────────────────────────────────

export const emailTemplateKeys = {
  all: ['emailTemplates'] as const,
  lists: () => [...emailTemplateKeys.all, 'list'] as const,
  details: () => [...emailTemplateKeys.all, 'detail'] as const,
  detail: (id: string) => [...emailTemplateKeys.details(), id] as const,
};

/** The org's email templates library. */
export function useEmailTemplates() {
  return useQuery<EmailTemplateListResponse>({
    queryKey: emailTemplateKeys.lists(),
    queryFn: getEmailTemplates,
    refetchOnMount: 'always',
  });
}

/** A single email template by id (disabled for the "new"/unsaved case). */
export function useEmailTemplate(id: string | undefined) {
  return useQuery<EmailTemplate>({
    queryKey: emailTemplateKeys.detail(id ?? ''),
    queryFn: () => getEmailTemplate(id!),
    enabled: Boolean(id) && id !== 'new',
  });
}

/** Create (id null) or update a template; primes the detail cache + invalidates the list. */
export function useSaveEmailTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (vars: { id: string | null; input: SaveEmailTemplateInput }) =>
      vars.id ? updateEmailTemplate(vars.id, vars.input) : createEmailTemplate(vars.input),
    onSuccess: (tmpl) => {
      qc.setQueryData(emailTemplateKeys.detail(tmpl.id), tmpl);
      qc.invalidateQueries({ queryKey: emailTemplateKeys.lists() });
    },
  });
}

/** Delete a template (optimistic row removal); rolls back on error. */
export function useDeleteEmailTemplate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteEmailTemplate(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: emailTemplateKeys.lists() });
      const prev = qc.getQueryData<EmailTemplateListResponse>(emailTemplateKeys.lists());
      qc.setQueryData<EmailTemplateListResponse>(emailTemplateKeys.lists(), (old) =>
        old ? { templates: old.templates.filter((t) => t.id !== id), total: Math.max(0, old.total - 1) } : old,
      );
      return { prev };
    },
    onError: (_e, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(emailTemplateKeys.lists(), ctx.prev);
    },
    onSettled: () => qc.invalidateQueries({ queryKey: emailTemplateKeys.lists() }),
  });
}

/** Send a test render of a template to the caller. No cache interaction. */
export function useTestSendEmailTemplate() {
  return useMutation({ mutationFn: (id: string) => testSendEmailTemplate(id) });
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

/** AI copilot (A7.3/A7.4): turn a natural-language prompt into a workflow draft,
 *  optionally editing an existing workflow (`current`). A plain mutation — nothing is
 *  saved server-side, so there's no cache to touch; the caller applies the returned
 *  draft to the builder store. */
export function useDraftWorkflow() {
  return useMutation<AIDraftResponse, Error, { prompt: string; current?: WorkflowEditContext | null }>({
    mutationFn: ({ prompt, current }) => draftWorkflow(prompt, current),
  });
}
