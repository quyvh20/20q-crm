import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { listAssets, removeAsset, uploadAsset } from './assetsApi';

export const assetKeys = {
  all: ['marketing', 'assets'] as const,
};

export function useAssets(enabled = true) {
  return useQuery({ queryKey: assetKeys.all, queryFn: listAssets, enabled });
}

export function useUploadAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: uploadAsset,
    onSuccess: () => qc.invalidateQueries({ queryKey: assetKeys.all }),
  });
}

export function useRemoveAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: removeAsset,
    onSuccess: () => qc.invalidateQueries({ queryKey: assetKeys.all }),
  });
}
