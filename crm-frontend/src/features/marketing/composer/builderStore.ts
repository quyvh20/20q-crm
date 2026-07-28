// Zustand store for the drag-and-drop email builder. React Query owns load/save
// (contentQueries); this store owns the working copy — the block document, the
// email settings, selection, and a document-level undo/redo history. The page
// hydrates it once per content id (seed-once trap) and POSTs its state verbatim
// on save: blocks are always wire-format (merge chips already serialized to bare
// {{path|fallback}} tokens by the rich-text layer).

import { create } from 'zustand';
import { makeBlock, type Block, type BlockType } from './blocks';
import {
  allIds,
  duplicateById,
  findBlock,
  insertAt,
  moveTo,
  removeById,
  type BlockAddress,
} from './blockUtils';

const HISTORY_LIMIT = 100;
// Rapid same-field edits (typing) collapse into one undo step until this much
// idle time passes.
const COALESCE_MS = 900;

export const DEFAULT_SCOPE = ['contact', 'org', 'campaign'];

/** DocStyles are the document-level "Styles" panel values (all optional; wire
 *  keys live on BlockDocument). */
export interface DocStyles {
  width?: number;
  fontFamily?: string;
  textColor?: string;
  linkColor?: string;
  footerBg?: string;
  footerColor?: string;
  footerText?: string;
}

export interface BuilderSeed {
  name: string;
  subject: string;
  preheader: string;
  scope: string[];
  blocks: Block[];
  bodyBg?: string;
  styles?: DocStyles;
}

interface BuilderState {
  name: string;
  subject: string;
  preheader: string;
  scope: string[];
  blocks: Block[];
  bodyBg: string;
  styles: DocStyles;
  selectedId: string | null;
  dirty: boolean;

  past: Block[][];
  future: Block[][];
  lastPatch: { key: string; at: number } | null;
  dragSnapshot: Block[] | null;

  hydrate: (seed: BuilderSeed) => void;
  reset: () => void;
  markSaved: () => void;

  setName: (v: string) => void;
  setSubject: (v: string) => void;
  setPreheader: (v: string) => void;
  setBodyBg: (v: string) => void;
  patchStyles: (p: Partial<DocStyles>) => void;
  convertToHtml: (html: string) => void;
  toggleScope: (root: string) => void;

  select: (id: string | null) => void;

  insertBlocks: (blocks: Block[], addr: BlockAddress) => void;
  addBlock: (type: BlockType, addr?: BlockAddress) => void;
  moveBlock: (id: string, addr: BlockAddress) => void;
  removeBlock: (id: string) => void;
  duplicateBlock: (id: string) => void;
  patchBlock: (id: string, patch: Partial<Block>, coalesceKey?: string) => void;
  setColumnCount: (id: string, count: number) => void;

  beginDrag: () => void;
  setBlocksDuringDrag: (blocks: Block[]) => void;
  endDrag: (commit: boolean) => void;

  undo: () => void;
  redo: () => void;
}

const initial = () => ({
  name: '',
  subject: '',
  preheader: '',
  scope: [...DEFAULT_SCOPE],
  blocks: [makeBlock('text')],
  bodyBg: '',
  styles: {} as DocStyles,
  selectedId: null as string | null,
  dirty: false,
  past: [] as Block[][],
  future: [] as Block[][],
  lastPatch: null as { key: string; at: number } | null,
  dragSnapshot: null as Block[] | null,
});

