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

  it('keeps an empty document empty (the canvas shows its starter gallery)', () => {
    store().hydrate({ name: '', subject: '', preheader: '', scope: [], blocks: [] });
    expect(store().blocks).toHaveLength(0);
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
    store().addBlock('columns', { parentId: 'c', colIndex: 0, index: 0 });
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
  it('subject/preheader/styles edits are undoable; name/scope stay outside history', () => {
    store().setSubject('hello');
    store().setName('nm');
    store().toggleScope('company');
    expect(store().dirty).toBe(true);
    expect(store().past).toHaveLength(1); // only the subject edit
    expect(store().scope).toContain('company');
    store().undo();
    expect(store().subject).toBe('s');
    expect(store().name).toBe('nm'); // name untouched by undo
    store().redo();
    expect(store().subject).toBe('hello');
  });

  it('styles changes round-trip through undo', () => {
    store().patchStyles({ textColor: '#123456' });
    expect(store().styles.textColor).toBe('#123456');
    store().undo();
    expect(store().styles.textColor).toBeUndefined();
  });

  it('markSaved clears dirty', () => {
    store().setSubject('x');
    store().markSaved();
    expect(store().dirty).toBe(false);
  });

  it('undoing back to the save point clears dirty; moving off it sets dirty again', () => {
    store().addBlock('divider');
    store().markSaved();
    store().addBlock('button');
    expect(store().dirty).toBe(true);
    store().undo();
    expect(store().dirty).toBe(false); // back at the saved document
    store().redo();
    expect(store().dirty).toBe(true);
  });

  it('invalidates the save point when its redo branch is discarded', () => {
    store().addBlock('divider');
    store().markSaved(); // save point at depth 1
    store().undo();      // depth 0, dirty
    store().addBlock('button'); // diverge — discards the branch holding the save point
    store().undo();      // back to depth 0... which is NOT the saved doc
    expect(store().dirty).toBe(true);
  });
});

describe('column management', () => {
  it('grows to 4 columns and drops widths when the count changes', () => {
    store().setColumnWidths('c', [33, 67]);
    expect(store().blocks[1].col_widths).toEqual([33, 67]);
    store().setColumnCount('c', 4);
    expect(store().blocks[1].columns).toHaveLength(4);
    expect(store().blocks[1].col_widths).toBeUndefined();
  });

  it('re-picking the active ratio is a full no-op (no phantom history, stays clean)', () => {
    store().setColumnWidths('c', undefined); // already equal widths
    expect(store().past).toHaveLength(0);
    expect(store().dirty).toBe(false);
    store().setColumnWidths('c', [33, 67]);
    store().markSaved();
    store().setColumnWidths('c', [33, 67]); // same value again
    expect(store().dirty).toBe(false);
  });
});

describe('dirty tracking beyond history', () => {
  it('keeps an unsaved rename visible after undoing a block edit', () => {
    store().setName('renamed');
    store().addBlock('divider');
    store().undo();
    expect(store().name).toBe('renamed');
    expect(store().dirty).toBe(true); // the rename is still unsaved
  });

  it('keeps an unsaved scope change visible after undo', () => {
    store().toggleScope('company');
    store().addBlock('divider');
    store().undo();
    expect(store().scope).toContain('company');
    expect(store().dirty).toBe(true);
  });

  it('never reports clean once the save point falls out of the history cap', () => {
    store().addBlock('divider');
    store().markSaved();
    const savedCount = store().blocks.length; // the document the server has
    // Overflow the 100-entry cap so the saved snapshot is dropped from history.
    for (let i = 0; i < 120; i++) store().addBlock('divider');
    // Undo back to the DEPTH that used to be the save point — the trap is that
    // depth now holds a completely different document.
    while (store().past.length > 1) store().undo();
    expect(store().blocks.length).not.toBe(savedCount);
    expect(store().dirty).toBe(true);
  });

  it('applyDraft is undoable and leaves the document dirty', () => {
    store().markSaved();
    store().addBlock('button');
    const withButton = store().blocks.length;
    store().applyDraft({
      name: 'from draft', subject: 'draft subject', preheader: '', scope: [],
      blocks: [{ id: 'd1', type: 'text', text: '<p>draft</p>' }],
    });
    expect(store().blocks.map((b) => b.id)).toEqual(['d1']);
    expect(store().dirty).toBe(true);
    // Restoring never destroys in-session work: undo brings it back.
    store().undo();
    expect(store().blocks).toHaveLength(withButton);
    expect(store().dirty).toBe(true); // name came from the draft, still unsaved
  });
});

describe('AI copilot application', () => {
  it('applyAIEmail replaces the document as ONE undoable step', () => {
    store().markSaved();
    store().applyAIEmail(
      [{ id: 'g1', type: 'heading', text: '<p>Generated</p>', level: 1 }],
      'Generated subject',
      'Generated preheader',
    );
    expect(store().blocks.map((b) => b.id)).toEqual(['g1']);
    expect(store().subject).toBe('Generated subject');
    expect(store().dirty).toBe(true);
    expect(store().past).toHaveLength(1);
    // The canvas is the review gate: one undo restores the user's own design.
    store().undo();
    expect(store().blocks.map((b) => b.id)).toEqual(['a', 'c']);
    expect(store().subject).toBe('s');
    expect(store().dirty).toBe(false);
  });

  it('a draft without a subject leaves the existing one alone', () => {
    store().setSubject('mine');
    store().applyAIEmail([{ id: 'g1', type: 'text', text: '<p>x</p>' }]);
    expect(store().subject).toBe('mine');
  });
});

