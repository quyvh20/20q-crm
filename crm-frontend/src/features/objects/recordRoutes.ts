// Shared route helpers so every entry point (list rows, kanban cards, global
// search, and the detail page's own back link) agrees on where a record lives.
//
// Deals keep their bespoke, feature-rich detail page (tasks, activity timeline,
// AI scoring) at /deals/:id — P8 deliberately excludes deals from the generic
// layout engine. Every other object routes to the unified record page.

export function recordPath(slug: string, id: string): string {
  if (slug === 'deal') return `/deals/${id}`;
  return `/objects/${slug}/records/${id}`;
}

export function listPath(slug: string): string {
  if (slug === 'contact') return '/contacts';
  if (slug === 'deal') return '/deals';
  return `/objects/${slug}`;
}
