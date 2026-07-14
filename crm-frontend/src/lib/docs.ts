// Single source of truth for the "Help & docs" destination (U7.5).
//
// Mirrors src/lib/legal.ts: an operator running this CRM points VITE_DOCS_URL at
// whatever they actually publish (a docs site, a Notion space, a support desk, or
// even a mailto: address for a small team), so the destination is configured at
// build time rather than hardcoded.
//
// The ONE difference from legal.ts: Terms/Privacy fall back to the in-app
// placeholder routes (/terms, /privacy) because those pages exist. There is no
// in-app help centre to fall back to, and a "Help & docs" entry that opens a 404
// is worse than no entry at all — so when VITE_DOCS_URL is unset, DOCS_ENABLED is
// false and the caller renders nothing.

const rawDocs = import.meta.env.VITE_DOCS_URL?.trim();

/** The configured help destination, or '' when the operator hasn't set one. */
export const DOCS_URL: string = rawDocs || '';

/** Whether to render the help entry at all. False = no dead link. */
export const DOCS_ENABLED: boolean = DOCS_URL !== '';

/** True for anything that leaves the SPA — an absolute http(s) URL. mailto: and
 *  in-app paths are handled by the caller (a mailto never gets target=_blank). */
export const DOCS_IS_EXTERNAL: boolean = /^https?:\/\//i.test(DOCS_URL);
