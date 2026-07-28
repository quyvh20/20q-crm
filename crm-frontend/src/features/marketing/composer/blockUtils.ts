// Pure tree helpers for the drag-and-drop email builder. The Block wire format
// (blocks.ts, mirrored by backend content_models.go) is a hard contract — the
// backend re-marshals only known fields — so the builder keeps its richer runtime
// state (selection, history, drop targets) OUTSIDE the document and these helpers
// only rearrange plain Block values. All functions return new arrays.

import { makeBlock, newBlockId, type Block, type BlockType } from './blocks';

/** A position in the document: the root list, or one column of a columns block. */
export interface BlockAddress {
  parentId: string | null; // columns block id, or null for the root list
  colIndex: number;        // column index when parentId is set; 0 otherwise
  index: number;           // position within that list
}

export interface FoundBlock extends BlockAddress {
  block: Block;
}

/** Block types that may live inside a column. Mirrors compile.go columnInner:
 *  spacer and columns are silently dropped there, so the builder refuses them. */
export const COLUMN_ALLOWED: ReadonlySet<BlockType> = new Set([
  'text', 'heading', 'button', 'image', 'divider', 'html', 'social', 'video', 'quote', 'menu',
]);

export function canPlaceIn(parentId: string | null, type: BlockType): boolean {
  return parentId === null || COLUMN_ALLOWED.has(type);
}

/** findBlock locates a block by id in the root list or one level deep in columns. */
export function findBlock(blocks: Block[], id: string): FoundBlock | null {
  for (let i = 0; i < blocks.length; i++) {
    const b = blocks[i];
    if (b.id === id) return { block: b, parentId: null, colIndex: 0, index: i };
    if (b.type === 'columns' && b.columns) {
      for (let c = 0; c < b.columns.length; c++) {
        const col = b.columns[c];
        for (let j = 0; j < col.length; j++) {
          if (col[j].id === id) return { block: col[j], parentId: b.id, colIndex: c, index: j };
        }
      }
    }
  }
  return null;
}

/** listAt returns the list a BlockAddress points into (root or a column), or null
 *  if the address no longer resolves (e.g. the columns block was removed). */
export function listAt(blocks: Block[], parentId: string | null, colIndex: number): Block[] | null {
  if (parentId === null) return blocks;
  const parent = blocks.find((b) => b.id === parentId);
  if (!parent || parent.type !== 'columns' || !parent.columns) return null;
  return parent.columns[colIndex] ?? null;
}

/** replaceList swaps out one list (root or a column) immutably. */
function replaceList(blocks: Block[], parentId: string | null, colIndex: number, next: Block[]): Block[] {
  if (parentId === null) return next;
  return blocks.map((b) => {
    if (b.id !== parentId || b.type !== 'columns' || !b.columns) return b;
    return { ...b, columns: b.columns.map((col, i) => (i === colIndex ? next : col)) };
  });
}

/** insertAt places a block at an address (index clamped to the list bounds).
 *  Returns the input unchanged if the address doesn't resolve or the type isn't
 *  allowed there. */
export function insertAt(blocks: Block[], block: Block, addr: BlockAddress): Block[] {
  if (!canPlaceIn(addr.parentId, block.type)) return blocks;
  const list = listAt(blocks, addr.parentId, addr.colIndex);
  if (!list) return blocks;
  const i = Math.max(0, Math.min(addr.index, list.length));
  const next = [...list.slice(0, i), block, ...list.slice(i)];
  return replaceList(blocks, addr.parentId, addr.colIndex, next);
}

/** removeById removes a block (root or nested) and returns it. */
export function removeById(blocks: Block[], id: string): { next: Block[]; removed: Block | null } {
  const found = findBlock(blocks, id);
  if (!found) return { next: blocks, removed: null };
  const list = listAt(blocks, found.parentId, found.colIndex)!;
  const next = replaceList(
    blocks,
    found.parentId,
    found.colIndex,
    list.filter((b) => b.id !== id),
  );
  return { next, removed: found.block };
}

