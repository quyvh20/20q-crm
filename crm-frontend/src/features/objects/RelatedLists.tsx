import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { listRecordRelatedLists, type RelatedList } from '../../lib/api';
import { recordPath } from './recordRoutes';

interface RelatedListsProps {
  slug: string;
  recordId: string;
}

// RelatedLists renders a record's reverse relationships: for every object that
// points back at this record through a typed relation field, a titled group of
// those child records (e.g. a Contact's "Deals"). It is schema-driven — the
// backend derives the groups from the registry — so no per-object code is needed,
// and each row links to the child's own record page.
export default function RelatedLists({ slug, recordId }: RelatedListsProps) {
  const [lists, setLists] = useState<RelatedList[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    listRecordRelatedLists(slug, recordId)
      .then((l) => {
        if (!cancelled) setLists(l);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : 'Failed to load related records');
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [slug, recordId]);

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
              {group.count}
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
                <span style={{ fontWeight: 500 }}>{rec.display || 'Untitled'}</span>
                <span style={{ color: '#94a3b8', fontSize: 12 }}>
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
