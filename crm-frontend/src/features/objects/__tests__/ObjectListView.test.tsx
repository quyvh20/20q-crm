import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import type { ObjectSchema, UniformRecord } from '../../../lib/api';

// Mock the API layer so the renderer is exercised without a backend. The whole
// point of P3 is that ONE component renders any object from its schema, so these
// tests drive the same <ObjectListView> with a system object (deal) and a custom
// object (project) and assert both render.
vi.mock('../../../lib/api', () => ({
  getObjectSchema: vi.fn(),
  listObjectRecordsUnified: vi.fn(),
  createObjectRecordUnified: vi.fn(),
  updateObjectRecordUnified: vi.fn(),
  deleteObjectRecordUnified: vi.fn(),
  getTags: vi.fn().mockResolvedValue([]),
  // The create form renders an OwnerPicker for objects that have an owner (U6.3),
  // which loads the member list.
  getWorkspaceMembers: vi.fn().mockResolvedValue([]),
}));

// U3.7: the list gates "+ Add"/Import on the caller's OLS create bit. Tests
// flip individual bits through this map; anything unset stays allowed, so the
// pre-existing rendering tests run unchanged.
let objectAccess: Record<string, boolean> = {};
vi.mock('../../../lib/auth', () => ({
  usePermissions: () => ({
    can: () => true,
    canAccess: (slug: string, action: string) => objectAccess[`${slug}.${action}`] ?? true,
    loaded: true,
  }),
}));

import {
  getObjectSchema,
  listObjectRecordsUnified,
} from '../../../lib/api';
import ObjectListView from '../ObjectListView';

const dealSchema: ObjectSchema = {
  slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
  is_system: true, searchable: false, has_owner: true, display_field: 'title',
  fields: [
    { key: 'title', label: 'Title', type: 'text', is_system: true, required: true },
    { key: 'value', label: 'Value', type: 'number', is_system: true, required: false },
  ],
};

const contactSchema: ObjectSchema = {
  slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#6366f1',
  is_system: true, searchable: false, has_owner: true, display_field: 'name',
  fields: [
    { key: 'name', label: 'Name', type: 'text', is_system: true, required: true },
  ],
};

const projectSchema: ObjectSchema = {
  slug: 'project', label: 'Project', label_plural: 'Projects', icon: '📁', color: '#6B7280',
  is_system: false, searchable: false, has_owner: true, display_field: 'name',
  fields: [
    { key: 'name', label: 'Name', type: 'text', is_system: false, required: true },
    { key: 'status', label: 'Status', type: 'select', options: ['active', 'done'], is_system: false, required: false },
  ],
};

function record(partial: Partial<UniformRecord>): UniformRecord {
  return {
    id: crypto.randomUUID(), object: 'x', display: '', fields: {},
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    ...partial,
  };
}

// Probe that surfaces the current route so navigation can be asserted.
function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.pathname}</div>;
}

function renderView(slug: string) {
  return render(
    <MemoryRouter initialEntries={[`/${slug}`]}>
      <ObjectListView slug={slug} />
      <LocationProbe />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  objectAccess = {};
});

describe('ObjectListView renders any object from its schema', () => {
  it('renders a system object (deal)', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ object: 'deal', display: 'Acme renewal', fields: { title: 'Acme renewal', value: 1500 } })],
      next_cursor: undefined,
    });

    renderView('deal');

    expect(await screen.findByRole('heading', { name: 'Deals' })).toBeInTheDocument();
    // "Acme renewal" appears in both the Name cell and the Title field column.
    expect((await screen.findAllByText('Acme renewal')).length).toBeGreaterThan(0);
    // Column headers come from the schema fields.
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Value')).toBeInTheDocument();
  });

  it('renders a custom object (project) through the same component', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ object: 'project', display: 'Apollo', fields: { name: 'Apollo', status: 'active' } })],
      next_cursor: undefined,
    });

    renderView('project');

    expect(await screen.findByRole('heading', { name: 'Projects' })).toBeInTheDocument();
    expect((await screen.findAllByText('Apollo')).length).toBeGreaterThan(0);
    expect(screen.getByText('Status')).toBeInTheDocument();
  });

  it('opens the shared create form from the list', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    renderView('deal');

    fireEvent.click(await screen.findByRole('button', { name: 'Add Deal' }));
    // ObjectForm header + a schema-driven field label appear. (The text shows
    // twice — the Modal's sr-only dialog title and the form's visible header.)
    await waitFor(() => expect(screen.getAllByText('New Deal').length).toBeGreaterThan(0));
    expect(screen.getByText('Create Deal')).toBeInTheDocument();
  });

  it('navigates to the unified record page when a custom-object row is clicked', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ id: 'r9', object: 'project', display: 'Apollo', fields: { name: 'Apollo', status: 'active' } })],
      next_cursor: undefined,
    });

    renderView('project');

    const cell = (await screen.findAllByText('Apollo'))[0];
    fireEvent.click(cell.closest('tr')!);

    await waitFor(() =>
      expect(screen.getByTestId('loc').textContent).toBe('/objects/project/records/r9'),
    );
  });

  it('hides + Add and Import and shows the denied empty-state when create is denied', async () => {
    objectAccess['contact.create'] = false;
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    renderView('contact');

    expect(await screen.findByRole('heading', { name: 'Contacts' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add Contact' })).not.toBeInTheDocument();
    // Import is a contact affordance, so its absence here is the create gate.
    expect(screen.queryByText('Import')).not.toBeInTheDocument();
    // The empty state doesn't tell a create-denied role to click a button it doesn't have.
    expect(await screen.findByText('No contacts to show.')).toBeInTheDocument();
    expect(screen.queryByText(/Click "Add/)).not.toBeInTheDocument();
  });

  it('keeps + Add and Import for a role with create access (contact)', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    renderView('contact');

    expect(await screen.findByRole('button', { name: 'Add Contact' })).toBeInTheDocument();
    expect(screen.getByText('Import')).toBeInTheDocument();
  });

  it('navigates a deal row to the bespoke /deals/:id page', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ id: 'd7', object: 'deal', display: 'Acme renewal', fields: { title: 'Acme renewal', value: 1500 } })],
      next_cursor: undefined,
    });

    renderView('deal');

    const cell = (await screen.findAllByText('Acme renewal'))[0];
    fireEvent.click(cell.closest('tr')!);

    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/deals/d7'));
  });
});
