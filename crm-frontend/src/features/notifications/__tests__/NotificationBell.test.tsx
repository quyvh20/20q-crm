import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../api', () => ({
  getUnreadCount: vi.fn(),
  getNotifications: vi.fn(),
  markNotificationRead: vi.fn(),
  markAllNotificationsRead: vi.fn(),
}));

import * as api from '../api';
import NotificationBell from '../NotificationBell';

function renderBell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NotificationBell />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('NotificationBell', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (api.getUnreadCount as Mock).mockResolvedValue(0);
    (api.getNotifications as Mock).mockResolvedValue({ notifications: [], unread_count: 0 });
    (api.markAllNotificationsRead as Mock).mockResolvedValue(undefined);
    (api.markNotificationRead as Mock).mockResolvedValue(undefined);
  });

  it('renders the unread badge from the count query', async () => {
    (api.getUnreadCount as Mock).mockResolvedValue(3);
    renderBell();
    expect(await screen.findByText('3')).toBeInTheDocument();
  });

  it('caps the badge at 9+', async () => {
    (api.getUnreadCount as Mock).mockResolvedValue(42);
    renderBell();
    expect(await screen.findByText('9+')).toBeInTheDocument();
  });

  it('shows the empty state when the inbox is empty', async () => {
    renderBell();
    fireEvent.click(screen.getByRole('button', { name: /notifications/i }));
    expect(await screen.findByText(/all caught up/i)).toBeInTheDocument();
  });

  it('lists notifications and marks one read on click', async () => {
    (api.getUnreadCount as Mock).mockResolvedValue(1);
    (api.getNotifications as Mock).mockResolvedValue({
      notifications: [
        { id: 'n1', org_id: 'o', user_id: 'u', type: 'automation', title: 'Deal won', body: 'Acme closed', link: '', created_at: new Date().toISOString(), read_at: null },
      ],
      unread_count: 1,
    });
    renderBell();
    fireEvent.click(await screen.findByRole('button', { name: /notifications/i }));

    const row = await screen.findByText('Deal won');
    fireEvent.click(row);
    await waitFor(() => expect(api.markNotificationRead).toHaveBeenCalledWith('n1'));
  });

  it('marks all read from the header action', async () => {
    (api.getUnreadCount as Mock).mockResolvedValue(2);
    (api.getNotifications as Mock).mockResolvedValue({
      notifications: [
        { id: 'n1', org_id: 'o', user_id: 'u', type: 'automation', title: 'One', body: '', link: '', created_at: new Date().toISOString(), read_at: null },
      ],
      unread_count: 2,
    });
    renderBell();
    fireEvent.click(await screen.findByRole('button', { name: /notifications/i }));
    fireEvent.click(await screen.findByRole('button', { name: /mark all read/i }));
    await waitFor(() => expect(api.markAllNotificationsRead).toHaveBeenCalled());
  });
});
