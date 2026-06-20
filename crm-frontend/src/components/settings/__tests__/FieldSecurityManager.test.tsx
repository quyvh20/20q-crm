import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';

// Mock the API so the field-security grid is exercised without a backend. This is
// the admin surface that configures the Field-Level Security RecordService
// enforces server-side (P5b).
vi.mock('../../../lib/api', () => ({
  getPermissionGrid: vi.fn(),
  getFieldPermissionGrid: vi.fn(),
  setFieldPermission: vi.fn(),
}));

import {
  getPermissionGrid,
  getFieldPermissionGrid,
  setFieldPermission,
  type PermissionGrid,
  type FieldPermissionGrid,
} from '../../../lib/api';
import FieldSecurityManager from '../FieldSecurityManager';

const OLS_GRID: PermissionGrid = {
  objects: [{ slug: 'deal', label: 'Deal', icon: '💰', is_system: true }],
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
  vi.mocked(setFieldPermission).mockResolvedValue(undefined);
});

describe('FieldSecurityManager — field × role level grid', () => {
  it('renders fields and reflects the configured levels (hidden cell, edit default)', async () => {
    render(<FieldSecurityManager />);

    expect(await screen.findByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Amount')).toBeInTheDocument();

    // viewer is restricted on Amount (hidden); unconfigured Title defaults to edit.
    expect((screen.getByLabelText('viewer Amount') as HTMLSelectElement).value).toBe('hidden');
    expect((screen.getByLabelText('viewer Title') as HTMLSelectElement).value).toBe('edit');
  });

  it('changing a level saves it with the object slug, role, field and level', async () => {
    render(<FieldSecurityManager />);
    const titleForViewer = await screen.findByLabelText('viewer Title');

    fireEvent.change(titleForViewer, { target: { value: 'read' } });

    await waitFor(() => expect(setFieldPermission).toHaveBeenCalledTimes(1));
    expect(setFieldPermission).toHaveBeenCalledWith({
      object_slug: 'deal',
      role_id: 'r-viewer',
      field_key: 'title',
      level: 'read',
    });
  });

  it('locks the owner column on Edit — owner bypasses FLS', async () => {
    render(<FieldSecurityManager />);
    const ownerAmount = (await screen.findByLabelText('owner Amount')) as HTMLSelectElement;

    expect(ownerAmount.value).toBe('edit');
    expect(ownerAmount.disabled).toBe(true);
  });
});
