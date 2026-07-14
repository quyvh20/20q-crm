// REST client for the in-app notification inbox (A6.2). Uses the shared apiFetch
// (bearer token + credentials + 401→refresh) and parseJsonSafe from lib/api. Every
// endpoint is scoped server-side to (org, caller), so there are no ids in these paths.
import { apiFetch, parseJsonSafe } from '../../lib/api';

export interface AppNotification {
  id: string;
  org_id: string;
  user_id: string;
  type: string;
  title: string;
  body: string;
  link: string;
  entity_type?: string;
  entity_id?: string;
  read_at?: string | null;
  created_at: string;
}

export interface NotificationPage {
  notifications: AppNotification[];
  next_cursor?: string;
  unread_count: number;
}

export async function getNotifications(params?: { cursor?: string; limit?: number; unreadOnly?: boolean }): Promise<NotificationPage> {
  const qs = new URLSearchParams();
  if (params?.cursor) qs.set('cursor', params.cursor);
  if (params?.limit) qs.set('limit', String(params.limit));
  if (params?.unreadOnly) qs.set('unread', 'true');
  const res = await apiFetch(`/api/notifications?${qs.toString()}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || json.error || 'Failed to fetch notifications');
  return json.data as NotificationPage;
}

export async function getUnreadCount(): Promise<number> {
  const res = await apiFetch(`/api/notifications/unread-count`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || json.error || 'Failed to fetch unread count');
  return (json.data?.unread_count ?? 0) as number;
}

export async function markNotificationRead(id: string): Promise<void> {
  const res = await apiFetch(`/api/notifications/${id}/read`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error?.message || json.error || 'Failed to mark notification read');
  }
}

export async function markAllNotificationsRead(): Promise<void> {
  const res = await apiFetch(`/api/notifications/read-all`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error?.message || json.error || 'Failed to mark all read');
  }
}

// --- Preferences (U5) ---

export interface NotificationTypePref {
  key: string;
  label: string;
  description: string;
  in_app: boolean;
  email: boolean;
}

export interface NotificationPreferences {
  mute_all: boolean;
  email_digest: 'off' | 'daily';
  types: NotificationTypePref[];
}

// The PUT payload: mute-all + digest are optional; types is a sparse list of the
// rows the user changed (only known catalog keys are honored server-side).
export interface NotificationPreferencesUpdate {
  mute_all?: boolean;
  email_digest?: 'off' | 'daily';
  types?: { key: string; in_app: boolean; email: boolean }[];
}

export async function getNotificationPreferences(): Promise<NotificationPreferences> {
  const res = await apiFetch(`/api/notifications/preferences`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || json.error || 'Failed to load notification preferences');
  return json.data as NotificationPreferences;
}

export async function updateNotificationPreferences(input: NotificationPreferencesUpdate): Promise<NotificationPreferences> {
  const res = await apiFetch(`/api/notifications/preferences`, { method: 'PUT', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || json.error || 'Failed to save notification preferences');
  return json.data as NotificationPreferences;
}
