// React Query layer for M9 analytics. Engagement for a completed campaign is static, so
// no polling/SSE — a single fetch. Keys namespaced under ['marketing','analytics',…].
import { useQuery } from '@tanstack/react-query';
import {
  getCampaignAnalytics, getDeliverability,
  type CampaignAnalytics, type Deliverability,
} from './analyticsApi';

export const analyticsKeys = {
  all: ['marketing', 'analytics'] as const,
  campaign: (id: string) => [...analyticsKeys.all, 'campaign', id] as const,
  deliverability: () => [...analyticsKeys.all, 'deliverability'] as const,
};

export function useCampaignAnalytics(id: string | undefined, enabled = true) {
  return useQuery<CampaignAnalytics>({
    queryKey: analyticsKeys.campaign(id ?? ''),
    queryFn: () => getCampaignAnalytics(id as string),
    enabled: !!id && enabled,
  });
}

export function useDeliverability() {
  return useQuery<Deliverability>({
    queryKey: analyticsKeys.deliverability(),
    queryFn: getDeliverability,
  });
}
