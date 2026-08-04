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
  is_system: true, searchable: false, has_owner: true, display_field: 'title',
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

describe('ObjectKanban pages the board (R4.1)', () => {
  it('follows next_cursor across pages and shows every card', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    const pages: Record<string, { records: UniformRecord[]; next_cursor?: string }> = {
      '': { records: [rec({ id: 'a', display: 'Acme', fields: { stage: 's1' } })], next_cursor: 'c1' },
      c1: { records: [rec({ id: 'b', display: 'Globex', fields: { stage: 's2' } })], next_cursor: 'c2' },
      c2: { records: [rec({ id: 'c', display: 'Initech', fields: { stage: 's1' } })], next_cursor: undefined },
    };
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (_slug, params) => pages[params?.cursor ?? '']);

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    expect(await screen.findByText('Acme')).toBeInTheDocument();
    expect(await screen.findByText('Globex')).toBeInTheDocument();
    expect(await screen.findByText('Initech')).toBeInTheDocument();
    expect(vi.mocked(listObjectRecordsUnified)).toHaveBeenCalledTimes(3);
    // Nothing was truncated, so no notice.
    expect(screen.queryByText(/Showing the first/)).not.toBeInTheDocument();
  });

  it('stops at the cap and says the board is a prefix, without inventing a total', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    // Every page is full and every page claims another — an unbounded object.
    let n = 0;
    vi.mocked(listObjectRecordsUnified).mockImplementation(async () => ({
      records: Array.from({ length: 100 }, () => rec({ id: `r${n++}`, display: `Deal ${n}`, fields: { stage: 's1' } })),
      next_cursor: `c${n}`,
    }));

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    expect(await screen.findByText('Showing the first 500 — refine with filters.')).toBeInTheDocument();
    // 500 / 100 per page = exactly 5 round trips, then it stops asking.
    expect(vi.mocked(listObjectRecordsUnified)).toHaveBeenCalledTimes(5);
    // No fabricated "of N" — RecordList carries no total.
    expect(screen.queryByText(/of \d/)).not.toBeInTheDocument();
  });

  it('drops duplicate ids across pages so dnd-kit never sees the same draggable twice', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    const dup = rec({ id: 'same', display: 'Acme', fields: { stage: 's1' } });
    const pages: Record<string, { records: UniformRecord[]; next_cursor?: string }> = {
      '': { records: [dup], next_cursor: 'c1' },
      c1: { records: [dup], next_cursor: undefined },
    };
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (_slug, params) => pages[params?.cursor ?? '']);

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    await waitFor(() => expect(vi.mocked(listObjectRecordsUnified)).toHaveBeenCalledTimes(2));
    expect(screen.getAllByText('Acme')).toHaveLength(1);
  });

  it('stops after the page ceiling even when every page repeats the same records', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    // The pathological cursor: it always advances, and always returns records
    // we have already seen. Deduping means the accumulator never grows, so a
    // count-based stop condition alone would loop until the tab died.
    let issued = 0;
    vi.mocked(listObjectRecordsUnified).mockImplementation(async () => {
      issued += 1;
      return {
        records: [rec({ id: 'same', display: 'Acme', fields: { stage: 's1' } })],
        next_cursor: `cursor-${issued}`,
      };
    });

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    await screen.findByText('Acme');
    // The notice counts what is ON the board, not the cap. Five pages that all
    // repeated one record leave exactly one card, and the old copy's literal
    // "500" told the user they were looking at 500 deals.
    await waitFor(() =>
      expect(screen.getByText('Showing the first 1 — refine with filters.')).toBeInTheDocument(),
    );
    expect(screen.queryByText(/Showing the first 500/)).not.toBeInTheDocument();
    expect(vi.mocked(listObjectRecordsUnified)).toHaveBeenCalledTimes(5);
    expect(screen.getAllByText('Acme')).toHaveLength(1);
  });
});