describe('synced blocks', () => {
  it('unlinkBlock breaks the link without touching the content', () => {
    store().hydrate({
      name: 'n', subject: 's', preheader: 'p', scope: ['contact', 'org', 'campaign'],
      blocks: [
        { id: 'hdr', type: 'text', text: '<p>shared header</p>', ref: 'lib-1' },
        { id: 'c', type: 'columns', columns: [[{ id: 'n1', type: 'text', text: '<p>nested</p>', ref: 'lib-1' }], []] },
      ],
    });
    store().unlinkBlock('hdr');
    expect(store().blocks[0].ref).toBeUndefined();
    expect(store().blocks[0].text).toBe('<p>shared header</p>'); // content untouched
    // The nested instance keeps its own link — unlinking is per instance.
    expect(store().blocks[1].columns![0][0].ref).toBe('lib-1');
    expect(store().past).toHaveLength(1); // undoable
    store().undo();
    expect(store().blocks[0].ref).toBe('lib-1');
  });

  it('unlinks a nested instance through the same api', () => {
    store().hydrate({
      name: 'n', subject: 's', preheader: 'p', scope: ['contact', 'org', 'campaign'],
      blocks: [{ id: 'c', type: 'columns', columns: [[{ id: 'n1', type: 'text', text: '<p>x</p>', ref: 'lib-1' }], []] }],
    });
    store().unlinkBlock('n1');
    expect(store().blocks[0].columns![0][0].ref).toBeUndefined();
  });

  it('unlinking a block that is not linked is a full no-op', () => {
    store().unlinkBlock('a');
    expect(store().past).toHaveLength(0);
    expect(store().dirty).toBe(false);
  });

  it('syncLocalInstances brings every local copy in line after a push', () => {
    store().hydrate({
      name: 'n', subject: 's', preheader: 'p', scope: ['contact', 'org', 'campaign'],
      blocks: [
        { id: 'one', type: 'text', text: '<p>old</p>', ref: 'lib-1' },
        { id: 'plain', type: 'text', text: '<p>mine</p>' },
        { id: 'c', type: 'columns', columns: [[{ id: 'two', type: 'text', text: '<p>old</p>', ref: 'lib-1' }], []] },
      ],
    });
    store().syncLocalInstances('lib-1', { id: 'src', type: 'text', text: '<p>NEW</p>', ref: 'lib-1', cond: { field: 'contact.email', op: 'exists' }, bg: '#000000' });

    // Both instances updated, identities kept.
    expect(store().blocks[0].text).toBe('<p>NEW</p>');
    expect(store().blocks[0].id).toBe('one');
    expect(store().blocks[2].columns![0][0].text).toBe('<p>NEW</p>');
    expect(store().blocks[2].columns![0][0].id).toBe('two');
    // Unrelated content untouched.
    expect(store().blocks[1].text).toBe('<p>mine</p>');
    // Root-only fields must NOT ride into a column (the compiler ignores them
    // there, so a condition would silently show the block to everyone).
    expect(store().blocks[2].columns![0][0].cond).toBeUndefined();
    expect(store().blocks[2].columns![0][0].bg).toBeUndefined();
    // The root instance keeps them.
    expect(store().blocks[0].cond).toBeDefined();
    expect(store().past).toHaveLength(1); // one undoable step
  });

  it('syncLocalInstances is a no-op when nothing links to that ref', () => {
    store().syncLocalInstances('nobody', { id: 'x', type: 'text', text: '<p>n</p>' });
    expect(store().past).toHaveLength(0);
    expect(store().dirty).toBe(false);
  });

  it('duplicating a synced instance keeps the link (it is another instance)', () => {
    store().hydrate({
      name: 'n', subject: 's', preheader: 'p', scope: ['contact', 'org', 'campaign'],
      blocks: [{ id: 'hdr', type: 'text', text: '<p>h</p>', ref: 'lib-1' }],
    });
    store().duplicateBlock('hdr');
    expect(store().blocks).toHaveLength(2);
    expect(store().blocks[1].ref).toBe('lib-1');
    expect(store().blocks[1].id).not.toBe('hdr');
  });
});

describe('save signalling', () => {
  it('markSaved with a matching signature clears dirty', () => {
    store().addBlock('divider');
    const sig = store().docSignature();
    expect(store().markSaved(sig)).toBe(true);
    expect(store().dirty).toBe(false);
  });

  it('edits made while a save is in flight are NOT marked clean', () => {
    store().addBlock('divider');
    const sig = store().docSignature();   // what the request carried
    store().addBlock('button');           // typed while it was travelling
    expect(store().markSaved(sig)).toBe(false);
    expect(store().dirty).toBe(true);
  });
});
