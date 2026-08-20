// Design → HTML conversion for the Code view. The output is clean, readable,
// email-idiomatic HTML (tables for columns, inline styles) that becomes ONE
// html block — every element/attribute used here survives the backend's wide
// raw-HTML sanitizer, so converting never silently loses content.

import { FONT_OPTIONS, type Block } from './blocks';
import { validColWidths } from './blockUtils';

const escAttr = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const escText = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function headingPx(b: Block): number {
  if (b.size && b.size >= 10 && b.size <= 60) return b.size;
  switch (b.level) {
    case 2: return 20;
    case 3: return 17;
    default: return 24;
  }
}

function styleOf(parts: (string | false | undefined)[]): string {
  const s = parts.filter(Boolean).join(';');
  return s ? ` style="${s}"` : '';
}

function renderBlock(b: Block): string {
  const align = b.align ?? 'left';
  switch (b.type) {
    case 'text':
      return `<div${styleOf([`padding:10px 25px`, `text-align:${align}`, b.size ? `font-size:${b.size}px` : false, b.color ? `color:${b.color}` : false])}>\n${b.text ?? ''}\n</div>`;
    case 'heading':
      return `<div${styleOf([`padding:10px 25px`, `text-align:${align}`, `font-size:${headingPx(b)}px`, 'font-weight:bold', b.color ? `color:${b.color}` : false])}>\n${b.text ?? ''}\n</div>`;
    case 'button': {
      const btnAlign = b.align === 'left' || b.align === 'right' ? b.align : 'center';
      return `<div style="padding:10px 25px;text-align:${btnAlign}">\n  <a href="${escAttr(b.href || '#')}"${styleOf([
        'display:inline-block',
        `background:${b.btn_bg || '#414141'}`,
        `color:${b.btn_color || '#ffffff'}`,
        `border-radius:${b.radius ?? 3}px`,
        `font-size:${b.size ?? 13}px`,
        'padding:10px 25px',
        'text-decoration:none',
      ])}>${escText(b.label ?? 'Button')}</a>\n</div>`;
    }
    case 'image': {
      const img = `<img src="${escAttr(b.src ?? '')}" alt="${escAttr(b.alt ?? '')}"${styleOf([
        'max-width:100%',
        b.width_px ? `width:${b.width_px}px` : false,
        b.radius ? `border-radius:${b.radius}px` : false,
      ])} />`;
      const wrapped = b.href ? `<a href="${escAttr(b.href)}">${img}</a>` : img;
      return `<div style="padding:10px 25px;text-align:${b.align ?? 'center'}">\n  ${wrapped}\n</div>`;
    }
    case 'divider':
      return `<div style="padding:10px 25px"><hr${styleOf([`border:none`, `border-top:${b.thickness ?? 1}px solid ${b.color || '#e5e7eb'}`])} /></div>`;
    case 'spacer':
      return `<div style="height:${b.height && b.height > 0 ? b.height : 20}px"></div>`;
    case 'html':
      return b.text ?? '';
    case 'social': {
      const size = b.size && b.size >= 12 && b.size <= 48 ? b.size : 24;
      const links = (b.social ?? [])
        .filter((s) => s.href.trim() !== '')
        .map((s) => `<a href="${escAttr(s.href)}" style="margin:0 4px"><img src="https://www.mailjet.com/images/theme/v1/icons/ico-social/${escAttr(s.network)}.png" alt="${escAttr(s.network)}" width="${size}" height="${size}" /></a>`)
        .join('\n  ');
      return links ? `<div style="padding:10px 25px;text-align:${b.align ?? 'center'}">\n  ${links}\n</div>` : '';
    }
    case 'video':
      return renderBlock({ ...b, type: 'image', align: 'center' }) +
        `\n<div style="padding:0 25px 10px;text-align:center">\n  <a href="${escAttr(b.href || '#')}" style="display:inline-block;background:#414141;color:#ffffff;border-radius:3px;font-size:13px;padding:10px 25px;text-decoration:none">${escText(b.label?.trim() || '▶ Watch video')}</a>\n</div>`;
    case 'quote': {
      const attribution = b.label?.trim() ? `\n  <p style="margin:8px 0 0;color:#6b7280;font-size:13px">— ${escText(b.label)}</p>` : '';
      return `<div style="padding:10px 25px">\n  <blockquote style="border-left:3px solid ${b.color || '#d1d5db'};margin:0;padding:4px 0 4px 16px;font-style:italic">\n${b.text ?? ''}\n  </blockquote>${attribution}\n</div>`;
    }
    case 'menu': {
      const items = (b.items ?? []).filter((it) => it.label.trim() !== '' && it.href.trim() !== '');
      if (items.length === 0) return '';
      const links = items
        .map((it) => `<a href="${escAttr(it.href)}" style="text-decoration:none;font-weight:500">${escText(it.label)}</a>`)
        .join(' &#8226; ');
      return `<div style="padding:10px 25px;text-align:center">${links}</div>`;
    }
    case 'columns': {
      const cols = b.columns ?? [];
      if (cols.length === 0) return '';
      // Honor authored ratios — converting a 33/67 layout must not silently
      // hand back a 50/50 one.
      const widths = validColWidths(b);
      const tds = cols
        .map((col, i) => `    <td valign="top" width="${widths ? widths[i] : Math.floor(100 / cols.length)}%">\n${col.map((sub) => renderBlock(sub)).map(indent(6)).join('\n')}\n    </td>`)
        .join('\n');
      return `<table role="presentation" width="100%" cellpadding="0" cellspacing="0">\n  <tr>\n${tds}\n  </tr>\n</table>`;
    }
    default:
      return '';
  }
}

const indent = (n: number) => (s: string) => s.split('\n').map((l) => ' '.repeat(n) + l).join('\n');

/** blocksToSimpleHtml renders the whole design as one editable HTML document
 *  body — section backgrounds (color and image), padding, and per-block fonts
 *  included, so the conversion changes the markup, not the design. */
export function blocksToSimpleHtml(blocks: Block[]): string {
  return blocks
    .map((b) => {
      const inner = renderBlock(b);
      if (!inner) return '';
      const padY = b.pad_y != null ? Math.min(Math.max(b.pad_y, 0), 80) : 20;
      const padX = b.pad_x != null ? Math.min(Math.max(b.pad_x, 0), 60) : 0;
      const font = b.font ? FONT_OPTIONS.find((f) => f.key === b.font)?.stack : undefined;
      const bgImage = b.bg_url && /^https?:\/\//i.test(b.bg_url)
        ? `background-image:url(${escAttr(b.bg_url)});background-size:cover;background-position:center center;background-repeat:no-repeat`
        : false;
      return `<div${styleOf([
        `padding:${padY}px ${padX}px`,
        b.bg ? `background-color:${b.bg}` : false,
        bgImage,
        font ? `font-family:${font}` : false,
      ])}>\n${inner}\n</div>`;
    })
    .filter(Boolean)
    .join('\n\n');
}
