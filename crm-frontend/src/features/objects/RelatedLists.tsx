import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { listRecordRelatedLists, type RelatedList } from '../../lib/api';
import { recordPath } from './recordRoutes';

interface RelatedListsProps {
  slug: string;
  recordId: string;
  /**
   * Parent-owned data: an array renders directly, null means the parent's
   * request is still in flight (show loading, don't fetch). Omit entirely
   * (undefined) for standalone usage where the component fetches for itself.
   */
  prefetchedLists?: RelatedList[] | null;
}

// RelatedLists renders a record's reverse relationships: for every object that
// points back at this record through a typed relation field, a titled group of
// those child records (e.g. a Contact's "Deals"). It is schema-driven — the
// backend derives the groups from the registry — so no per-object code is needed,
// and each row links to the child's own record page.
export default function RelatedLists({ slug, recordId, prefetchedLists }: RelatedListsProps) {
  // Managed mode: the parent owns fetching and may hydrate the prop after
  // mount (null → data), so never start a duplicate request of our own.
  const managed = prefetchedLists !== undefined;
  const [ownLists, setOwnLists] = useState<RelatedList[]>([]);
  const [ownLoading, setOwnLoading] = useState(!managed);
  const [error, setError] = useState('');

  useEffect(() => {
    if (managed) return;
    let cancelled = false;
    setOwnLoading(true);
    setError('');
    listRecordRelatedLists(slug, recordId)
      .then((l) => {
        if (!cancelled) setOwnLists(l);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load related records');
      })
      .finally(() => {
        if (!cancelled) setOwnLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [slug, recordId, managed]);

  const lists = managed ? prefetchedLists ?? [] : ownLists;
  const loading = managed ? prefetchedLists === null : ownLoading;

  // Only groups that actually have records are worth showing; an object that
  // could relate but doesn't yet would just be visual noise.
  const nonEmpty = lists.filter((l) => l.records.length > 0);

  if (loading) {
    return (
      <div className="mt-2 border-t border-border pt-4 text-sm text-muted-foreground">
        Loading related records…
      </div>
    );
  }

  if (error) {
    return (
      <div className="mt-2 border-t border-border pt-4">
        <div className="rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
      </div>
    );
  }

  if (nonEmpty.length === 0) {
    return (
      <div className="mt-2 border-t border-border pt-4 text-sm text-muted-foreground">
        No related records
      </div>
    );
  }

  return (
    <div className="mt-2 border-t border-border pt-4">
      {nonEmpty.map((group) => (
        <div key={`${group.object}:${group.field_key}`} className="mb-6">
          <div className="mb-2 flex items-center gap-2">
            <span className="flex items-center gap-1.5 text-sm font-semibold text-foreground">
              {/* The group icon is schema data (the target object's emoji), shown
                  in a neutral chip rather than as bare glyph chrome. */}
              <span aria-hidden className="flex h-6 w-6 items-center justify-center rounded bg-muted text-sm">
                {group.icon}
              </span>
              {group.label}
            </span>
            <span className="text-xs text-muted-foreground">
              {group.count}{group.has_more ? '+' : ''}
              {/* Which field on the child points back, so "Deals · via Contact" is unambiguous
                  when a child relates through more than one field. */}
              {' · via '}{group.field_label}
            </span>
          </div>
          <div className="divide-y divide-border overflow-hidden rounded-lg border border-border bg-card">
            {group.records.map((rec) => (
              <Link
                key={rec.id}
                to={recordPath(group.object, rec.id)}
                className="flex items-center justify-between gap-3 px-3 py-2 text-sm text-foreground no-underline transition-colors hover:bg-accent"
              >
                <span className="flex min-w-0 items-center gap-2">
                  {rec.number && (
                    <span className="whitespace-nowrap text-[11px] font-semibold text-muted-foreground">{rec.number}</span>
                  )}
                  <span className="truncate font-medium">{rec.display || 'Untitled'}</span>
                </span>
                <span className="whitespace-nowrap text-xs text-muted-foreground">
                  {new Date(rec.created_at).toLocaleDateString()}
                </span>
              </Link>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
