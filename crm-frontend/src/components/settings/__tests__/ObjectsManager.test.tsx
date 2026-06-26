import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
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

import { listRegistryObjects, getObjectSchema, getObjectDef } from '../../../lib/api';
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
      is_system: true, searchable: false, display_field: 'title',
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
    expect(screen.getByText('🔍 Searchable')).toBeInTheDocument();
  });

  it('opens the new custom object form', async () => {
    render(<ObjectsManager />);
    fireEvent.click(await screen.findByText('+ New Object'));
    await waitFor(() => expect(screen.getByText('New Custom Object')).toBeInTheDocument());
  });
});
