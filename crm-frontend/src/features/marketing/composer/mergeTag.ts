import { Node, mergeAttributes, nodeInputRule } from '@tiptap/core';

// MergeTag is the composer's inline atomic merge-tag chip (M6). Like the A5 node but
// with an optional `fallback` attribute, serialized to {{path|fallback}} (or
// {{path}} when there is no fallback). Kept marketing-local so extending the grammar
// here can't destabilize the live A5 template editor.
export const MergeTag = Node.create({
  name: 'mergeTag',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      path: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-merge-tag') || '',
        renderHTML: (attrs) => (attrs.path ? { 'data-merge-tag': attrs.path } : {}),
      },
      fallback: {
        default: '',
        parseHTML: (el) => el.getAttribute('data-merge-fallback') || '',
        renderHTML: (attrs) => (attrs.fallback ? { 'data-merge-fallback': attrs.fallback } : {}),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'span[data-merge-tag]' }];
  },

  renderHTML({ node, HTMLAttributes }) {
    return ['span', mergeAttributes(HTMLAttributes, { class: 'merge-tag' }), tokenText(node.attrs.path, node.attrs.fallback)];
  },

  renderText({ node }) {
    return tokenText(node.attrs.path, node.attrs.fallback);
  },

  addInputRules() {
    // Typing {{path}} or {{path|fallback}} auto-converts to a chip.
    return [
      nodeInputRule({
        find: /\{\{([\w.]+)(?:\|([^}]*))?\}\}$/,
        type: this.type,
        getAttributes: (match) => ({ path: match[1], fallback: (match[2] ?? '').trim() }),
      }),
    ];
  },
});

/** tokenText renders the {{path|fallback}} form (or {{path}} with no fallback). */
export function tokenText(path: string, fallback?: string): string {
  return fallback ? `{{${path}|${fallback}}}` : `{{${path}}}`;
}

export default MergeTag;
