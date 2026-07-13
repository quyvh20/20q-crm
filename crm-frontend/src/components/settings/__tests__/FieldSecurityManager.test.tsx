import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// Mock the API so the field-security grid is exercised without a backend. This is
// the admin surface that configures the Field-Level Security RecordService
// enforces server-side (P5b). CAPABILITY_LABELS is included because lib/roles
// (real, unmocked) imports it from this module.
vi.mock('../../../lib/api', () => ({
  getPermissionGrid: vi.fn(),
  getFieldPermissionGrid: vi.fn(),
  getFieldPermissionSummary: vi.fn(),
  setFieldPermission: vi.fn(),
  bulkSetFieldPermissions: vi.fn(),
  CAPABILITY_LABELS: {},
}));

import {
  getPermissionGrid,
  getFieldPermissionGrid,
  getFieldPermissionSummary,
  setFieldPermission,
  bulkSetFieldPermissions,
  type PermissionGrid,
  type FieldPermissionGrid,
} from '../../../lib/api';
import FieldSecurityManager from '../FieldSecurityManager';

const OLS_GRID: PermissionGrid = {
  objects: [
    { slug: 'deal', label: 'Deal', icon: '💰', is_system: true },
    { slug: 'contact', label: 'Contact', icon: '👤', is_system: true },
  ],
  roles: [],
  matrix: [],
};

const FIELD_GRID: FieldPermissionGrid = {
  slug: 'deal',
  label: 'Deal',
  fields: [
    { key: 'title', label: 'Title', type: 'text', is_system: true },
    { key: 'value', label: 'Amount', type: 'number', is_system: true },
  ],
  roles: [
    { id: 'r-owner', name: 'owner', is_system: true, is_owner: true },
    { id: 'r-viewer', name: 'viewer', is_system: true, is_owner: false },
  ],
  matrix: [{ role_id: 'r-viewer', field_key: 'value', level: 'hidden' }],
};

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(getPermissionGrid).mockResolvedValue(structuredClone(OLS_GRID));
  vi.mocked(getFieldPermissionGrid).mockResolvedValue(structuredClone(FIELD_GRID));
  vi.mocked(getFieldPermissionSummary).mockResolvedValue({});
  vi.mocked(setFieldPermission).mockResolvedValue(undefined);
  vi.mocked(bulkSetFieldPermissions).mockResolvedValue(undefined);
});

// The component reads/writes ?object= via useSearchParams, so it needs a router.
const renderPage = (initialEntry = '/settings/field-access') =>
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <FieldSecurityManager />
    </MemoryRouter>,
  );

