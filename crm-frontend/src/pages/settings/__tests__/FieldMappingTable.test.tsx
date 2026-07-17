import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { MappingView } from '../../../features/integrations/types';

// The mapping table exists so an admin never has to guess what their provider
// calls a field. The delivery log already recorded every payload, so the screen
// offers the REAL keys — a mistyped source key matches nothing and quarantines the
// lead silently, so removing the typing removes the failure.

vi.mock('../../../features/integrations/api', () => ({
  getMapping: vi.fn(),
  saveMapping: vi.fn(),
  listSources: vi.fn(),
  createSource: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  deleteSource: vi.fn(),
  rotateKey: vi.fn(),
  listEvents: vi.fn(),
}));

import { getMapping, saveMapping } from '../../../features/integrations/api';
import FieldMappingTable from '../FieldMappingTable';

const VIEW: MappingView = {
  observed: ['Work Email', 'Full Name', 'company_size'],
  target_fields: [
    { key: 'email', label: 'Email', type: 'text' },
    { key: 'first_name', label: 'First Name', type: 'text' },
    { key: 'phone', label: 'Phone', type: 'text' },
  ],
  field_map: {},
};

function renderTable() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <FieldMappingTable sourceId="s1" />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getMapping).mockResolvedValue(VIEW);
});
afterEach(cleanup);

describe('FieldMappingTable', () => {
  it('surfaces fields the source sends that nothing is capturing', async () => {
    // The feature's whole point: each of these is a field currently being thrown
    // away, and the admin would otherwise never know.
    renderTable();
    expect(await screen.findByText(/sending fields nothing is capturing/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Work Email/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /company_size/ })).toBeInTheDocument();
  });

  it('one click turns an unmapped key into a mapping row', async () => {
    renderTable();
    fireEvent.click(await screen.findByRole('button', { name: /Work Email/ }));
    // The row appears with the source key pre-filled — no typing, so no typo.
    expect(await screen.findByLabelText('Target field for Work Email')).toBeInTheDocument();
  });

  it('offers only fields a lead may actually be written into', async () => {
    // The options come from the allowlist, so ownership and relations can never
    // appear as choices — a mapping that would be refused at write time must not
    // be offerable at config time.
    renderTable();
    fireEvent.click(await screen.findByRole('button', { name: /Work Email/ }));
    const select = await screen.findByLabelText('Target field for Work Email');
    const options = Array.from(select.querySelectorAll('option')).map((o) => o.textContent);
    expect(options).toContain('Email');
    expect(options).not.toContain('owner_user_id');
    expect(options).not.toContain('company');
  });

  it('saves the mapping the admin built', async () => {
    vi.mocked(saveMapping).mockResolvedValue({} as never);
    renderTable();

    fireEvent.click(await screen.findByRole('button', { name: /Work Email/ }));
    fireEvent.change(screen.getByLabelText('Target field for Work Email'), {
      target: { value: 'email' },
    });
    fireEvent.click(screen.getByRole('button', { name: /save mapping/i }));

    await waitFor(() =>
      expect(saveMapping).toHaveBeenCalledWith('s1', {
        'Work Email': { target_key: 'email' },
      }),
    );
  });

  it('shows a rejected mapping ON its row, not as one opaque banner', async () => {
    // The server validates against the target object. "This mapping has problems"
    // is useless without saying WHICH row — that is the difference between a fix
    // and a shrug.
    const err = Object.assign(new Error('this mapping has problems'), {
      details: { 'Work Email': 'unknown field: emial' },
    });
    vi.mocked(saveMapping).mockRejectedValue(err);
    renderTable();

    fireEvent.click(await screen.findByRole('button', { name: /Work Email/ }));
    fireEvent.click(screen.getByRole('button', { name: /save mapping/i }));

    expect(await screen.findByText('unknown field: emial')).toBeInTheDocument();
  });

  it('explains what split_name will actually do', async () => {
    // A transform an admin cannot predict is one they will not trust.
    renderTable();
    fireEvent.click(await screen.findByRole('button', { name: /Full Name/ }));
    fireEvent.change(screen.getByLabelText('Transform for Full Name'), {
      target: { value: 'split_name' },
    });
    expect(await screen.findByText(/Ada Byron King/)).toBeInTheDocument();
  });

  it('says plainly that no mapping means pass-through', async () => {
    // Empty means identity — an admin must not think an empty table = nothing works.
    vi.mocked(getMapping).mockResolvedValue({ ...VIEW, observed: [] });
    renderTable();
    expect(await screen.findByText(/passed through under the name it arrives with/i)).toBeInTheDocument();
  });
});
