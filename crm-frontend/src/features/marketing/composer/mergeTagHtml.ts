// Pure HTML <-> merge-tag helpers for the M6 composer (dependency-free, so they can
// be unit-tested in isolation and reused by the block editors). Supports the
// {{path|fallback}} grammar (backend: automation.ExtractMergeTags / template.go).

// The chip node's serialized form: <span data-merge-tag="PATH" [data-merge-fallback="FB"] ...>…</span>.
const MERGE_TAG_SPAN = /<span[^>]*\bdata-merge-tag="([^"]*)"[^>]*>[\s\S]*?<\/span>/g;
const FALLBACK_ATTR = /\bdata-merge-fallback="([^"]*)"/;
// A bare {{path}} or {{path|fallback}} token in text (no braces inside).
const BARE_TOKEN = /\{\{\s*([\w.]+)\s*(?:\|([^}]*?))?\s*\}\}/g;

/** serializeMergeTags rewrites each chip span to the bare {{path|fallback}} token the
 *  backend resolves, stripping the editor-only wrapper. Idempotent on chip-free HTML. */
export function serializeMergeTags(html: string): string {
  return html.replace(MERGE_TAG_SPAN, (span, path) => {
    const fb = FALLBACK_ATTR.exec(span);
    return token(decodeAttr(path), fb ? decodeAttr(fb[1]) : '');
  });
}

/** deserializeMergeTags wraps bare {{path|fallback}} tokens back into chip spans so
 *  the TipTap editor loads them as atomic chips (the inverse of serialize). Used when
 *  seeding the editor from a stored block's text — the backend never persists the
 *  TipTap doc, so chips are reconstructed from the tokens. */
export function deserializeMergeTags(html: string): string {
  return html.replace(BARE_TOKEN, (_m, path: string, fb?: string) => {
    const f = (fb ?? '').trim();
    const attrs = `data-merge-tag="${encodeAttr(path)}"` + (f ? ` data-merge-fallback="${encodeAttr(f)}"` : '');
    return `<span ${attrs} class="merge-tag">${escapeText(token(path, f))}</span>`;
  });
}

/** token builds the {{path|fallback}} textual form. */
export function token(path: string, fallback?: string): string {
  return fallback ? `{{${path}|${fallback}}}` : `{{${path}}}`;
}

function decodeAttr(s: string): string {
  // Decode &amp; LAST — otherwise decoding it first would synthesize a new entity
  // (e.g. "&amp;lt;" -> "&lt;" -> "<") and corrupt a fallback that literally contains
  // an entity string. This makes decodeAttr a true inverse of encodeAttr.
  return s
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&amp;/g, '&');
}
function encodeAttr(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
function escapeText(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
