import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  createSavedBlock, listSavedBlocks, removeSavedBlock, renameSavedBlock,
  savedBlockUsage, updateSavedBlockContent,
} from './savedBlocksApi';
import type { Block } from './composer/blocks';

export const savedBlockKeys: {
  all: readonly ['marketing', 'saved-blocks'];
  usage: (id: string) => readonly unknown[];
} = {
  all: ['marketing', 'saved-blocks'] as const,
  // Nested UNDER `all` so invalidating the library also refreshes usage counts
  // (saving an email changes where synced blocks are used).
  usage: (id: string) => [...savedBlockKeys.all, id, 'usage'] as const,
};

export function useSavedBlocks() {
  return useQuery({ queryKey: savedBlockKeys.all, queryFn: listSavedBlocks });
}

export function useCreateSavedBlock() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, block, synced }: { name: string; block: Block; synced?: boolean }) =>
      createSavedBlock(name, block, synced ?? false),
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

/** Where a synced block is used. Only fetched when an id is given (the inspector
 *  asks for the selected instance's link), and kept fresh-ish rather than cached
 *  hard — the count is a decision input, not decoration. */
export function useSavedBlockUsage(id: string | null) {
  return useQuery({
    queryKey: savedBlockKeys.usage(id ?? 'none'),
    queryFn: () => savedBlockUsage(id as string),
    enabled: !!id,
    staleTime: 15_000,
  });
}

/** Push an instance's content to its library block and propagate everywhere. */
export function useUpdateSavedBlockContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, block }: { id: string; block: Block }) => updateSavedBlockContent(id, block),
    onSuccess: (_res, vars) => {
      qc.invalidateQueries({ queryKey: savedBlockKeys.all });
      qc.invalidateQueries({ queryKey: savedBlockKeys.usage(vars.id) });
      // Other emails were rewritten server-side; their cached copies are stale.
      qc.invalidateQueries({ queryKey: ['marketing', 'content'] });
    },
  });
}
