// Block-document model for the M6 email composer. This MUST match the backend
// BlockDocument/Block shape (crm-backend/internal/marketing/content_models.go) —
// the composer emits this JSON as `body_json`, and the backend compiles it to
// email-safe HTML. Only the struct fields below survive the backend round-trip
// (it re-marshals the parsed struct), so merge-tag chips round-trip via
// serialize/deserialize of the bare {{path|fallback}} tokens in `text`, not a
// stored TipTap doc.

export type BlockType =
  | 'text' | 'heading' | 'button' | 'image' | 'divider' | 'spacer' | 'columns' | 'html'
  | 'social' | 'video' | 'quote' | 'menu' | 'product';

export interface SocialLink {
  network: string; // gated server-side to MJML's built-in icon set
  href: string;
}

export interface LinkItem {
  label: string;
  href: string;
}

export interface FooterStyle {
  bg?: string;
  color?: string;
  text?: string; // optional HTML above the compliance lines
}

export interface BlockCondition {
  field: string; // merge path (same catalog as merge tags)
  op: string;    // key into COND_OPS
  value?: string;
}

/** COND_OPS mirrors the backend condOps allowlist. */
export const COND_OPS: { key: string; label: string; needsValue: boolean }[] = [
  { key: 'exists', label: 'has a value', needsValue: false },
  { key: 'not_exists', label: 'is empty', needsValue: false },
  { key: 'eq', label: 'is', needsValue: true },
  { key: 'neq', label: 'is not', needsValue: true },
  { key: 'contains', label: 'contains', needsValue: true },
  { key: 'gt', label: 'is greater than', needsValue: true },
  { key: 'lt', label: 'is less than', needsValue: true },
];

export interface Block {
  id: string;
  type: BlockType;
  text?: string;          // text/heading/quote: serialized HTML with {{path|fallback}} tokens; html: raw snippet
  level?: number;         // heading: 1-3
  align?: 'left' | 'center' | 'right';
  label?: string;         // button label; video watch-button label; quote attribution
  href?: string;          // button/image/video link
  src?: string;           // image/video-thumbnail src
  alt?: string;           // image alt
  height?: number;        // spacer px
  bg?: string;            // root blocks: section background (#hex)
  title?: string;         // product: name line
  price?: string;         // product: price line (free text)
  color?: string;         // text/heading color; divider line; quote accent (#hex)
  size?: number;          // text/heading font px; social icon px; button font px
  thickness?: number;     // divider px
  radius?: number;        // button/image border-radius px
  width_px?: number;      // image width px
  pad_y?: number;         // section vertical padding override (0 allowed)
  pad_x?: number;         // section horizontal padding override (root blocks; 0-60)
  btn_bg?: string;        // button background (#hex)
  btn_color?: string;     // button text color (#hex)
  font?: string;          // per-block font override — key into FONT_OPTIONS (text/heading/button/quote)
  full_width?: boolean;   // root blocks: section background bleeds edge-to-edge in the inbox
  bg_url?: string;        // root blocks: section background image (https URL; renders cover/center)
  col_widths?: number[];  // columns: per-column % widths (one per column, ~100 total; else equal)
  keep_columns?: boolean; // columns: stay side-by-side on mobile (mj-group) instead of stacking
  hide_mobile?: boolean;  // device visibility: hidden under 480px
  hide_desktop?: boolean; // device visibility: hidden at/over 480px
  cond?: BlockCondition;  // show-only-if rule, evaluated per recipient at send (root blocks)
  ref?: string;           // linked to a SYNCED saved block (its id) — content stays materialized here
  social?: SocialLink[];  // social block entries
  items?: LinkItem[];     // menu block entries
  columns?: Block[][];    // columns: one sub-block list per column
}

export interface BlockDocument {
  blocks: Block[];
  body_bg?: string;       // mj-body background (also behind unset-bg blocks)
  width?: number;         // content width px (480-800, default 600)
  font_family?: string;   // key into the server's allowlisted font stacks
  text_color?: string;    // default text color (#hex)
  link_color?: string;    // <a> color (#hex)
  line_height?: number;   // body text line-height (1.0-2.5, default 1.5)
  footer?: FooterStyle;   // compliance-footer styling
}

/** FONT_OPTIONS mirrors the backend fontStacks allowlist (key → label + css stack
 *  for canvas preview). */
export const FONT_OPTIONS: { key: string; label: string; stack: string }[] = [
  { key: 'arial', label: 'Arial', stack: 'Arial,Helvetica,sans-serif' },
  { key: 'helvetica', label: 'Helvetica', stack: 'Helvetica,Arial,sans-serif' },
  { key: 'georgia', label: 'Georgia', stack: "Georgia,'Times New Roman',serif" },
  { key: 'times', label: 'Times New Roman', stack: "'Times New Roman',Times,serif" },
  { key: 'verdana', label: 'Verdana', stack: 'Verdana,Geneva,sans-serif' },
  { key: 'tahoma', label: 'Tahoma', stack: 'Tahoma,Geneva,sans-serif' },
  { key: 'trebuchet', label: 'Trebuchet MS', stack: "'Trebuchet MS',Helvetica,sans-serif" },
  { key: 'courier', label: 'Courier New', stack: "'Courier New',Courier,monospace" },
];

/** SOCIAL_NETWORKS mirrors the backend socialNetworks allowlist. */
export const SOCIAL_NETWORKS: { key: string; label: string }[] = [
  { key: 'facebook', label: 'Facebook' },
  { key: 'instagram', label: 'Instagram' },
  { key: 'twitter', label: 'X (Twitter)' },
  { key: 'linkedin', label: 'LinkedIn' },
  { key: 'youtube', label: 'YouTube' },
  { key: 'pinterest', label: 'Pinterest' },
  { key: 'github', label: 'GitHub' },
  { key: 'web', label: 'Website' },
];

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
  { type: 'social', label: 'Social', icon: 'Share2' },
  { type: 'video', label: 'Video', icon: 'CirclePlay' },
  { type: 'quote', label: 'Quote', icon: 'Quote' },
  { type: 'menu', label: 'Menu', icon: 'Menu' },
  { type: 'product', label: 'Product', icon: 'Package' },
  { type: 'html', label: 'HTML', icon: 'CodeXml' },
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
    case 'html':
      // Deliberately EMPTY: the boilerplate lives as the code editor's
      // placeholder, never as real content that ships if the author forgets
      // to replace it. Empty html blocks compile to nothing.
      b.text = '';
      break;
    case 'social':
      b.align = 'center';
      b.social = [
        { network: 'facebook', href: '' },
        { network: 'instagram', href: '' },
        { network: 'twitter', href: '' },
      ];
      break;
    case 'video':
      b.src = '';
      b.alt = '';
      b.href = '';
      b.label = '▶ Watch video';
      break;
    case 'quote':
      b.text = '<p>“A short quote from a happy customer.”</p>';
      b.label = '';
      break;
    case 'menu':
      b.items = [
        { label: 'Shop', href: 'https://' },
        { label: 'About', href: 'https://' },
      ];
      break;
    case 'product':
      b.title = 'Product name';
      b.text = '<p>A short description of this product.</p>';
      b.price = '';
      b.label = 'View product';
      b.href = 'https://';
      b.src = '';
      b.align = 'left';
      break;
    default:
      break;
  }
  return b;
}
