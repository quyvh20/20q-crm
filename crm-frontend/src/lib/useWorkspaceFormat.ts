import { useQuery } from '@tanstack/react-query';
import { getCurrentWorkspace, type WorkspaceDetail } from './api';
import { resolveCurrency, resolveLocale } from './format';

// useWorkspaceFormat is the one place the UI learns which currency and locale to
// render in. It exists as its own file, and exports no component, so that
// react-refresh/only-export-components never fires on it — the warning budget is
// at its ceiling, and a hook exported beside a component costs one warning per
// export. Keep this file component-free (see lib/format.ts for the same rule).
//
// The workspace's currency/locale are NOT in the auth context: `Workspace`
// (lib/api.ts) carries only org_id/name/type/role/status, so the values have to
// come from GET /api/workspaces/current. That route is NOT capability-gated —
// every member of a workspace can read it — unlike the PATCH beside it, which
// requires org.settings. A caller who somehow can't read it just gets the
// fallbacks.

/**
 * Same react-query key as `settingsKeys.workspace()` in
 * features/settings/queries.ts, ON PURPOSE: the settings form and every money
 * label then share ONE cache entry, so saving a new currency in Workspace
 * General immediately re-renders the deal pages instead of leaving them on a
 * stale copy until their own staleTime expires. Duplicated as a literal rather
 * than imported because lib/ must not depend on features/ — the test in
 * lib/__tests__/format.test.ts asserts the two stay identical.
 */
export const workspaceFormatQueryKey = ['settings', 'workspace'] as const;

/**
 * Exactly the shape lib/format.ts's helpers take as their options bag, so a
 * call site reads `formatCurrency(deal.value, fmt)` with no re-spelling.
 */
export interface WorkspaceFormat {
  /** Canonical BCP-47 tag, or undefined to mean "use the runtime's locale". */
  locale: string | undefined;
  /** Always a well-formed ISO 4217 code — falls back to USD, never blank. */
  currency: string;
}

/**
 * The active workspace's display currency and locale, already normalised.
 *
 * Never suspends and never throws: while the fetch is in flight — or if it fails
 * outright — this returns the runtime locale and USD, so a page renders
 * plausible money immediately and sharpens when the workspace arrives. The
 * values are cached for five minutes; at this product's scale one workspace read
 * per session is the entire cost.
 */
export function useWorkspaceFormat(): WorkspaceFormat {
  const { data } = useQuery<WorkspaceDetail>({
    queryKey: workspaceFormatQueryKey,
    queryFn: getCurrentWorkspace,
    staleTime: 5 * 60_000,
    gcTime: 30 * 60_000,
    // One failure is enough. Formatting is decorative — retrying three times to
    // discover we still have to fall back to USD helps nobody.
    retry: false,
  });

  return {
    locale: resolveLocale(data?.locale),
    currency: resolveCurrency(data?.currency),
  };
}