export const useBuilderStore = create<BuilderState>((set, get) => {
  /** pushHistory snapshots the CURRENT blocks before a mutation. A coalesceKey
   *  collapses bursts of the same edit (typing in one field) into one undo step. */
  const pushHistory = (coalesceKey?: string) => {
    const s = get();
    const now = Date.now();
    if (
      coalesceKey &&
      s.lastPatch &&
      s.lastPatch.key === coalesceKey &&
      now - s.lastPatch.at < COALESCE_MS
    ) {
      set({ lastPatch: { key: coalesceKey, at: now } });
      return;
    }
    set({
      past: [...s.past.slice(-(HISTORY_LIMIT - 1)), s.blocks],
      future: [],
      lastPatch: coalesceKey ? { key: coalesceKey, at: now } : null,
    });
  };

  /** applyBlocks commits a tree change; no-ops (failed moves etc.) don't dirty. */
  const applyBlocks = (next: Block[], extra?: Partial<BuilderState>) => {
    if (next === get().blocks) return false;
    set({ blocks: next, dirty: true, ...extra });
    return true;
  };

  return {
    ...initial(),

    hydrate: (seed) =>
      set({
        ...initial(),
        name: seed.name,
        subject: seed.subject,
        preheader: seed.preheader,
        scope: seed.scope.length ? seed.scope : [...DEFAULT_SCOPE],
        blocks: seed.blocks.length ? seed.blocks : [makeBlock('text')],
        bodyBg: seed.bodyBg ?? '',
        styles: seed.styles ?? {},
      }),

    reset: () => set(initial()),
    markSaved: () => set({ dirty: false }),

    setName: (v) => set({ name: v, dirty: true }),
    setSubject: (v) => set({ subject: v, dirty: true }),
    setPreheader: (v) => set({ preheader: v, dirty: true }),
    setBodyBg: (v) => set({ bodyBg: v, dirty: true }),
    patchStyles: (p) => set((s) => ({ styles: { ...s.styles, ...p }, dirty: true })),

    // Code view's one-way conversion: the whole design becomes a single
    // editable html block. One history entry — undo restores the blocks.
    convertToHtml: (html) => {
      pushHistory();
      const blk: Block = { ...makeBlock('html'), text: html };
      applyBlocks([blk], { selectedId: blk.id });
    },
    toggleScope: (root) =>
      set((s) => ({
        scope: s.scope.includes(root) ? s.scope.filter((r) => r !== root) : [...s.scope, root],
        dirty: true,
      })),

    select: (id) => set({ selectedId: id }),

    insertBlocks: (blocks, addr) => {
      if (blocks.length === 0) return;
      let next = get().blocks;
      blocks.forEach((b, i) => {
        next = insertAt(next, b, { ...addr, index: addr.index + i });
      });
      if (next === get().blocks) return;
      pushHistory();
      applyBlocks(next, { selectedId: blocks[0].id });
    },

    addBlock: (type, addr) => {
      const s = get();
      const target = addr ?? { parentId: null, colIndex: 0, index: s.blocks.length };
      const block = makeBlock(type);
      const next = insertAt(s.blocks, block, target);
      if (next === s.blocks) return;
      pushHistory();
      applyBlocks(next, { selectedId: block.id });
    },

    moveBlock: (id, addr) => {
      const next = moveTo(get().blocks, id, addr);
      if (next === get().blocks) return;
      pushHistory();
      applyBlocks(next);
    },

    removeBlock: (id) => {
      const { next, removed } = removeById(get().blocks, id);
      if (!removed) return;
      pushHistory();
      const s = get();
      const selectionGone = s.selectedId !== null && !allIds(next).includes(s.selectedId);
      applyBlocks(next, selectionGone ? { selectedId: null } : undefined);
    },

    duplicateBlock: (id) => {
      const { next, newId } = duplicateById(get().blocks, id);
      if (!newId) return;
      pushHistory();
      applyBlocks(next, { selectedId: newId });
    },

    patchBlock: (id, patch, coalesceKey) => {
      const s = get();
      const found = findBlock(s.blocks, id);
      if (!found) return;
      // Value-identical patches (clicking the already-active alignment, a TipTap
      // echo) must not push history or dirty the doc — invariant 8.
      const changed = (Object.keys(patch) as (keyof Block)[]).some((k) => found.block[k] !== patch[k]);
      if (!changed) return;
      pushHistory(coalesceKey);
      const apply = (b: Block): Block => (b.id === id ? { ...b, ...patch } : b);
      const next = s.blocks.map((b) => {
        if (b.id === id) return apply(b);
        if (b.type === 'columns' && b.columns && found.parentId === b.id) {
          return { ...b, columns: b.columns.map((col) => col.map(apply)) };
        }
        return b;
      });
      applyBlocks(next);
    },

    setColumnCount: (id, count) => {
      const s = get();
      const found = findBlock(s.blocks, id);
      if (!found || found.block.type !== 'columns') return;
      const cols = found.block.columns ?? [];
      const n = Math.max(2, Math.min(3, count));
      if (n === cols.length) return;
      let nextCols: Block[][];
      if (n > cols.length) {
        nextCols = [...cols, ...Array.from({ length: n - cols.length }, () => [] as Block[])];
      } else {
        // Only trailing EMPTY columns may be dropped — never silently delete content.
        nextCols = cols.slice();
        while (nextCols.length > n && nextCols[nextCols.length - 1].length === 0) nextCols.pop();
        if (nextCols.length === cols.length) return;
      }
      pushHistory();
      applyBlocks(s.blocks.map((b) => (b.id === id ? { ...b, columns: nextCols } : b)));
    },

    // A drag is one gesture ⇒ one undo step. beginDrag snapshots; dragover
    // mutations skip history; endDrag(commit) pushes the pre-drag snapshot (or
    // restores it on cancel).
    beginDrag: () => set({ dragSnapshot: get().blocks, lastPatch: null }),
    setBlocksDuringDrag: (blocks) => set({ blocks }),
    endDrag: (commit) => {
      const s = get();
      const snap = s.dragSnapshot;
      set({ dragSnapshot: null });
      if (!snap) return;
      if (!commit) {
        set({ blocks: snap });
        return;
      }
      // A gesture that lands back where it started (dragover shuffled and
      // un-shuffled) produces a new-reference but identical tree — no undo step.
      if (snap !== s.blocks && JSON.stringify(snap) !== JSON.stringify(s.blocks)) {
        set({
          past: [...s.past.slice(-(HISTORY_LIMIT - 1)), snap],
          future: [],
          dirty: true,
        });
      }
    },

    undo: () => {
      const s = get();
      // Mid-drag undo would rewrite the tree under the gesture and desync the
      // transaction snapshot — ignore until the drag settles.
      if (s.dragSnapshot) return;
      if (s.past.length === 0) return;
      const prev = s.past[s.past.length - 1];
      set({
        past: s.past.slice(0, -1),
        future: [...s.future, s.blocks],
        blocks: prev,
        dirty: true,
        lastPatch: null,
        selectedId: s.selectedId && allIds(prev).includes(s.selectedId) ? s.selectedId : null,
      });
    },

    redo: () => {
      const s = get();
      if (s.dragSnapshot) return;
      if (s.future.length === 0) return;
      const nxt = s.future[s.future.length - 1];
      set({
        future: s.future.slice(0, -1),
        past: [...s.past, s.blocks],
        blocks: nxt,
        dirty: true,
        lastPatch: null,
        selectedId: s.selectedId && allIds(nxt).includes(s.selectedId) ? s.selectedId : null,
      });
    },
  };
});
