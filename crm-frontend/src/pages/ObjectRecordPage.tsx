import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  getObjectSchema,
  getObjectRecordUnified,
  deleteObjectRecordUnified,
  listRecordRelatedLists,
  listRecordTags,
  getTags,
  type ObjectSchema,
  type UniformRecord,
  type RelatedList,
  type Tag,
} from '../lib/api';
import { ObjectDetailView, ObjectForm } from '../features/objects';
import { listPath } from '../features/objects/recordRoutes';

// ObjectRecordPage is the Salesforce-style, URL-addressable detail page for any
// object — /objects/:slug/records/:id. Every list row, kanban card, and global
// search hit links here (deals are the one exception: they keep their bespoke
// /deals/:id page). The record id lives in the URL, so a record page is
// shareable, bookmarkable, and survives a refresh.
//
// The body (ObjectDetailView) renders the admin/role layout when configured and a
// built-in default "Details" layout otherwise — so the page is never blank.
//
// Performance: all five requests (schema, record, related lists, tags, tag
// palette) fire in one burst, but first paint waits only for schema + record —
// the two the header and details need. The slower panels (related lists can fan
// out into many child queries server-side) hydrate in when their responses land,
// so they can never hold the whole page hostage. Child components receive the
// data as props: null means "still loading", so they don't start their own fetch.
export default function ObjectRecordPage() {
  const { slug, id } = useParams<{ slug: string; id: string }>();
  const navigate = useNavigate();

  const [schema, setSchema] = useState<ObjectSchema | null>(null);
  const [record, setRecord] = useState<UniformRecord | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const [deleting, setDeleting] = useState(false);

  // Pre-fetched data for child components; null = request still in flight.
  const [relatedLists, setRelatedLists] = useState<RelatedList[] | null>(null);
  const [recordTags, setRecordTags] = useState<Tag[] | null>(null);
  const [allTags, setAllTags] = useState<Tag[] | null>(null);

  // Guards against a stale load overwriting a newer one (e.g. clicking through
  // to a related record while the first page's slower requests are in flight).
  const loadGen = useRef(0);

  const load = useCallback(async () => {
    if (!slug || !id) return;
    const gen = ++loadGen.current;
    const fresh = () => loadGen.current === gen;
    setLoading(true);
    setError('');
    setRelatedLists(null);
    setRecordTags(null);
    setAllTags(null);

    // Fire ALL requests at once — related lists and tags only need slug+id
    // (known from URL params), so nothing waits on anything else.
    const relatedP = listRecordRelatedLists(slug, id).catch(() => [] as RelatedList[]);
    const tagsP = listRecordTags(slug, id).catch(() => [] as Tag[]);
    const allTagsP = getTags().catch(() => [] as Tag[]);

    // First paint needs only schema + record; the slower panels hydrate in
    // whenever their responses land instead of blocking the whole page.
    try {
      const [s, r] = await Promise.all([getObjectSchema(slug), getObjectRecordUnified(slug, id)]);
      if (!fresh()) return;
      setSchema(s);
      setRecord(r);
    } catch (e) {
      if (fresh()) setError(e instanceof Error ? e.message : 'Failed to load this record.');
    } finally {
      if (fresh()) setLoading(false);
    }

    relatedP.then((rl) => { if (fresh()) setRelatedLists(rl); });
    tagsP.then((rt) => { if (fresh()) setRecordTags(rt); });
    allTagsP.then((at) => { if (fresh()) setAllTags(at); });
  }, [slug, id]);

  useEffect(() => {
    load();
  }, [load]);

  const backToList = () => navigate(slug ? listPath(slug) : '/');

  const handleDelete = async () => {
    if (!slug || !id) return;
    setDeleteError('');
    setDeleting(true);
    try {
      await deleteObjectRecordUnified(slug, id);
      backToList();
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : 'Delete failed');
      setDeleting(false);
    }
  };

  if (loading) {
    return (
      <div className="max-w-6xl mx-auto">
        <div style={{ height: 28, width: 200, background: '#f1f5f9', borderRadius: 8, marginBottom: 16 }} />
        <div style={{ height: 360, background: '#f8fafc', border: '1px solid #e2e8f0', borderRadius: 12 }} />
      </div>
    );
  }

  if (error || !schema || !record) {
    return (
      <div className="max-w-6xl mx-auto">
        <button onClick={() => navigate(slug ? listPath(slug) : '/')} style={backLinkStyle}>← Back</button>
        <div style={{ padding: 40, textAlign: 'center', color: '#64748b', border: '2px dashed #e2e8f0', borderRadius: 12 }}>
          <div style={{ fontSize: 32, marginBottom: 8 }}>🔍</div>
          {error || 'Record not found.'}
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-6xl mx-auto">
      {/* Back link */}
      <button onClick={backToList} style={backLinkStyle}>
        ← Back to {schema.label_plural}
      </button>

      {/* Header section */}
      <div
        style={{
          padding: '16px 0 32px 0',
          marginBottom: 32,
          display: 'flex',
          alignItems: 'flex-start',
          justifyContent: 'space-between',
          gap: 16,
          borderBottom: '2px solid #f1f5f9',
        }}
      >
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 4, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span>{schema.icon} {schema.label}</span>
            {record.number && (
              <span style={{ background: '#f1f5f9', color: '#475569', borderRadius: 6, padding: '1px 8px', fontWeight: 700, letterSpacing: '0.04em' }}>
                {record.number}
              </span>
            )}
          </div>
          <h1 style={{ fontSize: 24, fontWeight: 700, margin: 0, color: '#0f172a', wordBreak: 'break-word' }}>
            {record.display || 'Untitled'}
          </h1>
          <div style={{ fontSize: 12, color: '#94a3b8', marginTop: 6 }}>
            Created {new Date(record.created_at).toLocaleDateString()}
            {record.updated_at && record.updated_at !== record.created_at && (
              <> · Updated {new Date(record.updated_at).toLocaleDateString()}</>
            )}
          </div>
        </div>
        {!editing && (
          <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
            <button id="record-edit-btn" onClick={() => setEditing(true)} style={primaryBtnStyle}>Edit</button>
            <button id="record-delete-btn" onClick={() => setConfirmingDelete(true)} style={dangerBtnStyle}>Delete</button>
          </div>
        )}
      </div>

      {/* Body: inline edit form when editing, layout-driven detail otherwise.
          Editing happens on the page itself (no slide-over) so the record stays
          in full view while it's being changed. */}
      <div>
        {editing ? (
          <ObjectForm
            schema={schema}
            record={record}
            inline
            onSaved={(saved) => {
              setRecord(saved);
              setEditing(false);
            }}
            onCancel={() => setEditing(false)}
          />
        ) : (
          <ObjectDetailView
            schema={schema}
            record={record}
            prefetchedRelatedLists={relatedLists}
            prefetchedTags={recordTags}
            prefetchedAllTags={allTags}
          />
        )}
      </div>

      {/* Delete confirmation */}
      {confirmingDelete && (
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', zIndex: 60, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <div style={{ background: '#fff', borderRadius: 12, width: 420, maxWidth: '90vw', overflow: 'hidden' }}>
            <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0' }}>
              <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>Delete {schema.label}?</h3>
            </div>
            <div style={{ padding: 24, fontSize: 14, color: '#334155' }}>
              {deleteError && (
                <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 16, fontSize: 13 }}>{deleteError}</div>
              )}
              This permanently removes <strong>{record.display || 'this record'}</strong>. This cannot be undone.
            </div>
            <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', gap: 8 }}>
              <button onClick={() => { setConfirmingDelete(false); setDeleteError(''); }} style={{ flex: 1, padding: '10px', background: '#f1f5f9', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Cancel</button>
              <button id="record-delete-confirm-btn" onClick={handleDelete} disabled={deleting} style={{ flex: 1, padding: '10px', background: '#ef4444', color: '#fff', border: 'none', borderRadius: 6, cursor: deleting ? 'default' : 'pointer', fontWeight: 600, opacity: deleting ? 0.6 : 1 }}>
                {deleting ? 'Deleting…' : 'Delete'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

const backLinkStyle = {
  background: 'none',
  border: 'none',
  color: '#64748b',
  cursor: 'pointer',
  fontSize: 13,
  fontWeight: 500,
  padding: 0,
  marginBottom: 16,
};

const primaryBtnStyle = {
  padding: '8px 16px',
  background: '#3b82f6',
  color: '#fff',
  border: 'none',
  borderRadius: 8,
  cursor: 'pointer',
  fontWeight: 600,
  fontSize: 14,
};

const dangerBtnStyle = {
  padding: '8px 16px',
  background: '#fef2f2',
  color: '#dc2626',
  border: 'none',
  borderRadius: 8,
  cursor: 'pointer',
  fontWeight: 500,
  fontSize: 14,
};
