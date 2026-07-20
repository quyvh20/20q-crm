// Derivation for the sidebar's "Records" section.
//
// The nav is driven off the object REGISTRY (/api/registry/objects), which returns
// system and custom objects alike. It used to read /api/objects — the legacy
// custom-object endpoint, backed by `custom_object_defs`, which by construction can
// never return a system object. Contacts and Deals papered over that by being
// hardcoded NavLinks, which left Company — a first-class system object with its own
// table, registry entry, REST routes and OLS gates — with nowhere to appear.
//
// Kept out of AppLayout.tsx so the ordering and fallback rules are testable without
// mounting the whole app shell.

import type { ObjectSummary } from "../../lib/api";

/** The subset of an ObjectSummary the nav actually renders. */
export type NavObject = Pick<ObjectSummary, "slug" | "label_plural"> & { icon?: string };

/**
 * Registry order is already system-first, but the three system objects are pinned
 * explicitly so the nav can't be silently reordered by a change to the backend seed
 * or to ListDefs' ORDER BY. Slugs are stable — see systemObjectSpecs in the
 * backend's internal/repository/object_registry_repository.go.
 */
export const SYSTEM_NAV_ORDER = ["contact", "company", "deal"];

/**
 * A failed registry fetch must not empty the Records section — that would drop
 * Contacts and Deals out of the nav, something the previous hardcoded links could
 * never do. Falling back to the known system objects keeps the app navigable, and
 * the schema-driven pages behind these routes degrade on their own.
 */
export const SYSTEM_NAV_FALLBACK: NavObject[] = [
  { slug: "contact", label_plural: "Contacts" },
  { slug: "company", label_plural: "Companies" },
  { slug: "deal", label_plural: "Deals" },
];

/**
 * Sorts system objects into SYSTEM_NAV_ORDER and leaves everything else in registry
 * order. Array#sort is stable, so custom objects keep the order the backend chose.
 */
export function orderNavObjects<T extends { slug: string }>(objects: T[]): T[] {
  const rank = (slug: string) => {
    const i = SYSTEM_NAV_ORDER.indexOf(slug);
    return i === -1 ? SYSTEM_NAV_ORDER.length : i;
  };
  return [...objects].sort((a, b) => rank(a.slug) - rank(b.slug));
}
