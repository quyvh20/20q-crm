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
  clickInsertAddress,
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
  lineHeight?: number;
  footerBg?: string;
  footerColor?: string;
  footerText?: string;
}

/** DocSnapshot is one undo/redo history entry: everything the recipient-visible
 *  document is made of. Name and merge scope are deliberately outside history —
 *  they are editor metadata, and undoing a scope change under existing merge
 *  tags would be more confusing than helpful. */
interface DocSnapshot {
  blocks: Block[];
  bodyBg: string;
  styles: DocStyles;
  subject: string;
  preheader: string;
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
  // metaDirty records unsaved changes history does NOT carry (name, merge
  // scope, a restored draft's name/scope). Undo recomputes dirty from history
  // depth, so without this an undo would report "saved" while a rename or a
  // restored draft is still unsaved.
  metaDirty: boolean;
  // pickerFor: a canvas placeholder asked for the image library — the inspector
  // opens its ImagePicker for this block id, then clears the request.
  pickerFor: string | null;

  past: DocSnapshot[];
  future: DocSnapshot[];
  // savedLen: past.length at the last save/hydrate. Undoing back to exactly
  // this depth means the document equals what the server has — not dirty.
  // -1 means the save point is unreachable (its snapshot was dropped by the
  // history cap or discarded with a redo branch), so dirty can never lie clean.
  savedLen: number;
  lastPatch: { key: string; at: number } | null;
  dragSnapshot: Block[] | null;

  hydrate: (seed: BuilderSeed) => void;
  applyDraft: (seed: BuilderSeed) => void;
  applyAIEmail: (blocks: Block[], subject?: string, preheader?: string) => void;
  reset: () => void;
  docSignature: () => string;
  markSaved: (signature?: string) => boolean;

  setName: (v: string) => void;
  setSubject: (v: string) => void;
  setPreheader: (v: string) => void;
  setBodyBg: (v: string) => void;
  patchStyles: (p: Partial<DocStyles>) => void;
  convertToHtml: (html: string) => void;
  toggleScope: (root: string) => void;

  select: (id: string | null) => void;
  requestImagePicker: (id: string) => void;
  clearPickerRequest: () => void;

  insertBlocks: (blocks: Block[], addr: BlockAddress) => void;
  addBlock: (type: BlockType, addr?: BlockAddress) => void;
  moveBlock: (id: string, addr: BlockAddress) => void;
  removeBlock: (id: string) => void;
  duplicateBlock: (id: string) => void;
  patchBlock: (id: string, patch: Partial<Block>, coalesceKey?: string) => void;
  setColumnCount: (id: string, count: number) => void;
  setColumnWidths: (id: string, widths: number[] | undefined) => void;
  unlinkBlock: (id: string) => void;
  syncLocalInstances: (ref: string, content: Block) => void;

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
  blocks: [] as Block[],
  bodyBg: '',
  styles: {} as DocStyles,
  selectedId: null as string | null,
  dirty: false,
  metaDirty: false,
  pickerFor: null as string | null,
  past: [] as DocSnapshot[],
  future: [] as DocSnapshot[],
  savedLen: 0,
  lastPatch: null as { key: string; at: number } | null,
  dragSnapshot: null as Block[] | null,
});

