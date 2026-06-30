import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import type { ObjectSchema, UniformRecord, LayoutSection } from '../../../lib/api';

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
  listRecordRelatedLists: vi.fn().mockResolvedValue([]),
}));

import { getObjectRecordUnified } from '../../../lib/api';
import ObjectDetailView from '../ObjectDetailView';

const dealSchema: ObjectSchema = {
  slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
  is_system: true, searchable: false, display_field: 'title',
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

    render(<ObjectDetailView schema={dealSchema} record={dealRecord} />);

    // The relation resolves to the company's display, not the raw id 'c1'.
    expect(await screen.findByText('Acme Corp')).toBeInTheDocument();
    expect(screen.queryByText('c1')).not.toBeInTheDocument();
    // It asked for the right target record.
    await waitFor(() => expect(getObjectRecordUnified).toHaveBeenCalledWith('company', 'c1'));
  });
});

// ── P8 layout rendering ────────────────────────────────────────────────────

const contactSchema: ObjectSchema = {
  slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#6366f1',
  is_system: true, searchable: false, display_field: 'name',
  fields: [
    { key: 'name',  label: 'Name',  type: 'text', is_system: true, required: true },
    { key: 'email', label: 'Email', type: 'text', is_system: true, required: false },
    { key: 'phone', label: 'Phone', type: 'text', is_system: true, required: false },
    { key: 'notes', label: 'Notes', type: 'text', is_system: false, required: false },
  ],
};

const contactRecord: UniformRecord = {
  id: 'p1', object: 'contact', display: 'Jane Smith',
  fields: { name: 'Jane Smith', email: 'jane@example.com', phone: '555-1234', notes: 'VIP' },
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
};

describe('ObjectDetailView — P8 sectioned layout', () => {
  it('renders section labels when schema.layout is present', async () => {
    const sections: LayoutSection[] = [
      { id: 's1', label: 'Core Info', columns: 1, fields: [{ key: 'name' }, { key: 'email' }] },
      { id: 's2', label: 'Contact Details', columns: 1, fields: [{ key: 'phone' }] },
    ];
    const schema: ObjectSchema = { ...contactSchema, layout: sections };

    render(<ObjectDetailView schema={schema} record={contactRecord} />);

    // Section headings must appear.
    expect(screen.getByText('Core Info')).toBeInTheDocument();
    expect(screen.getByText('Contact Details')).toBeInTheDocument();
    // Fields in the sections appear.
    expect(screen.getByText('Jane Smith')).toBeInTheDocument();
    expect(screen.getByText('jane@example.com')).toBeInTheDocument();
  });

  it('places fields absent from all sections into an "Other" trailing section', async () => {
    const sections: LayoutSection[] = [
      { id: 's1', label: 'Core Info', columns: 1, fields: [{ key: 'name' }, { key: 'email' }] },
      // phone and notes are NOT in any section
    ];
    const schema: ObjectSchema = { ...contactSchema, layout: sections };

    render(<ObjectDetailView schema={schema} record={contactRecord} />);

    expect(screen.getByText('Other')).toBeInTheDocument();
    // The unlisted fields still appear under "Other".
    expect(screen.getByText('555-1234')).toBeInTheDocument();
    expect(screen.getByText('VIP')).toBeInTheDocument();
  });

  it('synthesizes a default "Details" section when no layout is configured', async () => {
    // No layout property — built-in default keeps the page structured, never blank.
    const schema: ObjectSchema = { ...contactSchema };

    render(<ObjectDetailView schema={schema} record={contactRecord} />);

    // The built-in default section heading appears...
    expect(screen.getByText('Details')).toBeInTheDocument();
    // ...there is no "Other" (the default places every field)...
    expect(screen.queryByText('Other')).not.toBeInTheDocument();
    // ...and every field still renders.
    expect(screen.getByText('Jane Smith')).toBeInTheDocument();
    expect(screen.getByText('jane@example.com')).toBeInTheDocument();
    expect(screen.getByText('555-1234')).toBeInTheDocument();
    expect(screen.getByText('VIP')).toBeInTheDocument();
  });

  it('uses the default section when schema.layout is an empty array', async () => {
    const schema: ObjectSchema = { ...contactSchema, layout: [] };

    render(<ObjectDetailView schema={schema} record={contactRecord} />);

    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.queryByText('Other')).not.toBeInTheDocument();
    expect(screen.getByText('Jane Smith')).toBeInTheDocument();
  });
});
