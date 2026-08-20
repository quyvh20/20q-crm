import { describe, expect, it } from 'vitest';
import { makeBlock, type Block } from '../blocks';
import {
  allIds,
  canPlaceIn,
  clickInsertAddress,
  cloneWithNewIds,
  COLUMN_ALLOWED,
  duplicateById,
  findBlock,
  insertAt,
  LAYOUT_PRESETS,
  listAt,
  moveTo,
  removeById,
  validColWidths,
} from '../blockUtils';

// A stable fixture: [text a, columns c (col0: [t1], col1: [t2, btn]), divider d]
function fixture(): Block[] {
  return [
    { id: 'a', type: 'text', text: '<p>A</p>' },
    {
      id: 'c',
      type: 'columns',
      columns: [
        [{ id: 't1', type: 'text', text: '<p>1</p>' }],
        [
          { id: 't2', type: 'text', text: '<p>2</p>' },
          { id: 'btn', type: 'button', label: 'Go', href: 'https://x' },
        ],
      ],
    },
    { id: 'd', type: 'divider' },
  ];
}

describe('findBlock', () => {
  it('finds root blocks with their address', () => {
    const f = findBlock(fixture(), 'd');
    expect(f).toMatchObject({ parentId: null, colIndex: 0, index: 2 });
    expect(f?.block.type).toBe('divider');
  });

  it('finds nested column blocks with their address', () => {
    const f = findBlock(fixture(), 'btn');
    expect(f).toMatchObject({ parentId: 'c', colIndex: 1, index: 1 });
  });

  it('returns null for unknown ids', () => {
    expect(findBlock(fixture(), 'nope')).toBeNull();
  });
});

describe('insertAt / listAt', () => {
  it('inserts at a root index', () => {
    const b = makeBlock('spacer');
    const next = insertAt(fixture(), b, { parentId: null, colIndex: 0, index: 1 });
    expect(next.map((x) => x.id)).toEqual(['a', b.id, 'c', 'd']);
  });

  it('clamps out-of-range indices', () => {
    const b = makeBlock('text');
    const next = insertAt(fixture(), b, { parentId: null, colIndex: 0, index: 99 });
    expect(next[next.length - 1].id).toBe(b.id);
  });

  it('inserts into a column', () => {
    const b = makeBlock('divider');
    const next = insertAt(fixture(), b, { parentId: 'c', colIndex: 0, index: 1 });
    expect(listAt(next, 'c', 0)!.map((x) => x.id)).toEqual(['t1', b.id]);
    // untouched sibling column keeps its identity
    expect(listAt(next, 'c', 1)).toEqual(listAt(fixture(), 'c', 1));
  });

  it('refuses column-illegal types (columns) — compiler would drop them', () => {
    const blocks = fixture();
    expect(canPlaceIn('c', 'columns')).toBe(false);
    const next = insertAt(blocks, makeBlock('columns'), { parentId: 'c', colIndex: 0, index: 0 });
    expect(next).toBe(blocks); // unchanged reference = rejected
    // Spacers are column-legal (compiler emits mj-spacer inside mj-column).
    expect(COLUMN_ALLOWED.has('spacer')).toBe(true);
    expect(COLUMN_ALLOWED.has('text')).toBe(true);
  });

  it('returns input unchanged for an unresolvable address', () => {
    const blocks = fixture();
    const next = insertAt(blocks, makeBlock('text'), { parentId: 'ghost', colIndex: 0, index: 0 });
    expect(next).toBe(blocks);
  });
});

describe('removeById', () => {
  it('removes a root block', () => {
    const { next, removed } = removeById(fixture(), 'a');
    expect(removed?.id).toBe('a');
    expect(next.map((b) => b.id)).toEqual(['c', 'd']);
  });

  it('removes a nested block without touching siblings', () => {
    const { next, removed } = removeById(fixture(), 't2');
    expect(removed?.id).toBe('t2');
    expect(listAt(next, 'c', 1)!.map((b) => b.id)).toEqual(['btn']);
    expect(listAt(next, 'c', 0)!.map((b) => b.id)).toEqual(['t1']);
  });

  it('no-ops on unknown ids', () => {
    const blocks = fixture();
    const { next, removed } = removeById(blocks, 'nope');
    expect(removed).toBeNull();
    expect(next).toBe(blocks);
  });
});

