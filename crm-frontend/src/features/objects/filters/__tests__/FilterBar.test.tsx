import { describe, expect, it, vi, beforeEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import type { ObjectSchema } from '../../../../lib/api';

// RelationPicker (imported via FilterValueInput) talks to the API when opened;
// none of these tests open it, but the module must resolve.
vi.mock('../../../../lib/api', async () => ({
  listObjectRecordsUnified: vi.fn().mockResolvedValue({ records: [] }),
  getObjectRecordUnified: vi.fn(),
}));

import FilterBar from '../FilterBar';
import { EMPTY_FILTER_STATE, type UiFilterState } from '../filterModel';

const schema = {
  slug: 'contact',
  label: 'Contact',
  label_plural: 'Contacts',
  icon: '👤',
  color: '#6366f1',
  is_system: true,
  searchable: false,
  has_owner: true,
  display_field: 'first_name',
  fields: [],
  filterable_fields: [
    { key: 'email', label: 'Email', type: 'text' },
    { key: 'lead_score', label: 'Lead Score', type: 'number' },
    { key: 'status', label: 'Status', type: 'select', options: ['new', 'won'] },
  ],
  filter_operators: {
    text: ['eq', 'neq', 'contains', 'is_empty', 'is_not_empty'],
    number: ['eq', 'gt', 'lt', 'between', 'is_empty', 'is_not_empty'],
    select: ['eq', 'neq', 'in', 'not_in', 'is_empty', 'is_not_empty'],
  },
} as unknown as ObjectSchema;

const sources = { optionsFor: () => [], targetSlugFor: () => undefined };
const resolveValue = (_f: unknown, v: unknown) => String(v ?? '—');

function renderBar(state: UiFilterState, onChange = vi.fn()) {
  render(
    <FilterBar
      schema={schema}
      state={state}
      onChange={onChange}
      sources={sources}
      resolveValue={resolveValue}
    />,
  );
  return onChange;
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('FilterBar', () => {
  it('builds a condition through the add popover: field → operator → value → Apply', async () => {
    const onChange = renderBar(EMPTY_FILTER_STATE);

    fireEvent.click(screen.getByRole('button', { name: /Filter/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Email/ }));

    // Text fields default to `contains` — the operator a person filtering by
    // email almost always means.
    expect(screen.getByLabelText('Operator')).toHaveValue('contains');

    fireEvent.change(screen.getByLabelText('Email value'), { target: { value: '@gmail' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));

    expect(onChange).toHaveBeenCalledWith({
      match: 'all',
      conditions: [{ field: 'email', operator: 'contains', value: '@gmail' }],
    });
  });

  it('commits a no-value operator the moment it is chosen', async () => {
    const onChange = renderBar(EMPTY_FILTER_STATE);

    fireEvent.click(screen.getByRole('button', { name: /Filter/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Email/ }));
    fireEvent.change(screen.getByLabelText('Operator'), { target: { value: 'is_empty' } });

    expect(onChange).toHaveBeenCalledWith({
      match: 'all',
      conditions: [{ field: 'email', operator: 'is_empty', value: undefined }],
    });
  });

  it('keeps Apply disabled until the condition is complete', async () => {
    renderBar(EMPTY_FILTER_STATE);

    fireEvent.click(screen.getByRole('button', { name: /Filter/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Lead Score/ }));
    fireEvent.change(screen.getByLabelText('Operator'), { target: { value: 'between' } });

    const apply = screen.getByRole('button', { name: 'Apply' });
    expect(apply).toBeDisabled();
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '10' } });
    expect(apply).toBeDisabled();
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '50' } });
    expect(apply).toBeEnabled();
  });

  it('renders pills with remove buttons, and only shows the match toggle at two conditions', () => {
    const two: UiFilterState = {
      match: 'all',
      conditions: [
        { field: 'email', operator: 'contains', value: '@x' },
        { field: 'status', operator: 'eq', value: 'won' },
      ],
    };
    const onChange = renderBar(two);

    expect(screen.getByText(/Email contains @x/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Match any' }));
    expect(onChange).toHaveBeenCalledWith({ ...two, match: 'any' });

    fireEvent.click(screen.getByRole('button', { name: 'Remove filter on Email' }));
    expect(onChange).toHaveBeenCalledWith({ match: 'all', conditions: [two.conditions[1]] });
  });

  it('searches the field picker', async () => {
    renderBar(EMPTY_FILTER_STATE);

    fireEvent.click(screen.getByRole('button', { name: /Filter/ }));
    fireEvent.change(await screen.findByLabelText('Search fields'), { target: { value: 'score' } });

    expect(screen.getByRole('button', { name: /Lead Score/ })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Email/ })).not.toBeInTheDocument();
  });

  it('renders nothing when the schema serves no filter catalog (old server)', () => {
    const bare = { ...schema, filterable_fields: undefined } as unknown as ObjectSchema;
    render(
      <FilterBar
        schema={bare}
        state={EMPTY_FILTER_STATE}
        onChange={vi.fn()}
        sources={sources}
        resolveValue={resolveValue}
      />,
    );
    expect(screen.queryByRole('button', { name: /Filter/ })).not.toBeInTheDocument();
  });
});
