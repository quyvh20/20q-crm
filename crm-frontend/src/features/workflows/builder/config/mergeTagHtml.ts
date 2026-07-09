// Pure HTML <-> merge-tag helpers for the email-template TipTap editor (A5.3).
// Kept dependency-free (no @tiptap import) so they can be unit-tested in isolation
// and reused by the editor's serialization path.

// Matches the chip node's serialized form: <span data-merge-tag="PATH" ...>…</span>.
// The span is an atom (no nested element children), so the lazy [\s\S]*? body is safe.
const MERGE_TAG_SPAN = /<span[^>]*\bdata-merge-tag="([^"]*)"[^>]*>[\s\S]*?<\/span>/g;

/**
 * serializeMergeTags rewrites the editor's HTML so each merge-tag chip becomes the
 * bare `{{path}}` token the backend's InterpolateTemplate resolves — stripping the
 * editor-only <span data-merge-tag> wrapper (and its class) so the sent body is
 * clean HTML. Idempotent: HTML with no chips is returned unchanged.
 */
export function serializeMergeTags(html: string): string {
  return html.replace(MERGE_TAG_SPAN, (_m, path) => `{{${decodeAttr(path)}}}`);
}

function decodeAttr(s: string): string {
  return s
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'");
}
