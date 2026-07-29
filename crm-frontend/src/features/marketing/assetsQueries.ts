import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { listAssets, removeAsset, setAssetFolder, uploadAsset } from './assetsApi';

export const assetKeys = {
  all: ['marketing', 'assets'] as const,
};

export function useAssets(enabled = true) {
  return useQuery({ queryKey: assetKeys.all, queryFn: listAssets, enabled });
}

export function useUploadAsset() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ file, folder }: { file: File; folder?: string }) => uploadAsset(file, folder),
    onSuccess: () => qc.invalidateQueries({ queryKey: assetKeys.all }),
  });
}

export function useSetAssetFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, folder }: { id: string; folder: string }) => setAssetFolder(id, folder),
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
