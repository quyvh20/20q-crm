import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import PreferenceCenterPage from '../PreferenceCenterPage';

vi.mock('../publicUnsubApi', () => ({
  fetchPreferenceInfo: vi.fn(),
  submitUnsubscribe: vi.fn(),
}));
import { fetchPreferenceInfo, submitUnsubscribe } from '../publicUnsubApi';

const fetchMock = fetchPreferenceInfo as unknown as ReturnType<typeof vi.fn>;
const submitMock = submitUnsubscribe as unknown as ReturnType<typeof vi.fn>;

const renderAt = (token = 'TOKEN123') =>
  render(
    <MemoryRouter initialEntries={[`/u/${token}`]}>
      <Routes>
        <Route path="/u/:token" element={<PreferenceCenterPage />} />
      </Routes>
    </MemoryRouter>,
  );

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('PreferenceCenterPage', () => {
  it('renders the unsubscribe options and completes a global unsubscribe', async () => {
    fetchMock.mockResolvedValue({ ok: true, orgName: 'Acme Inc', topics: [] });
    submitMock.mockResolvedValue(undefined);
    renderAt();

    // Ready state shows the org and the primary action.
    expect(await screen.findByText(/Acme Inc/)).toBeInTheDocument();
    const btn = screen.getByRole('button', { name: /unsubscribe from all/i });
    fireEvent.click(btn);

    await waitFor(() => expect(screen.getByText(/you’re unsubscribed/i)).toBeInTheDocument());
    // A global unsubscribe sends NO topic id.
    expect(submitMock).toHaveBeenCalledWith('TOKEN123');
  });

  it('offers per-topic opt-down and submits only that topic', async () => {
    fetchMock.mockResolvedValue({ ok: true, orgName: 'Acme Inc', topics: [{ id: 'tp1', name: 'Product news' }] });
    submitMock.mockResolvedValue(undefined);
    renderAt();

    expect(await screen.findByText('Product news')).toBeInTheDocument();
    // The row's own "Unsubscribe" button (not the global one).
    const topicBtn = screen.getAllByRole('button', { name: /^unsubscribe$/i })[0];
    fireEvent.click(topicBtn);

    await waitFor(() => expect(screen.getByText(/preference updated/i)).toBeInTheDocument());
    expect(submitMock).toHaveBeenCalledWith('TOKEN123', 'tp1');
  });

  it('shows an invalid-link state when the token cannot be resolved', async () => {
    fetchMock.mockRejectedValue(new Error('invalid'));
    renderAt('bad');
    expect(await screen.findByText(/link not valid/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /unsubscribe/i })).not.toBeInTheDocument();
  });
});
