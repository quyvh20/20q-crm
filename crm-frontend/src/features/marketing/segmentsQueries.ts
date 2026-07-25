// React Query layer for M5 audiences. A per-feature key factory + fetch +
// mutation/invalidation, mirroring marketing/queries.ts and the reports feature.
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  listSegments,
  getSegment,
  createSegment,
  updateSegment,
  deleteSegment,
  countSegment,
  listSegmentFields,
  addStaticMembers,
  removeStaticMember,
  type Segment,
  type SegmentInput,
  type SegmentFieldDescriptor,
} from './segmentsApi';

export const segmentKeys = {
  all: ['marketing', 'segments'] as const,
  list: () => [...segmentKeys.all, 'list'] as const,
  detail: (id: string) => [...segmentKeys.all, 'detail', id] as const,
  count: (id: string) => [...segmentKeys.all, 'count', id] as const,
  members: (id: string) => [...segmentKeys.all, 'members', id] as const,
  fields: () => [...segmentKeys.all, 'fields'] as const,
};

export function useSegments() {
  return useQuery<Segment[]>({ queryKey: segmentKeys.list(), queryFn: listSegments });
}

export function useSegment(id: string | undefined) {
  return useQuery<Segment>({
    queryKey: segmentKeys.detail(id ?? ''),
    queryFn: () => getSegment(id as string),
    enabled: !!id,
  });
}

/** Cached member count for the list badge. Short stale window so a just-edited
 *  segment re-counts soon; no retry so a 403 doesn't spin. */
export function useSegmentCount(id: string, enabled = true) {
  return useQuery<number>({
    queryKey: segmentKeys.count(id),
    queryFn: () => countSegment(id),
    enabled,
    staleTime: 30_000,
    retry: false,
  });
}

export function useSegmentFields() {
  return useQuery<SegmentFieldDescriptor[]>({
    queryKey: segmentKeys.fields(),
    queryFn: listSegmentFields,
    staleTime: 5 * 60_000, // the catalog changes rarely
  });
}

export function useCreateSegment() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SegmentInput) => createSegment(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: segmentKeys.list() }),
  });
}

export function useUpdateSegment() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: SegmentInput }) => updateSegment(id, input),
    onSuccess: (seg) => {
      qc.invalidateQueries({ queryKey: segmentKeys.list() });
      qc.invalidateQueries({ queryKey: segmentKeys.detail(seg.id) });
      qc.invalidateQueries({ queryKey: segmentKeys.count(seg.id) });
    },
  });
}

export function useDeleteSegment() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteSegment(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: segmentKeys.list() }),
  });
}

export function useAddStaticMembers(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (contactIds: string[]) => addStaticMembers(id, contactIds),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: segmentKeys.detail(id) });
      qc.invalidateQueries({ queryKey: segmentKeys.count(id) });
      qc.invalidateQueries({ queryKey: segmentKeys.members(id) });
    },
  });
}

export function useRemoveStaticMember(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (contactId: string) => removeStaticMember(id, contactId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: segmentKeys.detail(id) });
      qc.invalidateQueries({ queryKey: segmentKeys.count(id) });
      qc.invalidateQueries({ queryKey: segmentKeys.members(id) });
    },
  });
}
