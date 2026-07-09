import { Node, mergeAttributes, nodeInputRule } from '@tiptap/core';

// MergeTag is an inline, atomic TipTap node (A5.3) rendered as a pill in the editor
// and serialized to a bare `{{path}}` token (see mergeTagHtml.serializeMergeTags)
// so the backend's InterpolateTemplate resolves it. Being an atom, the {{path}}
// can't be partially edited/broken, and it round-trips through the stored body_json.
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
    };
  },

  parseHTML() {
    return [{ tag: 'span[data-merge-tag]' }];
  },

  renderHTML({ node, HTMLAttributes }) {
    // The text content IS the {{path}} token; serializeMergeTags later strips the
    // wrapping span so the sent body_html is clean.
    return ['span', mergeAttributes(HTMLAttributes, { class: 'merge-tag' }), `{{${node.attrs.path}}}`];
  },

  renderText({ node }) {
    return `{{${node.attrs.path}}}`;
  },

  addInputRules() {
    // Typing `{{contact.email}}` auto-converts to a chip (paths are word chars + dots).
    return [
      nodeInputRule({
        find: /\{\{([\w.]+)\}\}$/,
        type: this.type,
        getAttributes: (match) => ({ path: match[1] }),
      }),
    ];
  },
});

export default MergeTag;