/** moveTo relocates a block to an address, adjusting the target index when the
 *  block is removed from before its own insertion point in the same list. Returns
 *  the input unchanged for unresolvable/illegal moves (incl. a columns block into
 *  a column — the compiler drops nested columns). */
export function moveTo(blocks: Block[], id: string, addr: BlockAddress): Block[] {
  const found = findBlock(blocks, id);
  if (!found) return blocks;
  if (!canPlaceIn(addr.parentId, found.block.type)) return blocks;
  if (addr.parentId === id) return blocks; // a columns block can't move into itself

  let index = addr.index;
  const sameList = found.parentId === addr.parentId && found.colIndex === addr.colIndex;
  if (sameList && found.index < index) index -= 1;

  const { next: without, removed } = removeById(blocks, id);
  if (!removed) return blocks;
  const result = insertAt(without, removed, { ...addr, index });
  // insertAt returns its input unchanged when the address is unresolvable; don't
  // let that silently delete the block.
  return result === without ? blocks : result;
}

/** cloneWithNewIds deep-copies a block, regenerating every id (React keys must be
 *  unique document-wide). */
export function cloneWithNewIds(b: Block): Block {
  const copy: Block = { ...b, id: newBlockId() };
  if (b.columns) copy.columns = b.columns.map((col) => col.map(cloneWithNewIds));
  return copy;
}

/** duplicateById inserts a fresh-id copy right after the original. */
export function duplicateById(blocks: Block[], id: string): { next: Block[]; newId: string | null } {
  const found = findBlock(blocks, id);
  if (!found) return { next: blocks, newId: null };
  const copy = cloneWithNewIds(found.block);
  const next = insertAt(blocks, copy, {
    parentId: found.parentId,
    colIndex: found.colIndex,
    index: found.index + 1,
  });
  return { next, newId: copy.id };
}

/** allIds lists every block id (root + nested), for dnd registration and pruning
 *  a dangling selection. */
export function allIds(blocks: Block[]): string[] {
  const out: string[] = [];
  for (const b of blocks) {
    out.push(b.id);
    if (b.columns) for (const col of b.columns) for (const sub of col) out.push(sub.id);
  }
  return out;
}

// ---------------------------------------------------------------------------
// Layout presets: multi-block starting points inserted as one unit. They only
// ever emit backend-known block types (and column-legal interiors) so they can
// never produce a document the compiler would silently prune.
// ---------------------------------------------------------------------------

export interface LayoutPreset {
  key: string;
  label: string;
  description: string;
  make: () => Block[];
}

function text(html: string, align: Block['align'] = 'left'): Block {
  return { id: newBlockId(), type: 'text', text: html, align };
}
function heading(html: string, level: number, align: Block['align'] = 'left'): Block {
  return { id: newBlockId(), type: 'heading', text: html, level, align };
}
function button(label: string): Block {
  return { id: newBlockId(), type: 'button', label, href: 'https://', align: 'center' };
}
function image(alt: string): Block {
  return { id: newBlockId(), type: 'image', src: '', alt };
}

