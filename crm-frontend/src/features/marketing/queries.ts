// React Query data layer for marketing (M1). Centralizes cache keys + fetch +
// mutation/invalidation, mirroring the workflows/reports features. Using react-query
// (not hand-rolled useState/fetch) so two admins editing the suppression list can't
// silently overwrite each other's view.
import { useQuery, useMutation, useQueryClient, keepPreviousData } from '@tanstack/react-query';
import {
  listSuppressions,
  addSuppression,
  removeSuppression,
  getMarketingStatus,
  type SuppressionListParams,
  type SuppressionListResponse,
  type MarketingStatus,
  type AddSuppressionInput,
} from './api';

export const marketingKeys = {
  all: ['marketing'] as const,
  suppressions: () => [...marketingKeys.all, 'suppressions'] as const,
  suppressionList: (p: SuppressionListParams) => [...marketingKeys.suppressions(), p] as const,
  status: (email: string) => [...marketingKeys.all, 'status', email] as const,
};

export function useSuppressions(params: SuppressionListParams) {
  return useQuery<SuppressionListResponse>({
    queryKey: marketingKeys.suppressionList(params),
    queryFn: () => listSuppressions(params),
    placeholderData: keepPreviousData, // no empty flash on each keystroke
  });
}

export function useAddSuppression() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: AddSuppressionInput) => addSuppression(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: marketingKeys.suppressions() }),
  });
}

export function useRemoveSuppression() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => removeSuppression(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: marketingKeys.suppressions() }),
  });
}

/** Per-email marketing standing for the contact badge. Disabled when no email;
 *  no retry so a 403 (a non-marketer viewing the contact) just hides the badge. */
export function useMarketingStatus(email: string | undefined) {
  return useQuery<MarketingStatus>({
    queryKey: marketingKeys.status(email ?? ''),
    queryFn: () => getMarketingStatus(email as string),
    enabled: !!email,
    staleTime: 30_000,
    retry: false,
  });
}