describe('moveTo', () => {
  it('moves later within the same list, adjusting for its own removal', () => {
    // a → after d (visual index 3 in [a,c,d]) should land last
    const next = moveTo(fixture(), 'a', { parentId: null, colIndex: 0, index: 3 });
    expect(next.map((b) => b.id)).toEqual(['c', 'd', 'a']);
  });

  it('moves earlier within the same list without adjustment', () => {
    const next = moveTo(fixture(), 'd', { parentId: null, colIndex: 0, index: 0 });
    expect(next.map((b) => b.id)).toEqual(['d', 'a', 'c']);
  });

  it('moves from root into a column', () => {
    const next = moveTo(fixture(), 'a', { parentId: 'c', colIndex: 0, index: 0 });
    expect(next.map((b) => b.id)).toEqual(['c', 'd']);
    expect(listAt(next, 'c', 0)!.map((b) => b.id)).toEqual(['a', 't1']);
  });

  it('moves from a column back to root', () => {
    const next = moveTo(fixture(), 'btn', { parentId: null, colIndex: 0, index: 1 });
    expect(next.map((b) => b.id)).toEqual(['a', 'btn', 'c', 'd']);
    expect(listAt(next, 'c', 1)!.map((b) => b.id)).toEqual(['t2']);
  });

  it('moves between columns', () => {
    const next = moveTo(fixture(), 't1', { parentId: 'c', colIndex: 1, index: 2 });
    expect(listAt(next, 'c', 0)).toEqual([]);
    expect(listAt(next, 'c', 1)!.map((b) => b.id)).toEqual(['t2', 'btn', 't1']);
  });

  it('refuses moving a columns block into a column (compiler drops nested columns)', () => {
    const blocks = fixture();
    expect(moveTo(blocks, 'c', { parentId: 'c', colIndex: 0, index: 0 })).toBe(blocks);
  });

  it('moves a spacer into a column (column-legal since the compiler gained mj-spacer there)', () => {
    const blocks: Block[] = [...fixture(), { id: 's', type: 'spacer', height: 24 }];
    const next = moveTo(blocks, 's', { parentId: 'c', colIndex: 0, index: 0 });
    expect(listAt(next, 'c', 0)!.map((b) => b.id)).toEqual(['s', 't1']);
  });
});

describe('duplicateById / cloneWithNewIds', () => {
  it('duplicates right after the original with fresh ids', () => {
    const { next, newId } = duplicateById(fixture(), 'a');
    expect(newId).toBeTruthy();
    expect(next.map((b) => b.id)).toEqual(['a', newId, 'c', 'd']);
    expect(next[1].text).toBe('<p>A</p>');
  });

  it('regenerates ids deeply for columns', () => {
    const orig = fixture()[1];
    const copy = cloneWithNewIds(orig);
    const before = new Set(allIds([orig]));
    for (const id of allIds([copy])) expect(before.has(id)).toBe(false);
    expect(copy.columns![1][1].label).toBe('Go');
  });

  it('duplicates a nested block inside its column', () => {
    const { next, newId } = duplicateById(fixture(), 't2');
    expect(listAt(next, 'c', 1)!.map((b) => b.id)).toEqual(['t2', newId, 'btn']);
  });
});

describe('clickInsertAddress', () => {
  it('lands after a root selection', () => {
    expect(clickInsertAddress(fixture(), 'a', 'divider')).toEqual({ parentId: null, colIndex: 0, index: 1 });
  });

  it('lands inside the column after a nested selection for column-legal types', () => {
    expect(clickInsertAddress(fixture(), 't2', 'button')).toEqual({ parentId: 'c', colIndex: 1, index: 1 });
  });

  it('escapes to root beside the columns block for root-only payloads', () => {
    expect(clickInsertAddress(fixture(), 't2', 'columns')).toEqual({ parentId: null, colIndex: 0, index: 2 });
    expect(clickInsertAddress(fixture(), 't2', null)).toEqual({ parentId: null, colIndex: 0, index: 2 });
  });

  it('falls back to the end without a selection', () => {
    expect(clickInsertAddress(fixture(), null, 'text')).toEqual({ parentId: null, colIndex: 0, index: 3 });
  });
});

describe('validColWidths', () => {
  const cols = (w?: number[]): Block => ({ id: 'c', type: 'columns', col_widths: w, columns: [[], []] });

  it('accepts coherent ratios', () => {
    expect(validColWidths(cols([33, 67]))).toEqual([33, 67]);
    expect(validColWidths(cols([50, 50]))).toEqual([50, 50]);
  });

  it('rejects wrong count, out-of-range and bad sums', () => {
    expect(validColWidths(cols(undefined))).toBeNull();
    expect(validColWidths(cols([100]))).toBeNull();
    expect(validColWidths(cols([2, 98]))).toBeNull();
    expect(validColWidths(cols([10, 10]))).toBeNull();
  });
});

describe('LAYOUT_PRESETS', () => {
  // Mirrors backend content_models.go block-type constants.
  const KNOWN = new Set(['text', 'heading', 'button', 'image', 'divider', 'spacer', 'columns', 'html', 'social', 'video', 'quote', 'menu']);

  it('only emit backend-known types and column-legal interiors', () => {
    for (const preset of LAYOUT_PRESETS) {
      for (const b of preset.make()) {
        expect(KNOWN.has(b.type)).toBe(true);
        for (const col of b.columns ?? []) {
          for (const sub of col) expect(COLUMN_ALLOWED.has(sub.type)).toBe(true);
        }
      }
    }
  });

  it('generate fresh ids on every call', () => {
    for (const preset of LAYOUT_PRESETS) {
      const a = allIds(preset.make());
      const b = allIds(preset.make());
      expect(a.some((id) => b.includes(id))).toBe(false);
    }
  });
});
