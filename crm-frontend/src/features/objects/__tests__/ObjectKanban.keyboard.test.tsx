import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react';
import type { ObjectSchema, PipelineStage, UniformRecord } from '../../../lib/api';

// This suite drives the REAL dnd-kit: no DndContext mock, a real KeyboardSensor,
// real collision detection. That is the point — a test that only asserts
// "KeyboardSensor appears in the sensors array" would have passed against the
// no-op `sortableKeyboardCoordinates` getter this board must not use.
//
// The price is layout. jsdom has none, so every rect is 0×0 at 0,0 and every
// droppable would be collapsed on top of every other; the board's geometry is
// stubbed in below, once, and dnd-kit then measures it exactly as it would in a
// browser.
vi.mock('../../../lib/api', () => ({
  getStages: vi.fn(),
  listObjectRecordsUnified: vi.fn(),
  updateObjectRecordUnified: vi.fn(),
}));

let objectAccess: Record<string, boolean> = {};
vi.mock('../../../lib/auth', () => ({
  usePermissions: () => ({
    can: () => true,
    canAccess: (slug: string, action: string) => objectAccess[`${slug}.${action}`] ?? true,
    loaded: true,
  }),
}));

import { getStages, listObjectRecordsUnified, updateObjectRecordUnified } from '../../../lib/api';
import ObjectKanban from '../ObjectKanban';

const dealSchema: ObjectSchema = {
  slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
  is_system: true, searchable: false, has_owner: true, display_field: 'title',
  fields: [
    { key: 'title', label: 'Title', type: 'text', is_system: true, required: true },
    { key: 'stage', label: 'Stage', type: 'relation', is_system: true, required: false },
  ],
};

const stages: PipelineStage[] = [
  { id: 's1', name: 'New', color: '#3b82f6', position: 0, is_won: false, is_lost: false } as PipelineStage,
  { id: 's2', name: 'Won', color: '#10b981', position: 1, is_won: true, is_lost: false } as PipelineStage,
];

const card: UniformRecord = {
  id: 'r1', object: 'deal', display: 'Acme', fields: { title: 'Acme', stage: 's1' },
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
};

function rectOf(left: number, top: number, width: number, height: number): DOMRect {
  return {
    x: left, y: top, left, top, width, height,
    right: left + width, bottom: top + height,
    toJSON: () => ({}),
  } as DOMRect;
}

const CARD_RECT = rectOf(8, 120, 244, 60);
let rects = new WeakMap<Element, DOMRect>();

/** Give the board a geometry jsdom cannot: two 260px columns side by side, the
 *  card sitting inside the left one. dnd-kit reads it back through
 *  getBoundingClientRect when the drag starts. */
function layOutBoard() {
  stages.forEach((s, i) => {
    // <span>{name}</span> -> header row -> column wrapper; the wrapper's second
    // child is the droppable well.
    const wrapper = screen.getByText(s.name).parentElement!.parentElement!;
    rects.set(wrapper.children[1], rectOf(i * 280, 100, 260, 400));
  });
  rects.set(screen.getByText('Acme').closest('[role="button"]')!, CARD_RECT);
}

/** Let dnd-kit's measuring effects and the sensor's deferred keydown listener
 *  (attached in a setTimeout) run. */
async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 25));
  });
}

/** Collect everything the live region says, in order. Reading its text after
 *  the fact only ever shows the LAST announcement — and a drag that starts over
 *  its own column overwrites "Picked up…" within the same tick. */
function transcript(): string[] {
  const spoken: string[] = [];
  const region = screen.getByRole('status');
  new MutationObserver(() => {
    const said = region.textContent?.trim();
    if (said && spoken[spoken.length - 1] !== said) spoken.push(said);
  }).observe(region, { childList: true, subtree: true, characterData: true });
  return spoken;
}

const press = (target: Document | HTMLElement, code: string) =>
  fireEvent.keyDown(target, { key: code === 'Space' ? ' ' : code, code });

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  objectAccess = {};
  vi.mocked(getStages).mockResolvedValue(stages);
  vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [card], next_cursor: undefined });
  vi.mocked(updateObjectRecordUnified).mockResolvedValue(card);
  // jsdom implements neither, and dnd-kit calls both while dragging.
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.scrollTo = vi.fn();
  Element.prototype.scrollBy = vi.fn();

  // Geometry comes from the map layOutBoard fills in. The fallback matters for
  // exactly one node: the DragOverlay clone, which does not exist until the card
  // is picked up and so cannot be stubbed in advance. dnd-kit measures the
  // OVERLAY (not the card) for collisions once one is mounted, and a 0×0 overlay
  // intersects nothing — every drop would resolve to `over: null` for a reason
  // that exists only in jsdom. The clone is a copy of the card, so it gets the
  // card's rect.
  rects = new WeakMap();
  Element.prototype.getBoundingClientRect = function () {
    return rects.get(this) ?? CARD_RECT;
  };
});