describe('ObjectKanban tells a failure apart from an empty board (R4.3)', () => {
  it('does NOT claim the workspace has no stages when the stage request fails', async () => {
    vi.mocked(getStages).mockRejectedValue(new Error('upstream unavailable (reference: 4b1e)'));
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load the pipeline stages.");
    expect(alert).toHaveTextContent('upstream unavailable (reference: 4b1e)');
    // The old code sent the user to Settings to create stages that already exist.
    expect(screen.queryByText(/No pipeline stages yet/)).not.toBeInTheDocument();
  });

  it('shows the stage failure immediately, without waiting on the record page', async () => {
    vi.mocked(getStages).mockRejectedValue(new Error('stage service down'));
    // The record request never settles. The stage outcome is already known, so
    // the board must not hold "Loading board..." over a decided failure.
    vi.mocked(listObjectRecordsUnified).mockImplementation(
      () => new Promise(() => {}) as ReturnType<typeof listObjectRecordsUnified>,
    );

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load the pipeline stages.");
    expect(alert).toHaveTextContent('stage service down');
    expect(screen.queryByText(/Loading board/)).not.toBeInTheDocument();
  });

  it('keeps the columns but flags the failure when the records request fails', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    vi.mocked(listObjectRecordsUnified).mockRejectedValue(new Error('gateway timeout'));

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load deals.");
    expect(alert).toHaveTextContent('gateway timeout');
    // Stages loaded fine, so the board chrome stays.
    expect(screen.getByText('New')).toBeInTheDocument();
    expect(screen.getByText('Won')).toBeInTheDocument();
  });

  it('re-runs both requests when Retry is clicked', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);
    let down = true;
    vi.mocked(listObjectRecordsUnified).mockImplementation(async () => {
      if (down) throw new Error('gateway timeout');
      return { records: [rec({ id: 'a', display: 'Acme', fields: { stage: 's1' } })], next_cursor: undefined };
    });

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);

    const retry = await screen.findByRole('button', { name: /Retry/ });
    down = false;
    fireEvent.click(retry);

    expect(await screen.findByText('Acme')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
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
    // The failure is said out loud instead of a silent snap-back — and it goes
    // through ErrorState, so it carries role="alert". A hand-rolled div left a
    // screen-reader user with a card that silently jumped back to where it was.
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't move the card.");
    expect(alert).toHaveTextContent(/can't edit deal records/);
  });

  it('reverts only the dragged card, keeping a page that landed mid-drag', async () => {
    vi.mocked(getStages).mockResolvedValue(stages);

    // Page 2 is held open so it can be made to land AFTER the drag starts.
    let releasePage2: (v: { records: UniformRecord[]; next_cursor?: string }) => void = () => {};
    const page2 = new Promise<{ records: UniformRecord[]; next_cursor?: string }>((res) => { releasePage2 = res; });
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (_slug, params) =>
      params?.cursor
        ? page2
        : { records: [rec({ id: 'r1', display: 'Acme', fields: { stage: 's1' } })], next_cursor: 'c1' },
    );

    // Likewise the PATCH: it must still be in flight when page 2 arrives.
    let rejectPatch: (e: Error) => void = () => {};
    vi.mocked(updateObjectRecordUnified).mockImplementation(
      () => new Promise((_res, rej) => { rejectPatch = rej; }) as ReturnType<typeof updateObjectRecordUnified>,
    );

    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');

    // Drag r1 from New -> Won. The PATCH hangs.
    await act(async () => { dragEnd!({ active: { id: 'r1' }, over: { id: 's2' } }); });

    // Page 2 lands while the PATCH is still open.
    await act(async () => {
      releasePage2({ records: [rec({ id: 'r2', display: 'Globex', fields: { stage: 's1' } })], next_cursor: undefined });
      await page2;
    });
    expect(await screen.findByText('Globex')).toBeInTheDocument();

    // Now the PATCH fails.
    await act(async () => {
      rejectPatch(new Error('nope'));
      await Promise.resolve();
    });

    // The revert restores r1's stage WITHOUT discarding the page that arrived
    // in between — the old `setRecords(prev)` snapshot dropped Globex entirely.
    expect(await screen.findByText('nope')).toBeInTheDocument();
    expect(screen.getByText('Globex')).toBeInTheDocument();
    expect(screen.getByText('Acme')).toBeInTheDocument();
    // Both cards are back in "New" (count 2), and "Won" is empty again.
    expect(screen.getByText('New').parentElement).toHaveTextContent(/New\s*2/);
    expect(screen.getByText('Won').parentElement).toHaveTextContent(/Won\s*0/);
  });
});
