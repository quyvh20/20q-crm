import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react';
import type { ReactNode } from 'react';
import type { ObjectSchema, PipelineStage, UniformRecord } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  getStages: vi.fn(),
  listObjectRecordsUnified: vi.fn(),
  updateObjectRecordUnified: vi.fn(),
}));

// U3: the board gates dragging on the caller's OLS edit bit. Tests flip bits
// through this map; anything unset stays allowed.
let objectAccess: Record<string, boolean> = {};
vi.mock('../../../lib/auth', () => ({
  usePermissions: () => ({
    can: () => true,
    canAccess: (slug: string, action: string) => objectAccess[`${slug}.${action}`] ?? true,
    loaded: true,
  }),
}));

// jsdom has no layout, so a real pointer drag never resolves a droppable.
// Capture DndContext's onDragEnd and invoke it with a synthetic active/over
// pair — the same payload dnd-kit delivers after a drop. useDraggable /
// useDroppable stay real (they work against dnd-kit's default context).
let dragEnd: ((e: unknown) => void) | undefined;
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>();
  return {
    ...actual,
    DndContext: (props: { onDragEnd?: (e: unknown) => void; children?: ReactNode }) => {
      dragEnd = props.onDragEnd;
      return <div>{props.children}</div>;
    },
    DragOverlay: () => null,
  };
});

import { getStages, listObjectRecordsUnified, updateObjectRecordUnified } from '../../../lib/api';
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
  objectAccess = {};
  dragEnd = undefined;
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

describe('ObjectKanban OLS edit gate (U3)', () => {
  it('disables card dragging when the role lacks edit access, keeping clicks', async () => {
    objectAccess['deal.edit'] = false;
    vi.mocked(getStages).mockResolvedValue(stages);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [rec({ display: 'Acme', fields: { title: 'Acme', stage: 's1' } })],
      next_cursor: undefined,
    });
    const onCardClick = vi.fn();

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={onCardClick} />);

    // useDraggable({ disabled: true }) marks the card aria-disabled.
    const card = (await screen.findByText('Acme')).closest('[role="button"]');
    expect(card).toHaveAttribute('aria-disabled', 'true');
    // The card stays a plain link to the record page.
    fireEvent.click(screen.getByText('Acme'));
    expect(onCardClick).toHaveBeenCalledTimes(1);
  });

  it('keeps cards draggable when the role can edit', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [rec({ display: 'Acme', fields: { title: 'Acme', stage: 's1' } })],
      next_cursor: undefined,
    });

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    const card = (await screen.findByText('Acme')).closest('[role="button"]');
    expect(card).toHaveAttribute('aria-disabled', 'false');
  });

  it('reverts the optimistic move and shows an error when the update fails', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [rec({ id: 'r1', display: 'Acme', fields: { title: 'Acme', stage: 's1' } })],
      next_cursor: undefined,
    });
    vi.mocked(updateObjectRecordUnified).mockRejectedValue(
      new Error("your role can't edit deal records — ask an admin for access"),
    );

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');

    await act(async () => {
      dragEnd!({ active: { id: 'r1' }, over: { id: 's2' } });
    });

    expect(updateObjectRecordUnified).toHaveBeenCalledWith('deal', 'r1', { stage: 's2' });
    // The failure is said out loud instead of a silent snap-back.
    expect(await screen.findByText(/can't edit deal records/)).toBeInTheDocument();
  });
});
