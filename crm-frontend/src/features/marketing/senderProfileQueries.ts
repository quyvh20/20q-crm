// React Query data layer for the marketing sender profile + topics (M3).
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  getSenderProfile,
  saveSenderProfile,
  resumeMarketing,
  listTopics,
  createTopic,
  updateTopic,
  deleteTopic,
  type SenderProfile,
  type MarketingTopic,
  type SenderProfileInput,
} from './senderProfileApi';

export const senderProfileKeys = {
  all: ['marketing', 'sender-profile'] as const,
  profile: () => [...senderProfileKeys.all, 'profile'] as const,
  topics: () => [...senderProfileKeys.all, 'topics'] as const,
};

export function useSenderProfile() {
  return useQuery<SenderProfile>({
    queryKey: senderProfileKeys.profile(),
    queryFn: getSenderProfile,
  });
}

export function useSaveSenderProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SenderProfileInput) => saveSenderProfile(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: senderProfileKeys.profile() }),
  });
}

export function useResumeMarketing() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => resumeMarketing(),
    onSuccess: () => qc.invalidateQueries({ queryKey: senderProfileKeys.profile() }),
  });
}

export function useTopics() {
  return useQuery<MarketingTopic[]>({
    queryKey: senderProfileKeys.topics(),
    queryFn: listTopics,
  });
}

export function useCreateTopic() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: { name: string; description: string; opt_in_default: boolean }) => createTopic(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: senderProfileKeys.topics() }),
  });
}

export function useUpdateTopic() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (args: { id: string; name: string; description: string }) =>
      updateTopic(args.id, { name: args.name, description: args.description }),
    onSuccess: () => qc.invalidateQueries({ queryKey: senderProfileKeys.topics() }),
  });
}

export function useDeleteTopic() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteTopic(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: senderProfileKeys.topics() }),
  });
}