describe('ObjectKanban keyboard dragging (R7.6)', () => {
  it('picks a card up from its Move button, walks it right and drops it in the next stage', async () => {
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');
    await settle();
    layOutBoard();
    // The live region is the only feedback a keyboard user gets, so it is part
    // of the feature, not decoration.
    const spoken = transcript();

    const handle = screen.getByRole('button', { name: 'Move Acme to another stage' });
    act(() => handle.focus());
    press(handle, 'Enter');
    await settle();
    // The card is airborne: DragOverlay has mounted a clone of it.
    expect(screen.getAllByText('Acme')).toHaveLength(2);
    // NOTE: the onDragStart announcement ("Picked up Acme, in New…") is never
    // observable — dnd-kit resolves `over` and announces again inside the same
    // React batch, so only the later string is ever committed to the DOM. That
    // is dnd-kit's behaviour, not the board's, and it is why this asserts on the
    // over/end announcements instead of pretending to see the first one.
    expect(spoken[0]).toBe('Acme is over New.');

    // Arrow keys go to the document: that is where the sensor listens once a
    // drag is live.
    press(document, 'ArrowRight');
    await settle();
    expect(spoken).toContain('Acme is over Won.');

    press(document, 'Enter');
    await settle();

    expect(updateObjectRecordUnified).toHaveBeenCalledWith('deal', 'r1', { stage: 's2' });
    expect(spoken[spoken.length - 1]).toBe('Acme was moved to Won.');
    // The card really moved columns, not just the announcement.
    expect(screen.getByText('New').parentElement).toHaveTextContent(/New\s*0/);
    expect(screen.getByText('Won').parentElement).toHaveTextContent(/Won\s*1/);
  });

  it('does not walk off the end of the board', async () => {
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');
    await settle();
    layOutBoard();
    const spoken = transcript();

    const handle = screen.getByRole('button', { name: 'Move Acme to another stage' });
    act(() => handle.focus());
    press(handle, 'Enter');
    await settle();

    // Left from the FIRST column: there is nothing to the left, so the getter
    // returns nothing and the card stays put (the 25px-at-a-time default getter
    // would have drifted it into empty space instead).
    press(document, 'ArrowLeft');
    await settle();
    expect(spoken).not.toContain('Acme is over Won.');

    press(document, 'Enter');
    await settle();
    expect(updateObjectRecordUnified).not.toHaveBeenCalled();
    // ...and it is not announced as a move that never happened.
    expect(spoken[spoken.length - 1]).toBe('Acme was dropped and stayed in New.');
  });

  it('cancels on Escape without writing anything', async () => {
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');
    layOutBoard();

    const handle = screen.getByRole('button', { name: 'Move Acme to another stage' });
    act(() => handle.focus());
    press(handle, 'Space');
    await settle();
    press(document, 'ArrowRight');
    await settle();

    press(document, 'Escape');
    await settle();

    expect(updateObjectRecordUnified).not.toHaveBeenCalled();
    expect(screen.getByRole('status')).toHaveTextContent('Move cancelled. Acme stayed in New.');
    expect(screen.getByText('New').parentElement).toHaveTextContent(/New\s*1/);
  });
});

describe('ObjectKanban card Enter/Space (R7.6)', () => {
  it('opens the record from the card and does NOT start a drag', async () => {
    const onCardClick = vi.fn();
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={onCardClick} />);
    await screen.findByText('Acme');
    layOutBoard();

    const cardEl = screen.getByText('Acme').closest('[role="button"]') as HTMLElement;
    act(() => cardEl.focus());
    press(cardEl, 'Enter');
    await settle();

    expect(onCardClick).toHaveBeenCalledTimes(1);
    // The KeyboardSensor's activator is Enter/Space too. If it were bound to the
    // card as well it would win (it preventDefaults and claims the event), and
    // the record could never be opened from the keyboard.
    expect(screen.getByRole('status')).toHaveTextContent('');

    press(cardEl, 'Space');
    await settle();
    expect(onCardClick).toHaveBeenCalledTimes(2);
    expect(screen.getByRole('status')).toHaveTextContent('');
  });

  it('gives the how-to instructions on the handle, not just the card', async () => {
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');
    await settle();

    const handle = screen.getByRole('button', { name: 'Move Acme to another stage' });
    const describedBy = handle.getAttribute('aria-describedby');
    expect(describedBy).toBeTruthy();
    expect(document.getElementById(describedBy!)).toHaveTextContent(
      /Press Enter or Space on a card to open the record.*left and right arrow keys/s,
    );
  });

  it('does not bury the drag handle inside the card’s role="button"', async () => {
    // STRUCTURE, not behaviour, and deliberately so. dnd-kit stamps
    // role="button" + tabIndex={0} onto the draggable node, so rendering the
    // Move handle as a child of that node puts interactive content inside
    // role="button" — invalid ARIA, and screen readers that flatten such a
    // container never expose the inner control. That handle is the ONLY route
    // into a keyboard drag, so the regression would take the whole R7.6 feature
    // away from precisely the users it was added for. jsdom enforces no nesting
    // rules and happily drives the buried button, which is why every keyboard
    // test above passes either way and this one has to read the tree directly.
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');

    const handle = screen.getByRole('button', { name: 'Move Acme to another stage' });
    expect(handle.parentElement?.closest('button, [role="button"]')).toBeNull();

    // The card is still ONE button that opens the record, with nothing
    // focusable inside it.
    const cardButton = screen.getByText('Acme').closest('[role="button"]') as HTMLElement;
    expect(cardButton).not.toBeNull();
    expect(cardButton.contains(handle)).toBe(false);
    expect(cardButton.querySelector('a, button, input, [role="button"], [tabindex]')).toBeNull();
  });

  it('offers no Move button to a role that cannot edit', async () => {
    objectAccess['deal.edit'] = false;
    render(<ObjectKanban schema={dealSchema} stageKey="stage" onCardClick={vi.fn()} />);
    await screen.findByText('Acme');

    // A drag handle that can only 403 is worse than no handle (U3).
    expect(screen.queryByRole('button', { name: /Move Acme/ })).toBeNull();
  });
});
