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
      <div style={{ borderTop: '1px solid #e2e8f0', paddingTop: 16, marginTop: 8, color: '#94a3b8', fontSize: 13 }}>
        Loading related records…
      </div>
    );
  }

  if (error) {
    return (
      <div style={{ borderTop: '1px solid #e2e8f0', paddingTop: 16, marginTop: 8 }}>
        <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, fontSize: 13 }}>{error}</div>
      </div>
    );
  }

  if (nonEmpty.length === 0) {
    return (
      <div style={{ borderTop: '1px solid #e2e8f0', paddingTop: 16, marginTop: 8, color: '#94a3b8', fontSize: 13 }}>
        No related records
      </div>
    );
  }

  return (
    <div style={{ borderTop: '1px solid #e2e8f0', paddingTop: 16, marginTop: 8 }}>
      {nonEmpty.map((group) => (
        <div key={`${group.object}:${group.field_key}`} style={{ marginBottom: 24 }}>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, marginBottom: 8 }}>
            <span style={{ fontSize: 13, fontWeight: 700, color: '#334155' }}>
              {group.icon} {group.label}
            </span>
            <span style={{ fontSize: 12, color: '#94a3b8' }}>
              {group.count}{group.has_more ? '+' : ''}
              {/* Which field on the child points back, so "Deals · via Contact" is unambiguous
                  when a child relates through more than one field. */}
              {' · via '}{group.field_label}
            </span>
          </div>
          <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, overflow: 'hidden' }}>
            {group.records.map((rec, i) => (
              <Link
                key={rec.id}
                to={recordPath(group.object, rec.id)}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '8px 12px',
                  fontSize: 13,
                  color: '#0f172a',
                  textDecoration: 'none',
                  borderTop: i === 0 ? 'none' : '1px solid #f1f5f9',
                }}
              >
                <span style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                  {rec.number && (
                    <span style={{ fontSize: 11, fontWeight: 600, color: '#94a3b8', whiteSpace: 'nowrap' }}>{rec.number}</span>
                  )}
                  <span style={{ fontWeight: 500, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{rec.display || 'Untitled'}</span>
                </span>
                <span style={{ color: '#94a3b8', fontSize: 12, whiteSpace: 'nowrap' }}>
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
