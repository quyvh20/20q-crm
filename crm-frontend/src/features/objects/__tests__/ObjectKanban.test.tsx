import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import type { ObjectSchema, PipelineStage, UniformRecord } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  getStages: vi.fn(),
  listObjectRecordsUnified: vi.fn(),
  updateObjectRecordUnified: vi.fn(),
}));

import { getStages, listObjectRecordsUnified } from '../../../lib/api';
import ObjectKanban from '../ObjectKanban';

const dealSchema: ObjectSchema = {
  slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
  is_system: true, searchable: false, display_field: 'title',
  fields: [
    { key: 'title', label: 'Title', type: 'text', is_system: true, required: true },
    { key: 'value', label: 'Value', type: 'number', is_system: true, required: false },
    { key: 'stage', label: 'Stage', type: 'relation', is_system: true, required: false },
  ],
};

const stages: PipelineStage[] = [
  { id: 's1', name: 'New', color: '#3b82f6', position: 0, is_won: false, is_lost: false } as PipelineStage,
  { id: 's2', name: 'Won', color: '#10b981', position: 1, is_won: true, is_lost: false } as PipelineStage,
];

function rec(partial: Partial<UniformRecord>): UniformRecord {
  return {
    id: crypto.randomUUID(), object: 'deal', display: '', fields: {},
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...partial,
  };
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ObjectKanban groups stage-bearing records into columns', () => {
  it('renders one column per stage and places cards by their stage value', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [
        rec({ display: 'Acme', fields: { title: 'Acme', value: 100, stage: 's1' } }),
        rec({ display: 'Globex', fields: { title: 'Globex', value: 200, stage: 's2' } }),
      ],
      next_cursor: undefined,
    });
    const onCardClick = vi.fn();

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={onCardClick} />);

    expect(await screen.findByText('New')).toBeInTheDocument();
    expect(screen.getByText('Won')).toBeInTheDocument();
    expect(screen.getByText('Acme')).toBeInTheDocument();
    expect(screen.getByText('Globex')).toBeInTheDocument();

    fireEvent.click(screen.getByText('Acme'));
    expect(onCardClick).toHaveBeenCalledTimes(1);
    expect(onCardClick.mock.calls[0][0].display).toBe('Acme');
  });

  it('shows an empty-state when there are no stages', async () => {
    vi.mocked(getStages).mockResolvedValue([]);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    await waitFor(() => expect(screen.getByText(/No pipeline stages yet/)).toBeInTheDocument());
  });
});
