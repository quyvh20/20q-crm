import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createSavedBlock, listSavedBlocks, removeSavedBlock, renameSavedBlock } from './savedBlocksApi';
import type { Block } from './composer/blocks';

export const savedBlockKeys = {
  all: ['marketing', 'saved-blocks'] as const,
};

export function useSavedBlocks() {
  return useQuery({ queryKey: savedBlockKeys.all, queryFn: listSavedBlocks });
}

export function useCreateSavedBlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, block }: { name: string; block: Block }) => createSavedBlock(name, block),
    onSuccess: () => qc.invalidateQueries({ queryKey: savedBlockKeys.all }),
  });
}

export function useRenameSavedBlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => renameSavedBlock(id, name),
    onSuccess: () => qc.invalidateQueries({ queryKey: savedBlockKeys.all }),
  });
}

export function useRemoveSavedBlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: removeSavedBlock,
    onSuccess: () => qc.invalidateQueries({ queryKey: savedBlockKeys.all }),
  });
}
