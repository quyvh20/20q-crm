import { useState, useEffect, useCallback } from 'react';
import {
  getObjectSchema,
  listObjectRecordsUnified,
  deleteObjectRecordUnified,
  type ObjectSchema,
  type UniformRecord,
} from '../../lib/api';
import { formatFieldValue } from './fieldHelpers';
import ObjectForm from './ObjectForm';
import ObjectDetailView from './ObjectDetailView';

interface ObjectListViewProps {
  slug: string;
  /** Called when the object/schema can't be loaded (e.g. unknown slug). */
  onNotFound?: () => void;
}

type Panel =
  | { mode: 'create' }
  | { mode: 'view'; record: UniformRecord }
  | { mode: 'edit'; record: UniformRecord }
  | { mode: 'confirmDelete'; record: UniformRecord }
  | null;

const LIMIT = 25;
const MAX_COLUMNS = 4;

// ObjectListView renders any object — system or custom — from its registry
// schema: one table, one search box, one create/edit/detail slide-over. It is the
// component every object page now points at (P3 "one renderer").
export default function ObjectListView({ slug, onNotFound }: ObjectListViewProps) {
  const [schema, setSchema] = useState<ObjectSchema | null>(null);
  const [records, setRecords] = useState<UniformRecord[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [panel, setPanel] = useState<Panel>(null);
  const [actionError, setActionError] = useState('');

  // Reset transient state when switching objects.
  useEffect(() => {
    setSchema(null);
    setSearch('');
    setDebouncedSearch('');
    setPanel(null);
  }, [slug]);

  useEffect(() => {
    let cancelled = false;
    getObjectSchema(slug)
      .then((s) => {
        if (!cancelled) setSchema(s);
      })
      .catch(() => {
        if (!cancelled) onNotFound?.();
      });
    return () => {
      cancelled = true;
    };
  }, [slug, onNotFound]);

  // Debounce the search box.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  const fetchFirstPage = useCallback(async () => {
    setLoading(true);
    try {
      const page = await listObjectRecordsUnified(slug, { limit: LIMIT, q: debouncedSearch });
      setRecords(page.records);
      setNextCursor(page.next_cursor);
    } catch {
      setRecords([]);
      setNextCursor(undefined);
    } finally {
      setLoading(false);
    }
  }, [slug, debouncedSearch]);

  useEffect(() => {
    fetchFirstPage();
  }, [fetchFirstPage]);

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const page = await listObjectRecordsUnified(slug, { limit: LIMIT, q: debouncedSearch, cursor: nextCursor });
      setRecords((prev) => [...prev, ...page.records]);
      setNextCursor(page.next_cursor);
    } catch {
      /* keep what we have */
    } finally {
      setLoadingMore(false);
    }
  };

  const closePanel = () => {
    setPanel(null);
    setActionError('');
  };

  const handleSaved = () => {
    closePanel();
    fetchFirstPage();
  };

  const handleConfirmDelete = async (record: UniformRecord) => {
    setActionError('');
    try {
      await deleteObjectRecordUnified(slug, record.id);
      closePanel();
      fetchFirstPage();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : 'Delete failed');
    }
  };

  if (!schema) {
    return <div style={{ padding: 40, color: '#94a3b8', textAlign: 'center' }}>Loading...</div>;
  }

  const columns = schema.fields.slice(0, MAX_COLUMNS);

  return (
    <div>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 700, margin: 0 }}>{schema.icon} {schema.label_plural}</h1>
          <p style={{ color: '#64748b', marginTop: 4, fontSize: 14 }}>Manage your {schema.label_plural.toLowerCase()}</p>
        </div>
        <button
          onClick={() => setPanel({ mode: 'create' })}
          style={{ padding: '10px 20px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 8, cursor: 'pointer', fontWeight: 600, fontSize: 14 }}
        >
          + Add {schema.label}
        </button>
      </div>

      {/* Search */}
      <div style={{ marginBottom: 16 }}>
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={`Search ${schema.label_plural.toLowerCase()}...`}
          style={{ width: 300, padding: '8px 12px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14 }}
        />
      </div>

      {/* Records table */}
      <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, overflow: 'hidden', background: '#fff' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #e2e8f0', background: '#f8fafc' }}>
              <th style={thStyle}>Name</th>
              {columns.map((f) => (
                <th key={f.key} style={thStyle}>{f.label}</th>
              ))}
              <th style={thStyle}>Created</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={columns.length + 2} style={{ padding: 40, textAlign: 'center', color: '#94a3b8' }}>Loading...</td></tr>
            ) : records.length === 0 ? (
              <tr><td colSpan={columns.length + 2} style={{ padding: 40, textAlign: 'center', color: '#94a3b8' }}>
                <div style={{ fontSize: 32, marginBottom: 8 }}>{schema.icon}</div>
                No {schema.label_plural.toLowerCase()} yet. Click "+ Add {schema.label}" to create one.
              </td></tr>
            ) : (
              records.map((rec) => (
                <tr
                  key={rec.id}
                  onClick={() => setPanel({ mode: 'view', record: rec })}
                  style={{ borderBottom: '1px solid #f1f5f9', cursor: 'pointer' }}
                >
                  <td style={{ padding: '10px 12px', fontWeight: 500 }}>{rec.display || 'Untitled'}</td>
                  {columns.map((f) => (
                    <td key={f.key} style={{ padding: '10px 12px', fontSize: 13 }}>
                      {formatFieldValue(f, rec.fields[f.key])}
                    </td>
                  ))}
                  <td style={{ padding: '10px 12px', fontSize: 13, color: '#64748b' }}>
                    {new Date(rec.created_at).toLocaleDateString()}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Load more */}
      {nextCursor && (
        <div style={{ display: 'flex', justifyContent: 'center', marginTop: 16 }}>
          <button
            onClick={loadMore}
            disabled={loadingMore}
            style={{ padding: '8px 18px', border: '1px solid #d1d5db', borderRadius: 6, background: '#fff', cursor: loadingMore ? 'default' : 'pointer' }}
          >
            {loadingMore ? 'Loading...' : 'Load more'}
          </button>
        </div>
      )}

      <div style={{ marginTop: 8, textAlign: 'center', color: '#94a3b8', fontSize: 13 }}>
        Showing {records.length} {schema.label_plural.toLowerCase()}
      </div>

      {/* Slide-over */}
      {panel && (
        <>
          <div onClick={closePanel} style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.3)', zIndex: 49 }} />
          <div style={{ position: 'fixed', top: 0, right: 0, bottom: 0, width: 420, background: '#fff', boxShadow: '-4px 0 20px rgba(0,0,0,0.1)', zIndex: 50 }}>
            {panel.mode === 'create' && (
              <ObjectForm schema={schema} onSaved={handleSaved} onCancel={closePanel} />
            )}
            {panel.mode === 'edit' && (
              <ObjectForm schema={schema} record={panel.record} onSaved={handleSaved} onCancel={closePanel} />
            )}
            {panel.mode === 'view' && (
              <ObjectDetailView
                schema={schema}
                record={panel.record}
                onEdit={() => setPanel({ mode: 'edit', record: panel.record })}
                onDelete={() => setPanel({ mode: 'confirmDelete', record: panel.record })}
                onClose={closePanel}
              />
            )}
            {panel.mode === 'confirmDelete' && (
              <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
                <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0' }}>
                  <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>Delete {schema.label}?</h3>
                </div>
                <div style={{ flex: 1, padding: 24, fontSize: 14, color: '#334155' }}>
                  {actionError && (
                    <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 16, fontSize: 13 }}>{actionError}</div>
                  )}
                  This permanently removes <strong>{panel.record.display || 'this record'}</strong>. This cannot be undone.
                </div>
                <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', gap: 8 }}>
                  <button onClick={() => setPanel({ mode: 'view', record: panel.record })} style={{ flex: 1, padding: '10px', background: '#f1f5f9', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Cancel</button>
                  <button onClick={() => handleConfirmDelete(panel.record)} style={{ flex: 1, padding: '10px', background: '#ef4444', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600 }}>Delete</button>
                </div>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}

const thStyle = {
  padding: '10px 12px',
  textAlign: 'left' as const,
  fontSize: 12,
  fontWeight: 600,
  color: '#64748b',
  textTransform: 'uppercase' as const,
};