describe('FieldSecurityManager — field × role level grid', () => {
  it('renders fields and reflects the configured levels (hidden cell, edit default)', async () => {
    renderPage();

    expect(await screen.findByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Amount')).toBeInTheDocument();

    // viewer is restricted on Amount (hidden); unconfigured Title defaults to edit.
    expect((screen.getByLabelText('Viewer Amount') as HTMLSelectElement).value).toBe('hidden');
    expect((screen.getByLabelText('Viewer Title') as HTMLSelectElement).value).toBe('edit');
  });

  it('changing a level saves it with the object slug, role, field and level', async () => {
    renderPage();
    const titleForViewer = await screen.findByLabelText('Viewer Title');

    fireEvent.change(titleForViewer, { target: { value: 'read' } });

    await waitFor(() => expect(setFieldPermission).toHaveBeenCalledTimes(1));
    expect(setFieldPermission).toHaveBeenCalledWith({
      object_slug: 'deal',
      role_id: 'r-viewer',
      field_key: 'title',
      level: 'read',
    });
  });

  it('renders the owner column as static full access — owner bypasses FLS', async () => {
    renderPage();
    await screen.findByText('Title');

    // No selects for the owner column, one static cell per field instead.
    expect(screen.queryByLabelText('Owner Amount')).toBeNull();
    expect(screen.queryByLabelText('Owner Title')).toBeNull();
    expect(screen.getAllByText('Full access')).toHaveLength(2);
    // Header carries the lock affordance with an accessible name, no emoji.
    expect(screen.getByLabelText('Owner — full access')).toBeInTheDocument();
    // And no bulk control for the owner column.
    expect(screen.queryByLabelText('Set all Owner')).toBeNull();
  });

  it('search filters fields by label and by key, with an N-of-M count', async () => {
    renderPage();
    await screen.findByText('Title');

    const search = screen.getByLabelText('Search fields');
    fireEvent.change(search, { target: { value: 'amo' } }); // label match
    expect(screen.queryByText('Title')).toBeNull();
    expect(screen.getByText('Amount')).toBeInTheDocument();
    expect(screen.getByText('1 of 2 fields')).toBeInTheDocument();

    fireEvent.change(search, { target: { value: 'titl' } }); // key/label match on the other field
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.queryByText('Amount')).toBeNull();

    fireEvent.change(search, { target: { value: 'value' } }); // key-only match ('value' → Amount)
    expect(screen.getByText('Amount')).toBeInTheDocument();
    expect(screen.queryByText('Title')).toBeNull();
  });

  it('"Restricted only" shows only fields with a non-default cell', async () => {
    renderPage();
    await screen.findByText('Title');

    fireEvent.click(screen.getByLabelText('Restricted only'));

    expect(screen.queryByText('Title')).toBeNull(); // no cells → default edit everywhere
    expect(screen.getByText('Amount')).toBeInTheDocument(); // has the hidden cell
    expect(screen.getByText('1 of 2 fields')).toBeInTheDocument();
  });

  it('bulk apply confirms, then sends exactly the visible (filtered) field keys once', async () => {
    renderPage();
    await screen.findByText('Title');

    // Filter down to Amount only, then bulk-set the viewer column to hidden.
    fireEvent.change(screen.getByLabelText('Search fields'), { target: { value: 'amount' } });
    fireEvent.change(screen.getByLabelText('Set all Viewer'), { target: { value: 'hidden' } });

    // The real ConfirmDialog opens, naming role, level and the visible count.
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('Set 1 field to Hidden for Viewer?');
    expect(dialog.textContent).toContain('Members with this role will no longer see these fields.');
    expect(bulkSetFieldPermissions).not.toHaveBeenCalled(); // nothing before confirm
    fireEvent.click(within(dialog).getByRole('button', { name: 'Set to Hidden' }));

    await waitFor(() => expect(bulkSetFieldPermissions).toHaveBeenCalledTimes(1));
    expect(bulkSetFieldPermissions).toHaveBeenCalledWith({
      object_slug: 'deal',
      role_id: 'r-viewer',
      field_keys: ['value'],
      level: 'hidden',
    });
  });

  it('bulk apply patches the local matrix and the object badge without a refetch', async () => {
    renderPage();
    await screen.findByText('Title');

    fireEvent.change(screen.getByLabelText('Set all Viewer'), { target: { value: 'read' } });
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('Set 2 fields to Read for Viewer?');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Set to Read' }));

    await waitFor(() => expect(bulkSetFieldPermissions).toHaveBeenCalledTimes(1));
    expect(bulkSetFieldPermissions).toHaveBeenCalledWith({
      object_slug: 'deal',
      role_id: 'r-viewer',
      field_keys: ['title', 'value'],
      level: 'read',
    });

    // Local matrix patched: both viewer cells now read (Amount's hidden replaced).
    await waitFor(() =>
      expect((screen.getByLabelText('Viewer Title') as HTMLSelectElement).value).toBe('read'),
    );
    expect((screen.getByLabelText('Viewer Amount') as HTMLSelectElement).value).toBe('read');
    // Badge recomputed from the local matrix: 2 restricted cells on deal.
    expect(getFieldPermissionGrid).toHaveBeenCalledTimes(1); // no refetch
    const dealPill = screen.getByRole('tab', { name: /Deal/ });
    expect(within(dealPill).getByText('2')).toBeInTheDocument();
  });

  it('shows restriction-count badges from the summary and the loaded grid', async () => {
    vi.mocked(getFieldPermissionSummary).mockResolvedValue({ contact: 3 });
    renderPage();
    await screen.findByText('Title');

    // Contact's badge comes straight from the summary (its grid never loaded)…
    const contactPill = screen.getByRole('tab', { name: /Contact/ });
    expect(await within(contactPill).findByText('3')).toBeInTheDocument();
    // …while Deal's is derived from its loaded matrix (one restricted cell).
    const dealPill = screen.getByRole('tab', { name: /Deal/ });
    expect(within(dealPill).getByText('1')).toBeInTheDocument();
    // The badge counts restriction CELLS (field × role), not distinct fields —
    // the accessible label says so.
    expect(within(contactPill).getByLabelText('3 field restrictions')).toBeInTheDocument();
    expect(within(dealPill).getByLabelText('1 field restriction')).toBeInTheDocument();
  });

  it('bulk apply over more than 200 fields is chunked into sequential <=200-key calls', async () => {
    const manyFields = Array.from({ length: 201 }, (_, i) => ({
      key: `f${i}`, label: `Field ${i}`, type: 'text', is_system: false,
    }));
    vi.mocked(getFieldPermissionGrid).mockResolvedValue({
      ...structuredClone(FIELD_GRID),
      fields: manyFields,
      matrix: [],
    });
    renderPage();
    await screen.findByText('Field 0');

    fireEvent.change(screen.getByLabelText('Set all Viewer'), { target: { value: 'read' } });
    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('Set 201 fields to Read for Viewer?');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Set to Read' }));

    // The server caps field_keys at 200 per call: 201 visible fields → 2 calls.
    await waitFor(() => expect(bulkSetFieldPermissions).toHaveBeenCalledTimes(2));
    const first = vi.mocked(bulkSetFieldPermissions).mock.calls[0][0];
    const second = vi.mocked(bulkSetFieldPermissions).mock.calls[1][0];
    expect(first.field_keys).toHaveLength(200);
    expect(first.field_keys[0]).toBe('f0');
    expect(first.field_keys[199]).toBe('f199');
    expect(second.field_keys).toEqual(['f200']);
    expect(first).toMatchObject({ object_slug: 'deal', role_id: 'r-viewer', level: 'read' });
    expect(second).toMatchObject({ object_slug: 'deal', role_id: 'r-viewer', level: 'read' });
    expect(getFieldPermissionGrid).toHaveBeenCalledTimes(1); // success path: no refetch
  });

  it('a mid-chunk bulk failure surfaces the error and reloads the grid', async () => {
    const manyFields = Array.from({ length: 201 }, (_, i) => ({
      key: `f${i}`, label: `Field ${i}`, type: 'text', is_system: false,
    }));
    vi.mocked(getFieldPermissionGrid).mockResolvedValue({
      ...structuredClone(FIELD_GRID),
      fields: manyFields,
      matrix: [],
    });
    vi.mocked(bulkSetFieldPermissions)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('boom'));
    renderPage();
    await screen.findByText('Field 0');

    fireEvent.change(screen.getByLabelText('Set all Viewer'), { target: { value: 'hidden' } });
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Set to Hidden' }));

    // Chunk 1 landed, chunk 2 failed: the grid refetches (so the UI shows what
    // actually applied) and the error is surfaced after the reload.
    expect(await screen.findByText('boom')).toBeInTheDocument();
    expect(getFieldPermissionGrid).toHaveBeenCalledTimes(2);
  });

  it('?role= emphasizes that role\'s column; unknown ids highlight nothing', async () => {
    renderPage('/settings/field-access?role=r-viewer');
    await screen.findByText('Title');

    const viewerHeader = screen.getByRole('columnheader', { name: /Viewer/ });
    expect(viewerHeader.className).toContain('bg-blue-500/10');
    const ownerHeader = screen.getByRole('columnheader', { name: /Owner/ });
    expect(ownerHeader.className).not.toContain('bg-blue-500/10');

    cleanup();
    renderPage('/settings/field-access?role=bogus');
    await screen.findByText('Title');
    expect(screen.getByRole('columnheader', { name: /Viewer/ }).className).not.toContain('bg-blue-500/10');
  });

  it('?object= preselects the matching pill, unknown slugs fall back to the first', async () => {
    renderPage('/settings/field-access?object=contact');

    await waitFor(() =>
      expect(screen.getByRole('tab', { name: /Contact/ })).toHaveAttribute('aria-selected', 'true'),
    );
    expect(getFieldPermissionGrid).toHaveBeenCalledWith('contact');

    cleanup();
    vi.mocked(getFieldPermissionGrid).mockClear();
    renderPage('/settings/field-access?object=bogus');

    await waitFor(() =>
      expect(screen.getByRole('tab', { name: /Deal/ })).toHaveAttribute('aria-selected', 'true'),
    );
    expect(getFieldPermissionGrid).toHaveBeenCalledWith('deal');
  });
});