export const useBuilderStore = create<BuilderState>((set, get) => {
  const snapshot = (s: BuilderState): DocSnapshot => ({
    blocks: s.blocks,
    bodyBg: s.bodyBg,
    styles: s.styles,
    subject: s.subject,
    preheader: s.preheader,
  });

  const signatureOf = (s: BuilderState): string =>
    JSON.stringify([s.name, s.subject, s.preheader, s.scope, s.blocks, s.bodyBg, s.styles]);

  /** appendPast grows the history by one snapshot, enforcing the cap and keeping
   *  savedLen honest. Two ways the save point can become unreachable — the cap
   *  dropping its snapshot, and a redo branch that held it being discarded —
   *  both collapse it to -1 so `dirty` can never report a false "saved". */
  const appendPast = (s: BuilderState, snap: DocSnapshot): Pick<BuilderState, 'past' | 'future' | 'savedLen'> => {
    const past = [...s.past, snap];
    let savedLen = s.future.length > 0 && s.past.length < s.savedLen ? -1 : s.savedLen;
    if (past.length > HISTORY_LIMIT) {
      const dropped = past.length - HISTORY_LIMIT;
      past.splice(0, dropped);
      // Depths shift down by `dropped`; a save point that shifts below 0 fell
      // out of history entirely.
      savedLen = savedLen >= dropped ? savedLen - dropped : -1;
    }
    return { past, future: [], savedLen };
  };

  /** pushHistory snapshots the CURRENT document before a mutation. A coalesceKey
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
      ...appendPast(s, snapshot(s)),
      lastPatch: coalesceKey ? { key: coalesceKey, at: now } : null,
    });
  };

  /** applyBlocks commits a tree change; no-ops (failed moves etc.) don't dirty. */
  const applyBlocks = (next: Block[], extra?: Partial<BuilderState>) => {
    if (next === get().blocks) return false;
    set({ blocks: next, dirty: true, ...extra });
    return true;
  };

  /** restore applies a history snapshot (undo/redo target), pruning a selection
   *  that no longer resolves and recomputing dirty against the save point. */
  const restore = (snap: DocSnapshot, past: DocSnapshot[], future: DocSnapshot[]) => {
    const s = get();
    set({
      past,
      future,
      blocks: snap.blocks,
      bodyBg: snap.bodyBg,
      styles: snap.styles,
      subject: snap.subject,
      preheader: snap.preheader,
      // metaDirty keeps unsaved name/scope/draft-restore changes visible: they
      // are outside history, so depth alone cannot clear them.
      dirty: past.length !== s.savedLen || s.metaDirty,
      lastPatch: null,
      selectedId: s.selectedId && allIds(snap.blocks).includes(s.selectedId) ? s.selectedId : null,
    });
  };

  return {
    ...initial(),

    // An empty document hydrates EMPTY — the canvas shows its starter gallery
    // instead of silently seeding placeholder copy that could ship if forgotten.
    hydrate: (seed) =>
      set({
        ...initial(),
        name: seed.name,
        subject: seed.subject,
        preheader: seed.preheader,
        scope: seed.scope.length ? seed.scope : [...DEFAULT_SCOPE],
        blocks: seed.blocks,
        bodyBg: seed.bodyBg ?? '',
        styles: seed.styles ?? {},
      }),

    // applyDraft restores a checkpointed draft as a NORMAL undoable edit — not
    // a re-hydrate — so in-session work stays in history and Ctrl+Z undoes the
    // restore itself. name/scope ride outside history, hence metaDirty.
    applyDraft: (seed) => {
      pushHistory();
      set({
        name: seed.name,
        subject: seed.subject,
        preheader: seed.preheader,
        scope: seed.scope.length ? seed.scope : [...DEFAULT_SCOPE],
        blocks: seed.blocks,
        bodyBg: seed.bodyBg ?? '',
        styles: seed.styles ?? {},
        dirty: true,
        metaDirty: true,
        selectedId: null,
        lastPatch: null,
      });
    },

    // applyAIEmail replaces the document with a copilot draft as ONE undoable
    // step — the canvas is the review gate, so a draft the user doesn't like is
    // always one Ctrl+Z away. Subject/preheader only move when the draft carries
    // them (a section draft leaves them alone).
    applyAIEmail: (blocks, subject, preheader) => {
      pushHistory();
      const s = get();
      set({
        blocks,
        subject: subject != null && subject !== '' ? subject : s.subject,
        preheader: preheader != null && preheader !== '' ? preheader : s.preheader,
        dirty: true,
        selectedId: null,
        lastPatch: null,
      });
    },

    reset: () => set(initial()),

    docSignature: () => signatureOf(get()),

    // markSaved pins the save point and breaks typing coalescence so the next
    // edit is a fresh (undoable-back-to-saved) step. Passing the signature the
    // request actually carried guards the in-flight-edit race: keystrokes typed
    // while the save was travelling must NOT be marked clean. Returns whether
    // the working copy is now identical to what the server has.
    markSaved: (signature) => {
      const s = get();
      if (signature !== undefined && signature !== signatureOf(s)) {
        // The document moved on mid-request — the server copy is already stale.
        set({ savedLen: -1, dirty: true, lastPatch: null });
        return false;
      }
      set({ dirty: false, metaDirty: false, savedLen: s.past.length, lastPatch: null });
      return true;
    },

    setName: (v) => set({ name: v, dirty: true, metaDirty: true }),
    setSubject: (v) => {
      pushHistory('subject');
      set({ subject: v, dirty: true });
    },
    setPreheader: (v) => {
      pushHistory('preheader');
      set({ preheader: v, dirty: true });
    },
    setBodyBg: (v) => {
      pushHistory('bodyBg');
      set({ bodyBg: v, dirty: true });
    },
    patchStyles: (p) => {
      pushHistory('styles:' + Object.keys(p).sort().join(','));
      set((s) => ({ styles: { ...s.styles, ...p }, dirty: true }));
    },

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
        metaDirty: true,
      })),

    select: (id) => set({ selectedId: id }),
    requestImagePicker: (id) => set({ selectedId: id, pickerFor: id }),
    clearPickerRequest: () => set({ pickerFor: null }),

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

    // Without an explicit address, a new block lands right after the current
    // selection (or at the end) — click-to-add builds where you're working.
    addBlock: (type, addr) => {
      const s = get();
      const target = addr ?? clickInsertAddress(s.blocks, s.selectedId, type);
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
      const n = Math.max(2, Math.min(4, count));
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
      // Widths no longer match the column count — drop them (equal widths) so
      // the canvas keeps agreeing with the compiler's coherence rule.
      applyBlocks(s.blocks.map((b) => (b.id === id ? { ...b, columns: nextCols, col_widths: undefined } : b)));
    },

    setColumnWidths: (id, widths) => {
      const s = get();
      const found = findBlock(s.blocks, id);
      if (!found || found.block.type !== 'columns') return;
      // Re-picking the active ratio must not dirty the doc or push a no-op
      // undo step (invariant 8).
      if (JSON.stringify(found.block.col_widths ?? null) === JSON.stringify(widths ?? null)) return;
      pushHistory();
      applyBlocks(s.blocks.map((b) => (b.id === id ? { ...b, col_widths: widths } : b)));
    },

    // Break an instance's link to its synced library block: the content stays
    // exactly as-is and simply stops receiving library updates. Reusable
    // fragments you cannot override per campaign are a top practitioner
    // complaint, so unlinking is always available.
    unlinkBlock: (id) => {
      const s = get();
      const found = findBlock(s.blocks, id);
      if (!found || !found.block.ref) return;
      pushHistory();
      const strip = (b: Block): Block => (b.id === id ? { ...b, ref: undefined } : b);
      applyBlocks(s.blocks.map((b) => {
        if (b.id === id) return strip(b);
        if (b.type === 'columns' && b.columns && found.parentId === b.id) {
          return { ...b, columns: b.columns.map((col) => col.map(strip)) };
        }
        return b;
      }));
    },

    // After a successful "update everywhere", the OTHER instances of the same
    // block in THIS open document are still showing the old content. The server
    // already rewrote their stored copies, so without this the next save would
    // write the stale version straight back over the propagation.
    syncLocalInstances: (ref, content) => {
      const s = get();
      const apply = (b: Block, nested: boolean): Block => {
        if (b.ref !== ref) return b;
        // Root-only fields are meaningless (and silently ignored) in a column,
        // mirroring the server's own rule.
        const next: Block = { ...content, id: b.id, ref: b.ref };
        if (nested) {
          next.cond = undefined;
          next.bg = undefined;
          next.bg_url = undefined;
          next.full_width = undefined;
          next.pad_x = undefined;
          next.pad_y = undefined;
          next.columns = undefined;
        }
        return next;
      };
      let changed = false;
      const next = s.blocks.map((b) => {
        const top = apply(b, false);
        if (top !== b) changed = true;
        if (b.type === 'columns' && b.columns) {
          const cols = b.columns.map((col) => col.map((sub) => {
            const got = apply(sub, true);
            if (got !== sub) changed = true;
            return got;
          }));
          return { ...(top === b ? b : top), columns: cols };
        }
        return top;
      });
      if (!changed) return;
      pushHistory();
      applyBlocks(next);
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
          // Only blocks change during a drag, so the rest of the snapshot is
          // simply the current values.
          ...appendPast(s, { ...snapshot(s), blocks: snap }),
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
      restore(prev, s.past.slice(0, -1), [...s.future, snapshot(s)]);
    },

    redo: () => {
      const s = get();
      if (s.dragSnapshot) return;
      if (s.future.length === 0) return;
      const nxt = s.future[s.future.length - 1];
      restore(nxt, [...s.past, snapshot(s)], s.future.slice(0, -1));
    },
  };
});
