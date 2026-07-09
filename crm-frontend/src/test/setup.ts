import '@testing-library/jest-dom/vitest';

// jsdom doesn't implement layout, so getClientRects()/getBoundingClientRect()
// are missing on Range (and return nothing useful on Element). ProseMirror/TipTap
// call these during scrollToSelection after an edit, which otherwise throws an
// unhandled "target.getClientRects is not a function" during editor tests. Provide
// no-op geometry so the editor can run headless.
const emptyRect = () => ({
  x: 0, y: 0, width: 0, height: 0, top: 0, left: 0, right: 0, bottom: 0,
  toJSON: () => ({}),
});
const emptyRectList = () => Object.assign([], { item: () => null }) as unknown as DOMRectList;

if (typeof Range !== 'undefined') {
  Range.prototype.getClientRects = emptyRectList as unknown as Range['getClientRects'];
  Range.prototype.getBoundingClientRect = emptyRect as unknown as Range['getBoundingClientRect'];
}
if (typeof Element !== 'undefined' && !Element.prototype.getClientRects) {
  Element.prototype.getClientRects = emptyRectList as unknown as Element['getClientRects'];
}
