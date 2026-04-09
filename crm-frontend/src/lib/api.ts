const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

async function apiFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const token = localStorage.getItem('access_token');
  const headers: Record<string, string> = {
    ...(options.headers as Record<string, string> || {}),
  };

  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  // Don't set Content-Type for FormData (browser sets boundary automatically)
  if (!(options.body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
  }

  const res = await fetch(`${API_URL}${path}`, { ...options, headers });

  if (res.status === 401) {
    // Try refresh
    const refreshToken = localStorage.getItem('refresh_token');
    if (refreshToken) {
      const refreshRes = await fetch(`${API_URL}/api/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (refreshRes.ok) {
        const data = await refreshRes.json();
        localStorage.setItem('access_token', data.data.access_token);
        localStorage.setItem('refresh_token', data.data.refresh_token);
        headers['Authorization'] = `Bearer ${data.data.access_token}`;
        return fetch(`${API_URL}${path}`, { ...options, headers });
      }
    }
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
    window.location.href = '/login';
  }

  return res;
}

// ============================================================
// Contact types
// ============================================================

export interface Contact {
  id: string;
  org_id: string;
  first_name: string;
  last_name: string;
  email?: string;
  phone?: string;
  company_id?: string;
  owner_user_id?: string;
  custom_fields: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  company?: { id: string; name: string; industry?: string };
  owner?: { id: string; first_name: string; last_name: string };
  tags?: { id: string; name: string; color: string }[];
}

export interface ContactFilter {
  q?: string;
  cursor?: string;
  limit?: number;
  company_id?: string;
  owner_user_id?: string;
  tag_ids?: string[];
}

export interface CursorMeta {
  next_cursor?: string;
  has_more: boolean;
  total: number;
}

export interface ImportResult {
  created: number;
  skipped: number;
  errors: number;
  error_details?: string[];
}

// ============================================================
// Contact API functions
// ============================================================

export async function getContacts(filter: ContactFilter = {}) {
  const params = new URLSearchParams();
  if (filter.q) params.set('q', filter.q);
  if (filter.cursor) params.set('cursor', filter.cursor);
  if (filter.limit) params.set('limit', String(filter.limit));
  if (filter.company_id) params.set('company_id', filter.company_id);
  if (filter.owner_user_id) params.set('owner_user_id', filter.owner_user_id);
  if (filter.tag_ids?.length) params.set('tag_ids', filter.tag_ids.join(','));

  const res = await apiFetch(`/api/contacts?${params.toString()}`);
  const json = await res.json();
  return { contacts: json.data as Contact[], meta: json.meta as CursorMeta };
}

export async function getContact(id: string) {
  const res = await apiFetch(`/api/contacts/${id}`);
  const json = await res.json();
  return json.data as Contact;
}

export async function createContact(data: Partial<Contact> & { tag_ids?: string[] }) {
  const res = await apiFetch('/api/contacts', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create contact');
  return json.data as Contact;
}

export async function updateContact(id: string, data: Partial<Contact> & { tag_ids?: string[] }) {
  const res = await apiFetch(`/api/contacts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update contact');
  return json.data as Contact;
}

export async function deleteContact(id: string) {
  const res = await apiFetch(`/api/contacts/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json();
    throw new Error(json.error || 'Failed to delete contact');
  }
}

export async function importContacts(file: File) {
  const formData = new FormData();
  formData.append('file', file);
  const res = await apiFetch('/api/contacts/import', {
    method: 'POST',
    body: formData,
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Import failed');
  return json.data as ImportResult;
}
