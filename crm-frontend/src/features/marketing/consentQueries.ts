// React Query layer for R1 consent + preflight.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  getGrantableBases, getPreflight, grantLawfulBasis, previewGrant,
  type ConsentGrantRequest, type GrantCounts, type GrantableBasis, type MarketingPreflight,
} from './consentApi';

export const consentKeys = {
  all: ['marketing', 'consent'] as const,
  bases: () => [...consentKeys.all, 'bases'] as const,
  preflight: () => ['marketing', 'preflight'] as const,
};

export function useGrantableBases() {
  return useQuery<GrantableBasis[]>({
    queryKey: consentKeys.bases(),
    queryFn: getGrantableBases,
    // The grantable set changes only with a deploy.
    staleTime: 60 * 60 * 1000,
  });
}

export function usePreflight() {
  return useQuery<MarketingPreflight>({
    queryKey: consentKeys.preflight(),
    queryFn: getPreflight,
    staleTime: 30_000,
  });
}

export function usePreviewGrant() {
  return useMutation<GrantCounts, Error, ConsentGrantRequest>({ mutationFn: previewGrant });
}

export function useGrantLawfulBasis() {
  const qc = useQueryClient();
  return useMutation<GrantCounts, Error, ConsentGrantRequest>({
    mutationFn: grantLawfulBasis,
    onSuccess: () => {
      // A grant changes lawful-basis coverage, which is a preflight check — refresh
      // it so the readiness panel cannot show a stale red row after the fix.
      void qc.invalidateQueries({ queryKey: consentKeys.preflight() });
    },
  });
}
