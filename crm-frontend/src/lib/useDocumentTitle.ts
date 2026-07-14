import { useEffect } from 'react';

// The tab title used to be static (index.html's <title>), so all ~35 routes were
// indistinguishable in the tab strip, in history, and in bookmarks (U7.2).
//
// The router is react-router-dom's DECLARATIVE API (<Routes>/<Route>), not the
// data router — there is no route `handle`/loader to hang a title off — so titles
// are set by this hook instead.
//
// Nothing is restored on unmount: every route sets its own title on mount, and a
// restore-on-unmount would race the next route's set (unmount effects run before
// the incoming page's effects only for the *outgoing* tree — relying on that
// ordering to hand back a "previous" title is how titles end up one page behind).
//
// A falsy title yields the bare app name rather than "undefined · Guerrilla CRM",
// so a detail page whose record is still loading shows the product, never a
// broken or blank tab.

export const APP_NAME = 'Guerrilla CRM';

/** Sets `document.title` to "<title> · Guerrilla CRM", or the bare app name when
 *  no title is given (e.g. a dynamic page whose record hasn't loaded yet). */
export function useDocumentTitle(title?: string | null) {
  useEffect(() => {
    const trimmed = title?.trim();
    document.title = trimmed ? `${trimmed} · ${APP_NAME}` : APP_NAME;
  }, [title]);
}

/**
 * Component form of the hook, for LAYOUTS (AppLayout, SettingsLayout).
 *
 * A layout must not call the hook itself: React runs a parent's effects AFTER its
 * children's, so `useDocumentTitle(props.title)` in a layout fires last and stamps
 * its value — including `undefined` → the bare app name — on top of whatever the
 * page inside it just set. Every dynamic page (a deal, a record, a workflow) would
 * lose its title.
 *
 * Rendered as a component placed BEFORE {children}, the effect runs FIRST, so a
 * page that sets its own title still wins. The layout is then only the fallback
 * for static pages, which is exactly the intent. Render it conditionally (`{title
 * && <DocumentTitle …/>}`) so a layout with no title of its own touches nothing.
 */
export function DocumentTitle({ title }: { title?: string | null }): null {
  useDocumentTitle(title);
  return null;
}

export default useDocumentTitle;
