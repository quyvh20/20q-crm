// React Query data layer for sending domains (M2).
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  listDomains,
  addDomain,
  verifyDomain,
  refreshDomain,
  removeDomain,
  type DomainListResponse,
} from './domainsApi';

export const domainKeys = {
  all: ['marketing', 'domains'] as const,
  list: () => [...domainKeys.all, 'list'] as const,
};

export function useDomains() {
  return useQuery<DomainListResponse>({
    queryKey: domainKeys.list(),
    queryFn: listDomains,
  });
}

export function useAddDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { domain: string; tracking_subdomain?: string }) => addDomain(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: domainKeys.all }),
  });
}

export function useVerifyDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => verifyDomain(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: domainKeys.all }),
  });
}

export function useRefreshDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => refreshDomain(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: domainKeys.all }),
  });
}

export function useRemoveDomain() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => removeDomain(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: domainKeys.all }),
  });
}
