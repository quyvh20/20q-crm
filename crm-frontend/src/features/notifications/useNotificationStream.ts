// Persistent SSE listener for in-app notifications (A6.2). Opens ONE long-lived
// /api/events stream while the app is mounted and, on each `type:"notification"`
// message, pushes the fresh unread count into the badge cache and invalidates the
// inbox list (so an open popover refetches). The backend derives (org, user) from
// the auth token and subscribes the caller's per-user channel, so this hook needs
// no ids — it just filters the stream.
//
// The existing feature-local SSE consumers (deal score, meeting summary, voice)
// open their own short-lived streams on demand; this is the app-global one for
// the header bell. Reconnects with capped backoff if the stream drops.
import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { getAccessToken } from '../../lib/api';
import { notificationKeys } from './queries';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

export function useNotificationStream(enabled: boolean) {
  const qc = useQueryClient();

  useEffect(() => {
    if (!enabled) return;
    const token = getAccessToken();
    if (!token) return;

    let stopped = false;
    let abort: AbortController | null = null;
    let retry = 0;

    const connect = async () => {
      while (!stopped) {
        abort = new AbortController();
        try {
          const res = await fetch(`${API_URL}/api/events`, {
            headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
            credentials: 'include',
            signal: abort.signal,
          });
          if (!res.ok || !res.body) throw new Error(`SSE ${res.status}`);
          retry = 0; // a successful connect resets backoff

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
                if (data.type === 'notification') {
                  if (typeof data.unread_count === 'number') {
                    qc.setQueryData(notificationKeys.unreadCount(), data.unread_count);
                  } else {
                    qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
                  }
                  qc.invalidateQueries({ queryKey: notificationKeys.list() });
                }
              } catch { /* ignore keep-alives / non-JSON frames */ }
            }
          }
        } catch (e: any) {
          if (stopped || e?.name === 'AbortError') return;
        }
        // Stream ended or errored — back off (capped at 30s) and reconnect.
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
  }, [enabled, qc]);
}
