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
  getNotificationPreferences,
  updateNotificationPreferences,
  type NotificationPage,
  type NotificationPreferences,
  type NotificationPreferencesUpdate,
} from './api';

export const notificationKeys = {
  all: ['notifications'] as const,
  // listAll is the invalidation PREFIX; list(unreadOnly) is the concrete key so the
  // all-vs-unread-only bell views cache separately. Invalidating listAll refreshes both.
  listAll: () => [...notificationKeys.all, 'list'] as const,
  list: (unreadOnly = false) => [...notificationKeys.all, 'list', { unreadOnly }] as const,
  unreadCount: () => [...notificationKeys.all, 'unread-count'] as const,
  preferences: () => [...notificationKeys.all, 'preferences'] as const,
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
 *  open (`enabled`) so a closed bell costs nothing. `unreadOnly` folds into the
 *  query key so the two views cache independently (U5 bell toggle). */
export function useNotifications(enabled: boolean, unreadOnly = false) {
  return useInfiniteQuery<NotificationPage>({
    queryKey: notificationKeys.list(unreadOnly),
    queryFn: ({ pageParam }) => getNotifications({ cursor: pageParam as string | undefined, limit: 15, unreadOnly }),
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
      qc.invalidateQueries({ queryKey: notificationKeys.listAll() });
      qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}

export function useMarkAllRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => markAllNotificationsRead(),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: notificationKeys.listAll() });
      qc.invalidateQueries({ queryKey: notificationKeys.unreadCount() });
    },
  });
}

/** The caller's notification preferences (U5) — the preference center. */
export function useNotificationPreferences(enabled = true) {
  return useQuery<NotificationPreferences>({
    queryKey: notificationKeys.preferences(),
    queryFn: getNotificationPreferences,
    enabled,
    staleTime: 60_000,
  });
}

export function useUpdateNotificationPreferences() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: NotificationPreferencesUpdate) => updateNotificationPreferences(input),
    onSuccess: (data) => {
      qc.setQueryData(notificationKeys.preferences(), data);
    },
  });
}
