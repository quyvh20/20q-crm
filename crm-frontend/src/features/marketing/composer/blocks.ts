// Block-document model for the M6 email composer. This MUST match the backend
// BlockDocument/Block shape (crm-backend/internal/marketing/content_models.go) —
// the composer emits this JSON as `body_json`, and the backend compiles it to
// email-safe HTML. Only the struct fields below survive the backend round-trip
// (it re-marshals the parsed struct), so merge-tag chips round-trip via
// serialize/deserialize of the bare {{path|fallback}} tokens in `text`, not a
// stored TipTap doc.

export type BlockType = 'text' | 'heading' | 'button' | 'image' | 'divider' | 'spacer' | 'columns';

export interface Block {
  id: string;
  type: BlockType;
  text?: string;          // text/heading: serialized HTML with {{path|fallback}} tokens
  level?: number;         // heading: 1-3
  align?: 'left' | 'center' | 'right';
  label?: string;         // button label
  href?: string;          // button/image link
  src?: string;           // image src
  alt?: string;           // image alt
  height?: number;        // spacer px
  columns?: Block[][];    // columns: one sub-block list per column
}

export interface BlockDocument {
  blocks: Block[];
}

let seq = 0;
/** newBlockId returns a client-side id (React key + block.id). Not persisted-unique;
 *  the backend keys rows by its own uuid. */
export function newBlockId(): string {
  seq += 1;
  return `blk_${Date.now().toString(36)}_${seq}`;
}

export interface BlockTypeMeta {
  type: BlockType;
  label: string;
  icon: string; // lucide icon name resolved by the composer
}

/** PALETTE is the "add block" menu (footer is compiler-owned, not author-added). */
export const PALETTE: BlockTypeMeta[] = [
  { type: 'text', label: 'Text', icon: 'Type' },
  { type: 'heading', label: 'Heading', icon: 'Heading' },
  { type: 'button', label: 'Button', icon: 'MousePointerClick' },
  { type: 'image', label: 'Image', icon: 'Image' },
  { type: 'columns', label: 'Two columns', icon: 'Columns2' },
  { type: 'divider', label: 'Divider', icon: 'Minus' },
  { type: 'spacer', label: 'Spacer', icon: 'MoveVertical' },
];

/** makeBlock builds a sensible default for a new block of the given type. */
export function makeBlock(type: BlockType): Block {
  const b: Block = { id: newBlockId(), type };
  switch (type) {
    case 'text':
      b.text = '<p>New paragraph. Use “Insert variable” for merge tags.</p>';
      b.align = 'left';
      break;
    case 'heading':
      b.text = '<p>Heading</p>';
      b.level = 2;
      b.align = 'left';
      break;
    case 'button':
      b.label = 'Click here';
      b.href = 'https://';
      b.align = 'center';
      break;
    case 'image':
      b.src = '';
      b.alt = '';
      break;
    case 'spacer':
      b.height = 24;
      break;
    case 'columns':
      b.columns = [
        [{ id: newBlockId(), type: 'text', text: '<p>Left column</p>' }],
        [{ id: newBlockId(), type: 'text', text: '<p>Right column</p>' }],
      ];
      break;
    default:
      break;
  }
  return b;
}
