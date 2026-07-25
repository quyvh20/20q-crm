// React Query layer for M7 campaigns + a live SSE progress hook.
import { useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getAccessToken } from '../../lib/api';
import {
  listCampaigns, getCampaign, createCampaign, updateCampaign, deleteCampaign,
  getReadiness, launchCampaign, pauseCampaign, resumeCampaign, cancelCampaign, getProgress,
  isCampaignActive,
  type Campaign, type CampaignInput, type CampaignProgress,
} from './campaignsApi';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

export const campaignKeys = {
  all: ['marketing', 'campaigns'] as const,
  list: () => [...campaignKeys.all, 'list'] as const,
  detail: (id: string) => [...campaignKeys.all, 'detail', id] as const,
  readiness: (id: string) => [...campaignKeys.all, 'readiness', id] as const,
  progress: (id: string) => [...campaignKeys.all, 'progress', id] as const,
};

export function useCampaigns() {
  return useQuery<Campaign[]>({ queryKey: campaignKeys.list(), queryFn: listCampaigns });
}

export function useCampaign(id: string | undefined) {
  return useQuery<Campaign>({
    queryKey: campaignKeys.detail(id ?? ''),
    queryFn: () => getCampaign(id as string),
    enabled: !!id,
  });
}

export function useReadiness(id: string | undefined, enabled = true) {
  return useQuery<import('./campaignsApi').LaunchReadiness>({
    queryKey: campaignKeys.readiness(id ?? ''),
    queryFn: () => getReadiness(id as string),
    enabled: !!id && enabled,
    staleTime: 5_000,
  });
}

/** Polling progress query — the reliable baseline; the SSE hook below pushes instant
 *  updates into the same cache key. Polls only while the campaign is in flight. */
export function useCampaignProgress(id: string | undefined, status: string | undefined) {
  return useQuery<CampaignProgress>({
    queryKey: campaignKeys.progress(id ?? ''),
    queryFn: () => getProgress(id as string),
    enabled: !!id,
    // Key the poll's lifetime on the LIVE polled status (falling back to the passed-in
    // detail status only before the first fetch) so it self-terminates when the
    // campaign finishes, even if SSE never connected to flip the detail cache.
    refetchInterval: (query) => {
      const s = (query.state.data?.status ?? status) as Campaign['status'] | undefined;
      return s && isCampaignActive(s) ? 3000 : false;
    },
    retry: false,
  });
}

export function useCreateCampaign() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CampaignInput) => createCampaign(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: campaignKeys.list() }),
  });
}

export function useUpdateCampaign() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: CampaignInput }) => updateCampaign(id, input),
    onSuccess: (c) => {
      qc.invalidateQueries({ queryKey: campaignKeys.list() });
      qc.invalidateQueries({ queryKey: campaignKeys.detail(c.id) });
      qc.invalidateQueries({ queryKey: campaignKeys.readiness(c.id) });
    },
  });
}

export function useDeleteCampaign() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => deleteCampaign(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: campaignKeys.list() }),
  });
}

function invalidateCampaign(qc: ReturnType<typeof useQueryClient>, id: string) {
  qc.invalidateQueries({ queryKey: campaignKeys.detail(id) });
  qc.invalidateQueries({ queryKey: campaignKeys.list() });
  qc.invalidateQueries({ queryKey: campaignKeys.progress(id) });
}

export function useLaunchCampaign() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => launchCampaign(id),
    onSuccess: (c) => {
      // Seed the detail cache with the returned 'sending' campaign so the editor
      // unmounts and swaps to the monitor synchronously — otherwise the slide re-arms
      // during the refetch and a second slide fires a redundant launch that 409s.
      qc.setQueryData(campaignKeys.detail(c.id), c);
      invalidateCampaign(qc, c.id);
    },
  });
}

export function useCampaignLifecycle(action: 'pause' | 'resume' | 'cancel') {
  const qc = useQueryClient();
  const fn = action === 'pause' ? pauseCampaign : action === 'resume' ? resumeCampaign : cancelCampaign;
  return useMutation({
    mutationFn: (id: string) => fn(id),
    onSuccess: (_r, id) => invalidateCampaign(qc, id),
  });
}

/** Live campaign progress via the org SSE channel (/api/events). Pushes each
 *  campaign_progress frame for THIS campaign into the progress + detail caches, so the
 *  bar and status badge update instantly (polling remains the fallback). Reconnects
 *  with capped backoff — mirrors useNotificationStream. */
export function useCampaignProgressStream(campaignId: string | undefined, enabled: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!enabled || !campaignId) return;

    let stopped = false;
    let abort: AbortController | null = null;
    let retry = 0;

    const connect = async () => {
      while (!stopped) {
        // Re-read the token each attempt (not captured once) so a reconnect after a
        // token rotation uses the fresh token instead of 401-looping forever.
        const token = getAccessToken();
        if (!token) {
          retry = Math.min(retry + 1, 6);
          await new Promise((r) => setTimeout(r, Math.min(1000 * 2 ** retry, 30_000)));
          continue;
        }
        abort = new AbortController();
        try {
          const res = await fetch(`${API_URL}/api/events`, {
            headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
            credentials: 'include',
            signal: abort.signal,
          });
          if (!res.ok || !res.body) throw new Error(`SSE ${res.status}`);
          retry = 0;
          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buffer = '';
          while (!stopped) {
            const { done, value } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split('\n');
            buffer = lines.pop() ?? '';
            for (const line of lines) {
              if (!line.startsWith('data: ')) continue;
              const str = line.slice(6);
              if (str === '') continue;
              try {
                const data = JSON.parse(str);
                if (data.type === 'campaign_progress' && data.campaign_id === campaignId) {
                  // Preserve the last good status if a frame arrives status-less, so a
                  // blank status can't clobber the cache and blank the badge/controls.
                  qc.setQueryData<CampaignProgress>(campaignKeys.progress(campaignId), (prev) => ({
                    status: (data.status || prev?.status || '') as CampaignProgress['status'],
                    counts: data.counts ?? {},
                    total: data.total ?? 0,
                  }));
                  qc.invalidateQueries({ queryKey: campaignKeys.detail(campaignId) });
                }
              } catch { /* ignore keep-alives / non-JSON frames */ }
            }
          }
        } catch (e: any) {
          if (stopped || e?.name === 'AbortError') return;
        }
        if (stopped) return;
        retry = Math.min(retry + 1, 6);
        await new Promise((r) => setTimeout(r, Math.min(1000 * 2 ** retry, 30_000)));
      }
    };

    connect();
    return () => {
      stopped = true;
      abort?.abort();
    };
  }, [enabled, campaignId, qc]);
}
