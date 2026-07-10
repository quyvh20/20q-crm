// React Query data layer for notifications (A6.2). The header bell subscribes to
// useUnreadCount (always mounted) + useNotifications (only while the popover is
// open). The SSE stream (useNotificationStream) pushes fresh state into these
// caches so the bell updates in real time without polling.
import { useQuery, useMutation, useQueryClient, useInfiniteQuery } from '@tanstack/react-query';
import {
  getNotifications,
  getUnreadCount,
  markNotificationRead,
  markAllNotificationsRead,
  type NotificationPage,
} from './api';

export const notificationKeys = {
  all: ['notifications'] as const,
  list: () => [...notificationKeys.all, 'list'] as const,
  unreadCount: () => [...notificationKeys.all, 'unread-count'] as const,
};

/** Unread badge count. Always mounted (the bell is in the header), refreshed by
 *  the SSE stream on each new notification and by mutations. A slow poll is the
 *  safety net if the SSE connection drops. */
export function useUnreadCount() {
  return useQuery({
    queryKey: notificationKeys.unreadCount(),
    queryFn: getUnreadCount,
    staleTime: 30_000,
    refetchInterval: 120_000,
  });
}

/** The inbox list — infinite/cursor paged. Mounted only while the popover is
 *  open (`enabled`) so a closed bell costs nothing. */
export function useNotifications(enabled: boolean) {
  return useInfiniteQuery<NotificationPage>({
    queryKey: notificationKeys.list(),
    queryFn: ({ pageParam }) => getNotifications({ cursor: pageParam as string | undefined, limit: 15 }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    enabled,
    staleTime: 10_000,
  });
}

export function useMarkRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => markNotificationRead(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: notificationKeys.list() });
      qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}

export function useMarkAllRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => markAllNotificationsRead(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: notificationKeys.list() });
      qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}
