// React Query layer for marketing campaign content (M6).
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { listContent, getContent, createContent, updateContent, removeContent, setContentFolder, type CampaignContent, type ContentInput } from './contentApi';
import { savedBlockKeys } from './savedBlocksQueries';

export const contentKeys = {
  all: ['marketing', 'content'] as const,
  list: () => [...contentKeys.all, 'list'] as const,
  detail: (id: string) => [...contentKeys.all, 'detail', id] as const,
};

export function useContentList() {
  return useQuery<CampaignContent[]>({ queryKey: contentKeys.list(), queryFn: listContent });
}

export function useContent(id: string | undefined) {
  return useQuery<CampaignContent>({
    queryKey: contentKeys.detail(id ?? ''),
    queryFn: () => getContent(id as string),
    enabled: !!id,
  });
}

export function useCreateContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ContentInput) => createContent(input),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: contentKeys.all });
      // Saving an email changes where synced blocks are USED, so the counts the
      // inspector shows before "update everywhere" are now stale.
      qc.invalidateQueries({ queryKey: savedBlockKeys.all });
    },
  });
}

export function useUpdateContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: ContentInput }) => updateContent(id, input),
    onSuccess: (_d, vars) => {
      qc.invalidateQueries({ queryKey: contentKeys.list() });
      qc.invalidateQueries({ queryKey: contentKeys.detail(vars.id) });
      qc.invalidateQueries({ queryKey: savedBlockKeys.all });
    },
  });
}

export function useSetContentFolder() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, folder }: { id: string; folder: string }) => setContentFolder(id, folder),
    onSuccess: () => qc.invalidateQueries({ queryKey: contentKeys.all }),
  });
}

export function useRemoveContent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => removeContent(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: contentKeys.all }),
  });
}
