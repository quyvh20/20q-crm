// React Query layer for M8 drip sequences. Progress is polled from the enrolled-run
// rollup (there is no per-tick SSE frame for sequences — the feeder enrolls, it does not
// stream per-recipient send state).
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  listSequences, getSequence, createSequence, cancelSequence, getSequenceProgress,
  isSequenceActive,
  type SequenceEnrollment, type SequenceInput, type SequenceProgress, type SequenceStatus,
} from './sequencesApi';

export const sequenceKeys = {
  all: ['marketing', 'sequences'] as const,
  list: () => [...sequenceKeys.all, 'list'] as const,
  detail: (id: string) => [...sequenceKeys.all, 'detail', id] as const,
  progress: (id: string) => [...sequenceKeys.all, 'progress', id] as const,
};

export function useSequences() {
  return useQuery<SequenceEnrollment[]>({ queryKey: sequenceKeys.list(), queryFn: listSequences });
}

export function useSequence(id: string | undefined) {
  return useQuery<SequenceEnrollment>({
    queryKey: sequenceKeys.detail(id ?? ''),
    queryFn: () => getSequence(id as string),
    enabled: !!id,
    // Self-refresh while feeding so the status the progress poll keys on stays LIVE (it
    // flips to completed/blocked when the feeder drains). Stops once no longer active.
    refetchInterval: (query) => (query.state.data?.status === 'active' ? 5000 : false),
  });
}

/** Polls the enrolled-run rollup while the enrollment is still feeding OR while any
 *  enrolled run is still in flight (a completed feed can still have runs parked in a
 *  delay step). `status` comes from useSequence, which self-refreshes while active — so
 *  once feeding stops this keeps polling only until active reaches 0, then self-terminates
 *  (no unbounded background polling after the sequence finishes). */
export function useSequenceProgress(id: string | undefined, status: SequenceStatus | undefined) {
  return useQuery<SequenceProgress>({
    queryKey: sequenceKeys.progress(id ?? ''),
    queryFn: () => getSequenceProgress(id as string),
    enabled: !!id,
    refetchInterval: (query) => {
      const feeding = status ? isSequenceActive(status) : false;
      const running = (query.state.data?.active ?? 0) > 0;
      return feeding || running ? 5000 : false;
    },
    retry: false,
  });
}

export function useCreateSequence() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SequenceInput) => createSequence(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: sequenceKeys.list() }),
  });
}

export function useCancelSequence() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => cancelSequence(id),
    onSuccess: (_r, id) => {
      qc.invalidateQueries({ queryKey: sequenceKeys.list() });
      qc.invalidateQueries({ queryKey: sequenceKeys.detail(id) });
    },
  });
}
