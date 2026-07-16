import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { ObjectSummary, ObjectSchema, CustomObjectDef } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listRegistryObjects: vi.fn(),
  getObjectSchema: vi.fn(),
  getObjectDef: vi.fn(),
  createObjectDef: vi.fn(),
  updateObjectDef: vi.fn(),
  deleteObjectDef: vi.fn(),
  createFieldDef: vi.fn(),
  updateFieldDef: vi.fn(),
  deleteFieldDef: vi.fn(),
}));

import { listRegistryObjects, getObjectSchema, getObjectDef, createObjectDef, updateObjectDef } from '../../../lib/api';
import ObjectsManager from '../ObjectsManager';

const summaries: ObjectSummary[] = [
  { slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981', is_system: true, field_count: 7, searchable: false },
  { slug: 'project', label: 'Project', label_plural: 'Projects', icon: '📁', color: '#6B7280', is_system: false, field_count: 2, searchable: false },
];

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(listRegistryObjects).mockResolvedValue(summaries);
});

describe('ObjectsManager — one manager for every object', () => {
  it('lists system and custom objects together with their type badges', async () => {
    render(<ObjectsManager />);
    expect(await screen.findByText('Deal')).toBeInTheDocument();
    expect(screen.getByText('Project')).toBeInTheDocument();
    expect(screen.getByText('Built-in')).toBeInTheDocument();
    expect(screen.getByText('Custom')).toBeInTheDocument();
  });

  it('editing a system object shows native fields read-only and a custom-field adder', async () => {
    const dealSchema: ObjectSchema = {
      slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
      is_system: true, searchable: false, has_owner: true, display_field: 'title',
      fields: [
        { key: 'title', label: 'Title', type: 'text', is_system: true, required: true },
        { key: 'renewal_risk', label: 'Renewal Risk', type: 'select', options: ['low', 'high'], is_system: false, required: false },
      ],
    };
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);

    render(<ObjectsManager />);
    await screen.findByText('Deal');
    // Click the edit (✏️) button in the Deal row.
    fireEvent.click(screen.getAllByTitle('Edit fields')[0]);

    await waitFor(() => expect(screen.getByText('Add a custom field')).toBeInTheDocument());
    expect(screen.getByText('Title')).toBeInTheDocument();
    // The native field is marked Built-in (read-only); at least one such marker shows.
    expect(screen.getAllByText('Built-in').length).toBeGreaterThan(0);
    // The custom field is editable (delete control present).
    expect(screen.getByText('Renewal Risk')).toBeInTheDocument();
  });

  it('editing a custom object opens the full object editor', async () => {
    const projectDef: CustomObjectDef = {
      id: 'p1', org_id: 'o1', slug: 'project', label: 'Project', label_plural: 'Projects',
      icon: '📁', searchable: false, created_at: '', updated_at: '',
      fields: [{ key: 'name', label: 'Name', type: 'text', required: true, position: 0 }],
    } as CustomObjectDef;
    vi.mocked(getObjectDef).mockResolvedValue(projectDef);

    render(<ObjectsManager />);
    await screen.findByText('Project');
    // Edit the custom object (second row's edit button).
    fireEvent.click(screen.getAllByTitle('Edit fields')[1]);

    await waitFor(() => expect(screen.getByText('Edit Project')).toBeInTheDocument());
    expect(screen.getByText('Searchable')).toBeInTheDocument();
  });

  it('opens the new custom object form', async () => {
    render(<ObjectsManager />);
    fireEvent.click(await screen.findByRole('button', { name: 'New Object' }));
    await waitFor(() => expect(screen.getByText('New Custom Object')).toBeInTheDocument());
  });
});

// U3.6: a freshly created object is invisible to any role without an access
// grant — the list view must nudge the admin toward Object Access right then.
// The banner's Link needs a Router, so these tests wrap in MemoryRouter.
describe('ObjectsManager — creation-time access nudge (U3.6)', () => {
  it('shows the access-review banner after creating an object, with a link and a dismiss', async () => {
    vi.mocked(createObjectDef).mockResolvedValue({} as CustomObjectDef);

    render(<MemoryRouter><ObjectsManager /></MemoryRouter>);
    fireEvent.click(await screen.findByRole('button', { name: 'New Object' }));
    await waitFor(() => expect(screen.getByText('New Custom Object')).toBeInTheDocument());

    fireEvent.change(screen.getByPlaceholderText('e.g. Project'), { target: { value: 'Invoice' } });
    fireEvent.click(screen.getByText('Create Object'));

    // Back on the list, the amber banner names the new object and links out.
    expect(await screen.findByText(/was created\. Roles without an access grant won't see it anywhere/)).toBeInTheDocument();
    expect(screen.getByText('Invoice')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Object Access' })).toHaveAttribute('href', '/settings/object-access');

    fireEvent.click(screen.getByLabelText('Dismiss access reminder'));
    expect(screen.queryByText(/was created/)).toBeNull();
  });

  it('does not show the banner after editing an existing object', async () => {
    const projectDef: CustomObjectDef = {
      id: 'p1', org_id: 'o1', slug: 'project', label: 'Project', label_plural: 'Projects',
      icon: '📁', searchable: false, created_at: '', updated_at: '',
      fields: [{ key: 'name', label: 'Name', type: 'text', required: true, position: 0 }],
    } as CustomObjectDef;
    vi.mocked(getObjectDef).mockResolvedValue(projectDef);
    // NumberPrefixEditor loads the schema for the current prefix on mount.
    const projectSchema: ObjectSchema = {
      slug: 'project', label: 'Project', label_plural: 'Projects', icon: '📁', color: '#6B7280',
      is_system: false, searchable: false, has_owner: true, display_field: 'name', fields: [],
    };
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    vi.mocked(updateObjectDef).mockResolvedValue({} as CustomObjectDef);

    render(<MemoryRouter><ObjectsManager /></MemoryRouter>);
    await screen.findByText('Project');
    fireEvent.click(screen.getAllByTitle('Edit fields')[1]);
    await waitFor(() => expect(screen.getByText('Edit Project')).toBeInTheDocument());

    fireEvent.click(screen.getByText('Update Object'));

    // Saving an edit returns to the list without the nudge.
    expect(await screen.findByRole('button', { name: 'New Object' })).toBeInTheDocument();
    expect(screen.queryByText(/was created/)).toBeNull();
    expect(screen.queryByLabelText('Dismiss access reminder')).toBeNull();
  });
});
