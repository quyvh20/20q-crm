import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Block } from '../blocks';
import { useBuilderStore } from '../builderStore';

const store = () => useBuilderStore.getState();

function seedBlocks(): Block[] {
  return [
    { id: 'a', type: 'text', text: '<p>A</p>' },
    {
      id: 'c',
      type: 'columns',
      columns: [[{ id: 't1', type: 'text', text: '<p>1</p>' }], []],
    },
  ];
}

beforeEach(() => {
  store().reset();
  store().hydrate({ name: 'n', subject: 's', preheader: 'p', scope: ['contact', 'org', 'campaign'], blocks: seedBlocks() });
});

describe('hydrate', () => {
  it('seeds state and clears dirty/history', () => {
    expect(store().name).toBe('n');
    expect(store().dirty).toBe(false);
    expect(store().past).toEqual([]);
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
  });

  it('falls back to one starter text block for an empty document', () => {
    store().hydrate({ name: '', subject: '', preheader: '', scope: [], blocks: [] });
    expect(store().blocks).toHaveLength(1);
    expect(store().blocks[0].type).toBe('text');
    expect(store().scope).toEqual(['contact', 'org', 'campaign']);
  });
});

describe('structural edits', () => {
  it('addBlock appends, selects, dirties, and records history', () => {
    store().addBlock('divider');
    expect(store().blocks).toHaveLength(3);
    expect(store().selectedId).toBe(store().blocks[2].id);
    expect(store().dirty).toBe(true);
    expect(store().past).toHaveLength(1);
  });

  it('addBlock into an illegal target is a full no-op (no phantom history)', () => {
    store().addBlock('spacer', { parentId: 'c', colIndex: 0, index: 0 });
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
    expect(store().past).toHaveLength(0);
    expect(store().dirty).toBe(false);
  });

  it('removeBlock clears a selection that lived inside the removed subtree', () => {
    store().select('t1');
    store().removeBlock('c');
    expect(store().blocks.map((b) => b.id)).toEqual(['a']);
    expect(store().selectedId).toBeNull();
  });

  it('duplicateBlock selects the copy', () => {
    store().duplicateBlock('a');
    expect(store().blocks).toHaveLength(3);
    expect(store().selectedId).toBe(store().blocks[1].id);
    expect(store().blocks[1].id).not.toBe('a');
  });

  it('insertBlocks places a multi-block preset as one history entry', () => {
    store().insertBlocks(
      [
        { id: 'x1', type: 'heading', text: '<p>H</p>', level: 2 },
        { id: 'x2', type: 'text', text: '<p>T</p>' },
      ],
      { parentId: null, colIndex: 0, index: 1 },
    );
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'x1', 'x2', 'c']);
    expect(store().past).toHaveLength(1);
    expect(store().selectedId).toBe('x1');
    store().undo();
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
  });

  it('setColumnCount grows with empty columns and refuses to drop non-empty ones', () => {
    store().setColumnCount('c', 3);
    expect(store().blocks[1].columns).toHaveLength(3);
    // col0 has content; shrinking to 2 drops only the trailing empty column
    store().setColumnCount('c', 2);
    expect(store().blocks[1].columns).toHaveLength(2);
    // now col0 non-empty, col1 empty: shrink below 2 is clamped
    store().setColumnCount('c', 1);
    expect(store().blocks[1].columns).toHaveLength(2);
  });
});

describe('undo/redo', () => {
  it('round-trips a structural edit', () => {
    store().addBlock('divider');
    const after = store().blocks;
    store().undo();
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
    store().redo();
    expect(store().blocks).toEqual(after);
  });

  it('coalesces rapid same-key patches into one undo step', () => {
    vi.useFakeTimers();
    try {
      store().patchBlock('a', { text: '<p>A1</p>' }, 'text:a');
      vi.advanceTimersByTime(100);
      store().patchBlock('a', { text: '<p>A12</p>' }, 'text:a');
      vi.advanceTimersByTime(100);
      store().patchBlock('a', { text: '<p>A123</p>' }, 'text:a');
      expect(store().past).toHaveLength(1);
      store().undo();
      expect(store().blocks[0].text).toBe('<p>A</p>');
    } finally {
      vi.useRealTimers();
    }
  });

  it('starts a new undo step after the coalescing window', () => {
    vi.useFakeTimers();
    try {
      store().patchBlock('a', { text: '<p>A1</p>' }, 'text:a');
      vi.advanceTimersByTime(2000);
      store().patchBlock('a', { text: '<p>A2</p>' }, 'text:a');
      expect(store().past).toHaveLength(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('patches nested blocks through the same api', () => {
    store().patchBlock('t1', { text: '<p>edited</p>' });
    const cols = store().blocks[1].columns!;
    expect(cols[0][0].text).toBe('<p>edited</p>');
  });

  it('drops a dangling selection when undoing past the block creation', () => {
    store().addBlock('button');
    const btnId = store().selectedId!;
    store().undo();
    expect(store().selectedId).toBeNull();
    store().redo();
    expect(store().blocks.some((b) => b.id === btnId)).toBe(true);
  });
});

describe('drag transactions', () => {
  it('one gesture = one undo step', () => {
    store().beginDrag();
    // simulate two intermediate dragover shuffles
    store().setBlocksDuringDrag([store().blocks[1], store().blocks[0]]);
    store().setBlocksDuringDrag([store().blocks[1], store().blocks[0]]);
    store().endDrag(true);
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
    expect(store().past).toHaveLength(0); // landed back where it started ⇒ no step
  });

  it('commit of a real move records exactly one step and undo restores', () => {
    store().beginDrag();
    store().setBlocksDuringDrag([store().blocks[1], store().blocks[0]]);
    store().endDrag(true);
    expect(store().blocks.map((b) => b.id)).toEqual(['c', 'a']);
    expect(store().past).toHaveLength(1);
    store().undo();
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
  });

  it('cancel restores the pre-drag tree', () => {
    store().beginDrag();
    store().setBlocksDuringDrag([]);
    store().endDrag(false);
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
    expect(store().dirty).toBe(false);
  });
});

describe('email settings', () => {
  it('setters dirty the store without touching block history', () => {
    store().setSubject('hello');
    store().setPreheader('ph');
    store().setName('nm');
    store().toggleScope('company');
    expect(store().dirty).toBe(true);
    expect(store().past).toHaveLength(0);
    expect(store().scope).toContain('company');
    store().toggleScope('company');
    expect(store().scope).not.toContain('company');
  });

  it('markSaved clears dirty', () => {
    store().setSubject('x');
    store().markSaved();
    expect(store().dirty).toBe(false);
  });
});
