import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import type { ObjectSchema, UniformRecord } from '../../lib/api';

// Full record-page mock surface: the page fetches schema + record, the detail
// body resolves relations + embeds RecordRelations, and the inline edit form
// mounts ObjectForm — so every named export those touch must exist here.
vi.mock('../../lib/api', () => ({
  getObjectSchema: vi.fn(),
  getObjectRecordUnified: vi.fn(),
  deleteObjectRecordUnified: vi.fn(),
  updateObjectRecordUnified: vi.fn(),
  createObjectRecordUnified: vi.fn(),
  listObjectRecordsUnified: vi.fn().mockResolvedValue({ records: [] }),
  getStages: vi.fn().mockResolvedValue([]),
  listRecordLinks: vi.fn().mockResolvedValue([]),
  listRecordTags: vi.fn().mockResolvedValue([]),
  listRegistryObjects: vi.fn().mockResolvedValue([]),
  listRecordRelatedLists: vi.fn().mockResolvedValue([]),
  getTags: vi.fn().mockResolvedValue([]),
  addRecordLink: vi.fn(),
  removeRecordLink: vi.fn(),
  addRecordTag: vi.fn(),
  removeRecordTag: vi.fn(),
}));

import {
  getObjectSchema,
  getObjectRecordUnified,
  deleteObjectRecordUnified,
  updateObjectRecordUnified,
} from '../../lib/api';
import ObjectRecordPage from '../ObjectRecordPage';

const contactSchema: ObjectSchema = {
  slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#6366f1',
  is_system: true, searchable: false, display_field: 'name',
  fields: [
    { key: 'name', label: 'Name', type: 'text', is_system: true, required: true },
    { key: 'email', label: 'Email', type: 'text', is_system: true, required: false },
  ],
};

const contactRecord: UniformRecord = {
  id: 'p1', object: 'contact', display: 'Jane Smith',
  fields: { name: 'Jane Smith', email: 'jane@example.com' },
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
};

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.pathname}</div>;
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/objects/contact/records/p1']}>
      <Routes>
        <Route path="/objects/:slug/records/:id" element={<ObjectRecordPage />} />
        <Route path="*" element={<div>elsewhere</div>} />
      </Routes>
      <LocationProbe />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ObjectRecordPage', () => {
  it('loads the record by id from the URL and renders a structured page', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(getObjectRecordUnified).mockResolvedValue(contactRecord);

    renderPage();

    // It fetched the record named in the URL.
    await waitFor(() => expect(getObjectRecordUnified).toHaveBeenCalledWith('contact', 'p1'));

    // Header shows the record title + object label. ("Jane Smith" appears twice:
    // the header h1 and the `name` field row.)
    expect((await screen.findAllByText('Jane Smith')).length).toBeGreaterThan(0);
    expect(screen.getByText('👤 Contact')).toBeInTheDocument();

    // Built-in default "Details" section renders the fields (never blank).
    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.getByText('jane@example.com')).toBeInTheDocument();

    // Edit + Delete actions are present.
    expect(screen.getByText('Edit')).toBeInTheDocument();
    expect(screen.getByText('Delete')).toBeInTheDocument();
  });

  it('deletes the record and returns to the object list', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(getObjectRecordUnified).mockResolvedValue(contactRecord);
    vi.mocked(deleteObjectRecordUnified).mockResolvedValue(undefined as unknown as void);

    renderPage();

    // Header Delete opens the confirm dialog.
    fireEvent.click(await screen.findByText('Delete'));
    await screen.findByText('Delete Contact?');
    // Two "Delete" buttons now exist (header + modal confirm) — click the modal's.
    const deleteButtons = screen.getAllByRole('button', { name: 'Delete' });
    fireEvent.click(deleteButtons[deleteButtons.length - 1]);

    await waitFor(() => expect(deleteObjectRecordUnified).toHaveBeenCalledWith('contact', 'p1'));
    // Contact list path.
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/contacts'));
  });

  it('shows a not-found state when the record fails to load', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(getObjectRecordUnified).mockRejectedValue(new Error('forbidden'));

    renderPage();

    expect(await screen.findByText('forbidden')).toBeInTheDocument();
  });

  it('updates the record in place after an edit is saved', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(getObjectRecordUnified).mockResolvedValue(contactRecord);
    vi.mocked(updateObjectRecordUnified).mockResolvedValue({
      ...contactRecord,
      display: 'Jane Doe',
      fields: { name: 'Jane Doe', email: 'jane@example.com' },
    });

    renderPage();

    fireEvent.click(await screen.findByText('Edit'));
    // ObjectForm opens inline on the page in edit mode.
    const saveBtn = await screen.findByText('Update Contact');
    fireEvent.click(saveBtn);

    await waitFor(() => expect(updateObjectRecordUnified).toHaveBeenCalledWith('contact', 'p1', expect.any(Object)));
    // The header title reflects the saved record without a navigation/reload.
    await waitFor(() => expect(screen.getAllByText('Jane Doe').length).toBeGreaterThan(0));
    // The inline edit form closed, restoring the detail view.
    expect(screen.queryByText('Update Contact')).not.toBeInTheDocument();
  });

  it('keeps the user on the page and shows an error when delete fails', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(getObjectRecordUnified).mockResolvedValue(contactRecord);
    vi.mocked(deleteObjectRecordUnified).mockRejectedValue(new Error('not allowed'));

    renderPage();

    fireEvent.click(await screen.findByText('Delete'));
    await screen.findByText('Delete Contact?');
    const deleteButtons = screen.getAllByRole('button', { name: 'Delete' });
    fireEvent.click(deleteButtons[deleteButtons.length - 1]);

    // Error surfaces in the modal and we did NOT navigate away.
    expect(await screen.findByText('not allowed')).toBeInTheDocument();
    expect(screen.getByTestId('loc').textContent).toBe('/objects/contact/records/p1');
  });
});
