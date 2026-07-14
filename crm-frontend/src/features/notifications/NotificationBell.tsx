// Header bell + unread badge + Radix popover inbox (A6.2). Token-styled to match
// the light AppLayout header (not the dark builder island). The badge count and
// list are driven by React Query; useNotificationStream keeps them live over SSE.
import { useState } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import * as Popover from '@radix-ui/react-popover';
import { Bell, Check, Loader2, Zap, Settings, Filter } from 'lucide-react';
import { useUnreadCount, useNotifications, useMarkRead, useMarkAllRead } from './queries';
import type { AppNotification } from './api';

/** Compact relative time ("just now", "3m", "2h", "5d") from an ISO timestamp. */
function relativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return '';
  const secs = Math.max(0, Math.floor((Date.now() - then) / 1000));
  if (secs < 45) return 'just now';
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h`;
  const days = Math.floor(hrs / 24);
  if (days < 7) return `${days}d`;
  return new Date(iso).toLocaleDateString();
}

export default function NotificationBell() {
  const [open, setOpen] = useState(false);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const navigate = useNavigate();

  const { data: unread = 0 } = useUnreadCount();
  const list = useNotifications(open, unreadOnly);
  const markRead = useMarkRead();
  const markAllRead = useMarkAllRead();

  const notifications: AppNotification[] = list.data?.pages.flatMap((p) => p.notifications) ?? [];
  const badge = unread > 9 ? '9+' : String(unread);

  const handleClick = (n: AppNotification) => {
    if (!n.read_at) markRead.mutate(n.id);
    if (n.link) {
      setOpen(false);
      navigate(n.link);
    }
  };

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          type="button"
          aria-label={unread > 0 ? `Notifications (${unread} unread)` : 'Notifications'}
          className="relative h-9 w-9 flex items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-accent hover:text-foreground transition-colors"
        >
          <Bell className="h-[18px] w-[18px]" />
          {unread > 0 && (
            <span className="absolute -top-1 -right-1 min-w-[18px] h-[18px] px-1 flex items-center justify-center rounded-full bg-red-500 text-white text-[10px] font-semibold leading-none">
              {badge}
            </span>
          )}
        </button>
      </Popover.Trigger>

      <Popover.Portal>
        <Popover.Content
          align="end"
          sideOffset={8}
          className="z-50 w-[380px] max-w-[calc(100vw-1rem)] rounded-lg border border-border bg-popover text-popover-foreground shadow-lg overflow-hidden"
        >
          <div className="flex items-center justify-between px-4 py-3 border-b border-border">
            <h3 className="text-sm font-semibold">Notifications</h3>
            <Link
              to="/settings/notifications"
              onClick={() => setOpen(false)}
              aria-label="Notification settings"
              title="Notification settings"
              className="text-muted-foreground hover:text-foreground transition-colors"
            >
              <Settings className="h-4 w-4" />
            </Link>
          </div>
          <div className="flex items-center justify-between px-4 py-2 border-b border-border">
            <button
              type="button"
              onClick={() => setUnreadOnly((v) => !v)}
              aria-pressed={unreadOnly}
              className={`text-xs font-medium inline-flex items-center gap-1 transition-colors ${unreadOnly ? 'text-primary' : 'text-muted-foreground hover:text-foreground'}`}
            >
              <Filter className="h-3.5 w-3.5" /> Unread only
            </button>
            {unread > 0 && (
              <button
                type="button"
                onClick={() => markAllRead.mutate()}
                disabled={markAllRead.isPending}
                className="text-xs font-medium text-primary hover:underline disabled:opacity-50 inline-flex items-center gap-1"
              >
                <Check className="h-3.5 w-3.5" /> Mark all read
              </button>
            )}
          </div>

          <div className="max-h-[420px] overflow-y-auto">
            {list.isLoading ? (
              <div className="flex items-center justify-center py-10 text-muted-foreground">
                <Loader2 className="h-5 w-5 animate-spin" />
              </div>
            ) : list.isError ? (
              <div className="px-4 py-10 text-center text-sm text-muted-foreground">
                Couldn't load notifications.
                <button onClick={() => list.refetch()} className="ml-1 text-primary hover:underline">Retry</button>
              </div>
            ) : notifications.length === 0 ? (
              <div className="px-4 py-12 text-center text-sm text-muted-foreground">
                <Bell className="h-8 w-8 mx-auto mb-2 opacity-40" />
                {unreadOnly ? 'No unread notifications.' : "You're all caught up."}
              </div>
            ) : (
              <ul className="divide-y divide-border">
                {notifications.map((n) => (
                  <li key={n.id}>
                    <button
                      type="button"
                      onClick={() => handleClick(n)}
                      className={`w-full text-left px-4 py-3 flex gap-3 transition-colors hover:bg-accent ${n.read_at ? '' : 'bg-primary/[0.04]'}`}
                    >
                      <span className={`mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${n.read_at ? 'bg-muted text-muted-foreground' : 'bg-primary/10 text-primary'}`}>
                        <Zap className="h-3.5 w-3.5" />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2">
                          <span className={`truncate text-sm ${n.read_at ? 'font-medium text-foreground' : 'font-semibold text-foreground'}`}>{n.title}</span>
                          {!n.read_at && <span className="h-2 w-2 shrink-0 rounded-full bg-primary" aria-hidden />}
                        </span>
                        {n.body && <span className="mt-0.5 block text-xs text-muted-foreground line-clamp-2">{n.body}</span>}
                        <span className="mt-1 block text-[11px] text-muted-foreground/70">{relativeTime(n.created_at)}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}

            {list.hasNextPage && (
              <button
                type="button"
                onClick={() => list.fetchNextPage()}
                disabled={list.isFetchingNextPage}
                className="w-full py-3 text-xs font-medium text-primary hover:bg-accent disabled:opacity-50 border-t border-border"
              >
                {list.isFetchingNextPage ? 'Loading…' : 'Load more'}
              </button>
            )}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
