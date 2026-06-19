import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import type { ObjectSchema, UniformRecord } from '../../../lib/api';

// Mock the API: ObjectDetailView resolves native relation FIELDS to labels via
// getObjectRecordUnified, and embeds RecordRelations (which reads links/tags).
vi.mock('../../../lib/api', () => ({
  getObjectRecordUnified: vi.fn(),
  listRecordLinks: vi.fn().mockResolvedValue([]),
  listRecordTags: vi.fn().mockResolvedValue([]),
  listRegistryObjects: vi.fn().mockResolvedValue([]),
  getTags: vi.fn().mockResolvedValue([]),
  addRecordLink: vi.fn(),
  removeRecordLink: vi.fn(),
  addRecordTag: vi.fn(),
  removeRecordTag: vi.fn(),
  listObjectRecordsUnified: vi.fn().mockResolvedValue({ records: [] }),
}));

import { getObjectRecordUnified } from '../../../lib/api';
import ObjectDetailView from '../ObjectDetailView';

const dealSchema: ObjectSchema = {
  slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
  is_system: true, display_field: 'title',
  fields: [
    { key: 'title', label: 'Title', type: 'text', is_system: true, required: true },
    { key: 'company', label: 'Company', type: 'relation', target_slug: 'company', is_system: true, required: false },
  ],
};

const dealRecord: UniformRecord = {
  id: 'd1', object: 'deal', display: 'Acme renewal',
  fields: { title: 'Acme renewal', company: 'c1' },
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
};

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ObjectDetailView resolves relation fields to labels', () => {
  it('shows the target display instead of a raw UUID', async () => {
    vi.mocked(getObjectRecordUnified).mockResolvedValue({
      id: 'c1', object: 'company', display: 'Acme Corp', fields: {},
      created_at: '', updated_at: '',
    });

    render(<ObjectDetailView schema={dealSchema} record={dealRecord} onEdit={() => {}} onDelete={() => {}} onClose={() => {}} />);

    // The relation resolves to the company's display, not the raw id 'c1'.
    expect(await screen.findByText('Acme Corp')).toBeInTheDocument();
    expect(screen.queryByText('c1')).not.toBeInTheDocument();
    // It asked for the right target record.
    await waitFor(() => expect(getObjectRecordUnified).toHaveBeenCalledWith('company', 'c1'));
  });
});
