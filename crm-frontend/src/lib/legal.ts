// Single source of truth for the Terms / Privacy destinations (U7.6).
//
// An operator running this CRM almost certainly has real legal pages on their
// marketing site, so both URLs are configurable at build time. When they are NOT
// configured we fall back to the in-app placeholder routes (/terms, /privacy) so
// the consent line never points at a 404 — a signup flow that promises terms and
// then can't show them is worse than no link at all.
//
// External URLs must open in a new tab (leaving a half-finished signup to read
// the terms would lose the form), and cross-origin targets need
// rel="noopener noreferrer". Internal routes stay in the SPA via <Link>.

const rawTerms = import.meta.env.VITE_TERMS_URL?.trim();
const rawPrivacy = import.meta.env.VITE_PRIVACY_URL?.trim();

/** True for anything that leaves the SPA — an absolute http(s) URL. */
function isExternalUrl(url: string | undefined): boolean {
  return !!url && /^https?:\/\//i.test(url);
}

export const TERMS_URL: string = rawTerms || '/terms';
export const PRIVACY_URL: string = rawPrivacy || '/privacy';

export const TERMS_IS_EXTERNAL: boolean = isExternalUrl(rawTerms);
export const PRIVACY_IS_EXTERNAL: boolean = isExternalUrl(rawPrivacy);

/** True when BOTH legal destinations are hosted elsewhere — i.e. the in-app
 *  placeholder pages are unreachable from the consent line. The routes stay
 *  registered regardless (an old emailed link may still point at them). */
export const LEGAL_IS_EXTERNAL: boolean = TERMS_IS_EXTERNAL && PRIVACY_IS_EXTERNAL;
