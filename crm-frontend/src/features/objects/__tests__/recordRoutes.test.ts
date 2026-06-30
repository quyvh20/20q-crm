import { describe, it, expect } from 'vitest';
import { recordPath, listPath } from '../recordRoutes';

// The routing contract is shared by every entry point that opens a record —
// list rows, the kanban board's card click, and global search — so proving it
// here covers all of them at once.
describe('recordPath', () => {
  it('routes deals to the bespoke /deals/:id page', () => {
    expect(recordPath('deal', 'd1')).toBe('/deals/d1');
  });

  it('routes contacts to the unified record page', () => {
    expect(recordPath('contact', 'c1')).toBe('/objects/contact/records/c1');
  });

  it('routes custom objects to the unified record page', () => {
    expect(recordPath('project', 'p9')).toBe('/objects/project/records/p9');
  });
});

describe('listPath', () => {
  it('maps system objects to their dedicated list routes', () => {
    expect(listPath('contact')).toBe('/contacts');
    expect(listPath('deal')).toBe('/deals');
  });

  it('maps custom objects to their generic list route', () => {
    expect(listPath('project')).toBe('/objects/project');
  });
});