export const LAYOUT_PRESETS: LayoutPreset[] = [
  {
    key: 'hero',
    label: 'Hero',
    description: 'Big headline, supporting line, call to action',
    make: () => [
      heading('<p>Say hello to something new</p>', 1, 'center'),
      text('<p>One or two sentences on why this matters to your reader.</p>', 'center'),
      button('Learn more'),
    ],
  },
  {
    key: 'feature-columns',
    label: 'Two features',
    description: 'Side-by-side features with images',
    make: () => [
      {
        id: newBlockId(),
        type: 'columns',
        columns: [
          [image('Feature one'), heading('<p>First feature</p>', 3), text('<p>A short description of the first feature.</p>')],
          [image('Feature two'), heading('<p>Second feature</p>', 3), text('<p>A short description of the second feature.</p>')],
        ],
      },
    ],
  },
  {
    key: 'announcement',
    label: 'Announcement',
    description: 'Heading, divider, body copy',
    make: () => [
      heading('<p>An update from {{org.name}}</p>', 2),
      { id: newBlockId(), type: 'divider' },
      text('<p>Hi {{contact.first_name|there}},</p><p>Here’s what’s new.</p>'),
    ],
  },
  {
    key: 'cta',
    label: 'Call to action',
    description: 'Centered pitch with a button, breathing room around it',
    make: () => [
      { id: newBlockId(), type: 'spacer', height: 24 },
      heading('<p>Ready when you are</p>', 2, 'center'),
      text('<p>Wrap up with a single, clear next step.</p>', 'center'),
      button('Get started'),
      { id: newBlockId(), type: 'spacer', height: 24 },
    ],
  },
  {
    key: 'welcome',
    label: 'Welcome',
    description: 'Warm hello, what to expect, social links',
    make: () => [
      heading('<p>Welcome, {{contact.first_name|friend}}!</p>', 1, 'center'),
      text('<p>Thanks for joining {{org.name}}. Here’s what you can expect from us — and where to find us.</p>', 'center'),
      button('Explore your account'),
      { id: newBlockId(), type: 'social', align: 'center', social: [
        { network: 'facebook', href: '' }, { network: 'instagram', href: '' }, { network: 'linkedin', href: '' },
      ] },
    ],
  },
  {
    key: 'newsletter',
    label: 'Newsletter',
    description: 'Menu, headline, two article teasers',
    make: () => [
      { id: newBlockId(), type: 'menu', items: [
        { label: 'News', href: 'https://' }, { label: 'Products', href: 'https://' }, { label: 'Contact', href: 'https://' },
      ] },
      { id: newBlockId(), type: 'divider' },
      heading('<p>This month at {{org.name}}</p>', 1),
      {
        id: newBlockId(),
        type: 'columns',
        columns: [
          [image('Article one'), heading('<p>First story</p>', 3), text('<p>Two lines teasing the first story.</p>'), button('Read more')],
          [image('Article two'), heading('<p>Second story</p>', 3), text('<p>Two lines teasing the second story.</p>'), button('Read more')],
        ],
      },
    ],
  },
  {
    key: 'promo',
    label: 'Promotion',
    description: 'Bold offer on a colored section with a big CTA',
    make: () => [
      {
        id: newBlockId(), type: 'heading', level: 1, align: 'center', bg: '#0f172a', color: '#ffffff',
        text: '<p>25% off — this week only</p>',
      },
      { id: newBlockId(), type: 'text', align: 'center', bg: '#0f172a', color: '#cbd5e1',
        text: '<p>Use code <strong>SAVE25</strong> at checkout before Sunday.</p>' },
      { id: newBlockId(), type: 'button', label: 'Shop the sale', href: 'https://', align: 'center', bg: '#0f172a', btn_bg: '#f59e0b', btn_color: '#111827', radius: 24, pad_y: 0 },
      { id: newBlockId(), type: 'spacer', height: 24 },
    ],
  },
  {
    key: 'product',
    label: 'Product feature',
    description: 'Image beside copy with a buy button',
    make: () => [
      {
        id: newBlockId(),
        type: 'columns',
        columns: [
          [image('Product photo')],
          [heading('<p>Meet the new one</p>', 2), text('<p>Three crisp sentences on why it matters.</p>'), button('Buy now')],
        ],
      },
    ],
  },
  {
    key: 'event',
    label: 'Event / webinar',
    description: 'Invitation with a video teaser and RSVP',
    make: () => [
      heading('<p>You’re invited</p>', 1, 'center'),
      text('<p>Join us live — {{contact.first_name|friend}}, we saved you a seat.</p>', 'center'),
      { id: newBlockId(), type: 'video', src: '', alt: 'Event teaser', href: 'https://', label: '▶ Watch the teaser' },
      button('RSVP now'),
    ],
  },
  {
    key: 'testimonial',
    label: 'Testimonial',
    description: 'Customer quote with attribution and CTA',
    make: () => [
      { id: newBlockId(), type: 'quote', text: '<p>“Switching was the best decision we made this year.”</p>', label: 'A happy customer', color: '#2563eb' },
      button('Read the case study'),
    ],
  },
];

/** makeBlockOfType re-exports makeBlock so canvas/palette code has one import
 *  surface for both single blocks and presets. */
export function makeBlockOfType(type: BlockType): Block {
  return makeBlock(type);
}
