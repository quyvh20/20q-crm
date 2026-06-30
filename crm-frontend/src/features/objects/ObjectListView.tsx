import { useState, useEffect, useCallback, useMemo } from 'react';
import { useNavigate, Link } from 'react-router-dom';
import {
  getObjectSchema,
  listObjectRecordsUnified,
  getTags,
  getStages,
  type ObjectSchema,
  type ObjectFieldDescriptor,
  type UniformRecord,
  type Tag,
} from '../../lib/api';
import { formatFieldValue } from './fieldHelpers';
import ObjectForm from './ObjectForm';
import ObjectKanban from './ObjectKanban';
import ImportModal from '../../components/contacts/ImportModal';
import { recordPath } from './recordRoutes';

interface ObjectListViewProps {
  slug: string;
  /** Called when the object/schema can't be loaded (e.g. unknown slug). */
  onNotFound?: () => void;
}

// Viewing/editing/deleting a record now happens on its own URL-addressable page
// (ObjectRecordPage); the list only opens the shared create form in a slide-over.
type Panel =
  | { mode: 'create' }
  | null;

interface RelationOption {
  id: string;
  label: string;
}

const LIMIT = 25;
const MAX_COLUMNS = 4;

// ObjectListView renders any object — system or custom — from its registry
// schema: one table, one schema-driven filter bar, one search box, one
// create/edit/detail slide-over. It is the component every object page now points
// at (P3 "one renderer"); the filter bar brings it to parity with the legacy
// per-object pages (P7) — relation filters, tag filter, and semantic search all
// driven by the schema, with zero per-object code.
export default function ObjectListView({ slug, onNotFound }: ObjectListViewProps) {
  const navigate = useNavigate();
  const openRecord = useCallback(
    (rec: UniformRecord) => navigate(recordPath(slug, rec.id)),
    [navigate, slug],
  );
  const [schema, setSchema] = useState<ObjectSchema | null>(null);
  const [records, setRecords] = useState<UniformRecord[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [search, setSearch] = useState('');
  const [debouncedSearch, setDebouncedSearch] = useState('');
  const [panel, setPanel] = useState<Panel>(null);
  const [showImport, setShowImport] = useState(false);

  // CSV import is a contact-specific affordance (the bulk importer is contact-aware).
  const supportsImport = slug === 'contact';

  // Filters (parity with the legacy pages): relation field key → selected id,
  // tag ids (any-match), and a semantic toggle.
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [tagIds, setTagIds] = useState<string[]>([]);
  const [semantic, setSemantic] = useState(false);
  const [relationOptions, setRelationOptions] = useState<Record<string, RelationOption[]>>({});
  const [tags, setTags] = useState<Tag[]>([]);

  // Relation fields the schema says we can filter on (a resolvable target object).
  const relationFields = useMemo(
    () => (schema?.fields ?? []).filter((f) => f.type === 'relation' && f.target_slug),
    [schema],
  );
  // Semantic search is wired for contacts (native vector index).
  const supportsSemantic = slug === 'contact';
  // A relation field named "stage" makes the object board-able (deals today).
  const stageField = useMemo(
    () => (schema?.fields ?? []).find((f) => f.key === 'stage' && f.type === 'relation'),
    [schema],
  );
  const [view, setView] = useState<'table' | 'board'>('table');

  // Reset transient state when switching objects.
  useEffect(() => {
    setSchema(null);
    setSearch('');
    setDebouncedSearch('');
    setPanel(null);
    setFilters({});
    setTagIds([]);
    setSemantic(false);
    setRelationOptions({});
    setView('table');
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

  // Load tags for the tag filter (every object is taggable).
  useEffect(() => {
    let cancelled = false;
    getTags()
      .then((t) => {
        if (!cancelled) setTags(t);
      })
      .catch(() => {
        if (!cancelled) setTags([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Load options for each filterable relation field from its target object.
  useEffect(() => {
    let cancelled = false;
    relationFields.forEach((f) => {
      listObjectRecordsUnified(f.target_slug as string, { limit: 200 })
        .then((page) => {
          if (cancelled) return;
          setRelationOptions((prev) => ({
            ...prev,
            [f.key]: page.records.map((r) => ({ id: r.id, label: r.display || r.id })),
          }));
        })
        .catch(() => {
          /* a relation we can't enumerate is simply not filterable */
        });
    });
    return () => {
      cancelled = true;
    };
  }, [relationFields]);

  // A "stage" relation has no registry target (pipeline_stages isn't a registered
  // object), so its labels come from the pipeline stages. Loaded into the same
  // option map so relation cells resolve to the stage name, not its id.
  useEffect(() => {
    if (!stageField) return;
    let cancelled = false;
    getStages()
      .then((s) => {
        if (!cancelled) {
          setRelationOptions((prev) => ({ ...prev, [stageField.key]: s.map((st) => ({ id: st.id, label: st.name })) }));
        }
      })
      .catch(() => {
        /* stage labels just fall back to the id */
      });
    return () => {
      cancelled = true;
    };
  }, [stageField]);

  // Resolve a relation field's id value to the target's display label, using the
  // already-loaded option maps. Falls back to the raw value when unknown.
  const relationLabel = useCallback(
    (field: ObjectFieldDescriptor, value: unknown): string | undefined => {
      if (field.type !== 'relation' || value == null || value === '') return undefined;
      const opt = (relationOptions[field.key] ?? []).find((o) => o.id === value);
      return opt?.label;
    },
    [relationOptions],
  );

  // Debounce the search box.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300);
    return () => clearTimeout(t);
  }, [search]);

  const listParams = useCallback(
    (cursor?: string) => ({
      limit: LIMIT,
      q: debouncedSearch,
      cursor,
      filters,
      tagIds,
      semantic: semantic && supportsSemantic,
    }),
    [debouncedSearch, filters, tagIds, semantic, supportsSemantic],
  );

  const fetchFirstPage = useCallback(async () => {
    setLoading(true);
    try {
      const page = await listObjectRecordsUnified(slug, listParams());
      setRecords(page.records);
      setNextCursor(page.next_cursor);
    } catch {
      setRecords([]);
      setNextCursor(undefined);
    } finally {
      setLoading(false);
    }
  }, [slug, listParams]);

  useEffect(() => {
    fetchFirstPage();
  }, [fetchFirstPage]);

  const loadMore = async () => {
    if (!nextCursor) return;
    setLoadingMore(true);
    try {
      const page = await listObjectRecordsUnified(slug, listParams(nextCursor));
      setRecords((prev) => [...prev, ...page.records]);
      setNextCursor(page.next_cursor);
    } catch {
      /* keep what we have */
    } finally {
      setLoadingMore(false);
    }
  };

  const closePanel = () => setPanel(null);

  const handleSaved = () => {
    closePanel();
    fetchFirstPage();
  };

  const setFilter = (key: string, value: string) => {
    setFilters((prev) => {
      const next = { ...prev };
      if (value) next[key] = value;
      else delete next[key];
      return next;
    });
  };

  const toggleTag = (id: string) => {
    setTagIds((prev) => (prev.includes(id) ? prev.filter((t) => t !== id) : [...prev, id]));
  };

  const clearFilters = () => {
    setFilters({});
    setTagIds([]);
    setSemantic(false);
    setSearch('');
  };

  const hasActiveFilters =
    Object.keys(filters).length > 0 || tagIds.length > 0 || semantic || !!search;

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
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          {stageField && (
            <div style={{ display: 'inline-flex', border: '1px solid #d1d5db', borderRadius: 8, overflow: 'hidden' }}>
              {(['table', 'board'] as const).map((v) => (
                <button
                  key={v}
                  onClick={() => setView(v)}
                  style={{
                    padding: '8px 14px', fontSize: 13, fontWeight: 600, border: 'none', cursor: 'pointer',
                    background: view === v ? '#3b82f6' : '#fff',
                    color: view === v ? '#fff' : '#64748b',
                  }}
                >
                  {v === 'table' ? 'Table' : 'Board'}
                </button>
              ))}
            </div>
          )}
          {supportsImport && (
            <button
              onClick={() => setShowImport(true)}
              style={{ padding: '10px 16px', background: '#fff', color: '#334155', border: '1px solid #d1d5db', borderRadius: 8, cursor: 'pointer', fontWeight: 600, fontSize: 14 }}
            >
              Import
            </button>
          )}
          <button
            onClick={() => setPanel({ mode: 'create' })}
            style={{ padding: '10px 20px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 8, cursor: 'pointer', fontWeight: 600, fontSize: 14 }}
          >
            + Add {schema.label}
          </button>
        </div>
      </div>

      {showImport && (
        <ImportModal
          onClose={() => setShowImport(false)}
          onSuccess={() => {
            setShowImport(false);
            fetchFirstPage();
          }}
        />
      )}

      {view === 'board' && stageField ? (
        <ObjectKanban
          schema={schema}
          stageKey={stageField.key}
          onCardClick={openRecord}
        />
      ) : (
      <>
      {/* Search + filters */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', marginBottom: 16 }}>
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={semantic && supportsSemantic ? `Describe the ${schema.label.toLowerCase()} you want…` : `Search ${schema.label_plural.toLowerCase()}...`}
          style={{ width: 280, padding: '8px 12px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14 }}
        />

        {supportsSemantic && (
          <button
            onClick={() => setSemantic((v) => !v)}
            title="Toggle AI semantic search"
            style={{
              padding: '8px 12px', borderRadius: 6, fontSize: 13, fontWeight: 600, cursor: 'pointer',
              border: semantic ? '1px solid #6366f1' : '1px solid #d1d5db',
              background: semantic ? '#eef2ff' : '#fff',
              color: semantic ? '#4f46e5' : '#64748b',
            }}
          >
            ✦ AI Search{semantic ? ' ON' : ''}
          </button>
        )}

        {/* One dropdown per filterable relation field (company, contact, …). */}
        {relationFields.map((f: ObjectFieldDescriptor) => (
          <select
            key={f.key}
            value={filters[f.key] ?? ''}
            onChange={(e) => setFilter(f.key, e.target.value)}
            style={{ padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, background: '#fff', maxWidth: 200 }}
          >
            <option value="">All {f.label}</option>
            {(relationOptions[f.key] ?? []).map((o) => (
              <option key={o.id} value={o.id}>{o.label}</option>
            ))}
          </select>
        ))}

        {hasActiveFilters && (
          <button
            onClick={clearFilters}
            style={{ padding: '8px 12px', borderRadius: 6, fontSize: 13, border: '1px solid #d1d5db', background: '#fff', color: '#64748b', cursor: 'pointer' }}
          >
            Clear filters
          </button>
        )}
      </div>

      {/* Tag filter chips */}
      {tags.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 16 }}>
          {tags.map((t) => {
            const active = tagIds.includes(t.id);
            return (
              <button
                key={t.id}
                onClick={() => toggleTag(t.id)}
                style={{
                  padding: '4px 10px', borderRadius: 999, fontSize: 12, fontWeight: 500, cursor: 'pointer',
                  border: active ? `1px solid ${t.color}` : '1px solid #e2e8f0',
                  background: active ? `${t.color}20` : '#fff',
                  color: active ? t.color : '#64748b',
                }}
              >
                {t.name}{active ? ' ✓' : ''}
              </button>
            );
          })}
        </div>
      )}

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
                {hasActiveFilters
                  ? `No ${schema.label_plural.toLowerCase()} match your filters.`
                  : `No ${schema.label_plural.toLowerCase()} yet. Click "+ Add ${schema.label}" to create one.`}
              </td></tr>
            ) : (
              records.map((rec) => (
                <tr
                  key={rec.id}
                  onClick={() => openRecord(rec)}
                  style={{ borderBottom: '1px solid #f1f5f9', cursor: 'pointer' }}
                >
                  <td style={{ padding: '10px 12px', fontWeight: 500 }}>
                    {/* A real link so Cmd/Ctrl/middle-click opens the record in a
                        new tab (Salesforce-style); plain click is SPA navigation.
                        stopPropagation keeps the row's onClick from double-firing. */}
                    <Link
                      to={recordPath(slug, rec.id)}
                      onClick={(e) => e.stopPropagation()}
                      style={{ color: 'inherit', textDecoration: 'none' }}
                    >
                      {rec.display || 'Untitled'}
                    </Link>
                  </td>
                  {columns.map((f) => (
                    <td key={f.key} style={{ padding: '10px 12px', fontSize: 13 }}>
                      {formatFieldValue(f, rec.fields[f.key], relationLabel(f, rec.fields[f.key]))}
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
      </>
      )}

      {/* Create popup (view/edit/delete live on the record page now). A centered
          modal — clicking the backdrop closes it; the inner box stops the click
          from bubbling so clicks inside the form don't dismiss it. */}
      {panel?.mode === 'create' && (
        <div
          onClick={closePanel}
          style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', zIndex: 50, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 16 }}
        >
          <div
            onClick={(e) => e.stopPropagation()}
            style={{ width: 480, maxWidth: '100%', maxHeight: '90vh', background: '#fff', borderRadius: 12, boxShadow: '0 20px 60px rgba(0,0,0,0.25)', overflow: 'hidden', display: 'flex', flexDirection: 'column' }}
          >
            <ObjectForm schema={schema} onSaved={handleSaved} onCancel={closePanel} />
          </div>
        </div>
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
