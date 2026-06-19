import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';

// Mock the API so the relationships/tags panel is exercised without a backend.
// The whole point of P4 is that ONE panel relates/tags any object through the
// same endpoints, so we drive it for a deal and assert it reads + writes.
vi.mock('../../../lib/api', () => ({
  listRecordLinks: vi.fn(),
  addRecordLink: vi.fn(),
  removeRecordLink: vi.fn(),
  listRecordTags: vi.fn(),
  addRecordTag: vi.fn(),
  removeRecordTag: vi.fn(),
  listRegistryObjects: vi.fn(),
  listObjectRecordsUnified: vi.fn(),
  getTags: vi.fn(),
}));

import {
  listRecordLinks,
  removeRecordLink,
  listRecordTags,
  addRecordTag,
  removeRecordTag,
  listRegistryObjects,
  getTags,
} from '../../../lib/api';
import RecordRelations from '../RecordRelations';

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(listRegistryObjects).mockResolvedValue([
    { slug: 'company', label: 'Company', label_plural: 'Companies', icon: '🏢', color: '#8B5CF6', is_system: true, field_count: 3 },
  ]);
});

describe('RecordRelations renders + edits links and tags uniformly', () => {
  it('shows tags and resolved relationships', async () => {
    vi.mocked(listRecordTags).mockResolvedValue([{ id: 't1', name: 'VIP', color: '#f00' }]);
    vi.mocked(listRecordLinks).mockResolvedValue([
      { id: 'l1', relation_key: 'account', to_slug: 'company', to_id: 'c1', to_display: 'Acme' },
    ]);
    vi.mocked(getTags).mockResolvedValue([
      { id: 't1', name: 'VIP', color: '#f00' },
      { id: 't2', name: 'Lead', color: '#0f0' },
    ]);

    render(<RecordRelations slug="deal" recordId="d1" />);

    expect(await screen.findByText('VIP')).toBeInTheDocument();
    // Relationship resolves to the target's display, not a raw UUID.
    expect(await screen.findByText('Acme')).toBeInTheDocument();
    expect(screen.getByText('account')).toBeInTheDocument();
    // Only an unapplied tag is offered in the picker.
    expect(screen.getByRole('option', { name: 'Lead' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'VIP' })).not.toBeInTheDocument();
  });

  it('adds a tag through the uniform endpoint', async () => {
    vi.mocked(listRecordTags).mockResolvedValue([]);
    vi.mocked(listRecordLinks).mockResolvedValue([]);
    vi.mocked(getTags).mockResolvedValue([{ id: 't2', name: 'Lead', color: '#0f0' }]);
    vi.mocked(addRecordTag).mockResolvedValue(undefined);

    render(<RecordRelations slug="deal" recordId="d1" />);

    const picker = await screen.findByLabelText('Add tag');
    fireEvent.change(picker, { target: { value: 't2' } });

    await waitFor(() => expect(addRecordTag).toHaveBeenCalledWith('deal', 'd1', 't2'));
  });

  it('removes a relationship by edge id', async () => {
    vi.mocked(listRecordTags).mockResolvedValue([]);
    vi.mocked(listRecordLinks).mockResolvedValue([
      { id: 'l1', relation_key: 'account', to_slug: 'company', to_id: 'c1', to_display: 'Acme' },
    ]);
    vi.mocked(getTags).mockResolvedValue([]);
    vi.mocked(removeRecordLink).mockResolvedValue(undefined);

    render(<RecordRelations slug="deal" recordId="d1" />);

    fireEvent.click(await screen.findByLabelText('Remove link'));
    await waitFor(() => expect(removeRecordLink).toHaveBeenCalledWith('l1'));
  });

  it('removes a tag by id', async () => {
    vi.mocked(listRecordTags).mockResolvedValue([{ id: 't1', name: 'VIP', color: '#f00' }]);
    vi.mocked(listRecordLinks).mockResolvedValue([]);
    vi.mocked(getTags).mockResolvedValue([{ id: 't1', name: 'VIP', color: '#f00' }]);
    vi.mocked(removeRecordTag).mockResolvedValue(undefined);

    render(<RecordRelations slug="deal" recordId="d1" />);

    fireEvent.click(await screen.findByLabelText('Remove tag VIP'));
    await waitFor(() => expect(removeRecordTag).toHaveBeenCalledWith('deal', 'd1', 't1'));
  });
});
