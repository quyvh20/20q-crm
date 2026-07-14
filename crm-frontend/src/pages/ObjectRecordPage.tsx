import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  getObjectRecordPage,
  getObjectSchema,
  getObjectRecordUnified,
  deleteObjectRecordUnified,
  listRecordRelatedLists,
  listRecordTags,
  getTags,
  type ObjectSchema,
  type UniformRecord,
  type RelatedList,
  type RecordLevel,
  type Tag,
} from '../lib/api';
import { ObjectDetailView, ObjectForm } from '../features/objects';
import { listPath } from '../features/objects/recordRoutes';
import ShareRecordModal from '../components/records/ShareRecordModal';
import AccessDeniedPanel from '../components/common/AccessDeniedPanel';
import { usePermissions } from '../lib/auth';

// ObjectRecordPage is the Salesforce-style, URL-addressable detail page for any
// object — /objects/:slug/records/:id. Every list row, kanban card, and global
// search hit links here (deals are the one exception: they keep their bespoke
// /deals/:id page). The record id lives in the URL, so a record page is
// shareable, bookmarkable, and survives a refresh.
//
// The body (ObjectDetailView) renders the admin/role layout when configured and a
// built-in default "Details" layout otherwise — so the page is never blank.
//
// Performance: the page is served by ONE composite request (/records/:id/page)
// carrying schema, record, related lists, both tag sets, and server-resolved
// relation/mirror labels — so a remote deployment pays a single network round
// trip instead of five plus one per relation field. If that endpoint is
// unavailable (deploy skew, transient failure), the page falls back to the
// original per-endpoint burst: first paint on schema + record, slower panels
// hydrating in as they land. Child components receive data as props: null means
// "still loading", so they don't start their own fetch.
export default function ObjectRecordPage() {
  const { slug, id } = useParams<{ slug: string; id: string }>();
  const navigate = useNavigate();
  // OLS-aware buttons (U3.7): Edit/Delete hide for roles whose object access
  // lacks them, instead of 403ing on click. Fails open while permissions load;
  // the server enforces every action regardless.
  const { canAccess } = usePermissions();

  const [schema, setSchema] = useState<ObjectSchema | null>(null);
  const [record, setRecord] = useState<UniformRecord | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [editing, setEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const [deleting, setDeleting] = useState(false);
  const [sharing, setSharing] = useState(false);

  // Pre-fetched data for child components; null = request still in flight.
  const [relatedLists, setRelatedLists] = useState<RelatedList[] | null>(null);
  const [recordTags, setRecordTags] = useState<Tag[] | null>(null);
  const [allTags, setAllTags] = useState<Tag[] | null>(null);
  // Server-resolved display strings (composite endpoint only). undefined means
  // "not provided" — ObjectDetailView then resolves them itself, as before.
  const [relationLabels, setRelationLabels] = useState<Record<string, string> | undefined>(undefined);
  const [mirrorValues, setMirrorValues] = useState<Record<string, string> | undefined>(undefined);
  // Row-level access for THIS record (U6), distinct from the object-level OLS
  // bits: 'manage' may share, 'edit' may edit, 'view' is read-only. undefined =
  // unknown (older server, or the per-endpoint fallback) → fail open, as the OLS
  // map does; the server enforces every action regardless.
  const [effectiveLevel, setEffectiveLevel] = useState<RecordLevel | undefined>(undefined);

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
    setRelationLabels(undefined);
    setMirrorValues(undefined);
    setEffectiveLevel(undefined);

    // One request for the whole page. On a remote backend this is the whole
    // game: every panel arrives together after a single round trip.
    try {
      const page = await getObjectRecordPage(slug, id);
      if (!fresh()) return;
      setSchema(page.schema);
      setRecord(page.record);
      setRelatedLists(page.related_lists ?? []);
      setRecordTags(page.tags ?? []);
      setAllTags(page.all_tags ?? []);
      setRelationLabels(page.relation_labels ?? {});
      setMirrorValues(page.mirror_values ?? {});
      setEffectiveLevel(page.effective_level);
      setLoading(false);
      return;
    } catch {
      if (!fresh()) return;
      // Composite endpoint unavailable or failed — fall back to the
      // per-endpoint burst, which also surfaces the real record error.
    }

    // Fallback: fire all requests at once — related lists and tags only need
    // slug+id (known from URL params), so nothing waits on anything else.
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
    // A denied read has two message flavors: the backend's OLS deny ("your
    // role can't read … records — ask an admin for access", verb varies by
    // action) and parseJsonSafe's non-JSON 403 hint ("you don't have permission
    // for this action"). Either means "denied", not "broken" — show the
    // friendly panel, not a red error.
    const accessDenied = /your role can't|you don't have permission/i.test(error);
    return (
      <div className="max-w-6xl mx-auto">
        <button onClick={() => navigate(slug ? listPath(slug) : '/')} style={backLinkStyle}>← Back</button>
        {accessDenied ? (
          <AccessDeniedPanel message={`Your role can't view ${slug ?? 'these'} records — ask an admin for access.`} />
        ) : (
          <div style={{ padding: 40, textAlign: 'center', color: '#64748b', border: '2px dashed #e2e8f0', borderRadius: 12 }}>
            <div style={{ fontSize: 32, marginBottom: 8 }}>🔍</div>
            {error || 'Record not found.'}
          </div>
        )}
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
          <div className="flex shrink-0 gap-2">
            {/* Two gates now (U6): the object-level OLS bit AND this record's
                row-level access — a record shared to you at 'view' is read-only
                even when your role may edit the object. Unknown level ⇒ shown. */}
            {slug && canAccess(slug, 'edit') && effectiveLevel !== 'view' && (
              <button
                id="record-edit-btn"
                onClick={() => setEditing(true)}
                className="rounded-lg bg-primary px-4 py-2 text-sm font-semibold text-primary-foreground hover:opacity-90"
              >
                Edit
              </button>
            )}
            {/* Sharing a record is a 'manage' action — hidden for someone who
                merely holds view/edit on it. */}
            {(effectiveLevel === undefined || effectiveLevel === 'manage') && (
              <button
                id="record-share-btn"
                onClick={() => setSharing(true)}
                className="rounded-lg border border-border bg-muted px-4 py-2 text-sm font-medium text-foreground hover:bg-accent"
              >
                Share
              </button>
            )}
            {slug && canAccess(slug, 'delete') && effectiveLevel !== 'view' && (
              <button
                id="record-delete-btn"
                onClick={() => setConfirmingDelete(true)}
                className="rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-2 text-sm font-medium text-red-600 hover:bg-red-500/20 dark:text-red-400"
              >
                Delete
              </button>
            )}
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
            prefetchedRelationLabels={relationLabels}
            prefetchedMirrorValues={mirrorValues}
          />
        )}
      </div>

      {/* Share modal (P3, I2) */}
      {sharing && slug && id && (
        <ShareRecordModal
          slug={slug}
          recordId={id}
          recordName={record.display || schema.label}
          onClose={() => setSharing(false)}
        />
      )}

      {/* Delete confirmation */}
      {confirmingDelete && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-md overflow-hidden rounded-xl border bg-card text-card-foreground shadow-xl">
            <div className="border-b px-6 py-5">
              <h3 className="text-base font-semibold">Delete {schema.label}?</h3>
            </div>
            <div className="px-6 py-5 text-sm">
              {deleteError && (
                <div className="mb-4 rounded-md bg-red-500/10 px-3 py-2 text-[13px] text-red-600 dark:text-red-400">{deleteError}</div>
              )}
              This permanently removes <strong>{record.display || 'this record'}</strong>. This cannot be undone.
            </div>
            <div className="flex gap-2 border-t px-6 py-4">
              <button
                onClick={() => { setConfirmingDelete(false); setDeleteError(''); }}
                className="flex-1 rounded-md border border-border bg-muted px-3 py-2.5 text-sm font-medium hover:bg-accent"
              >
                Cancel
              </button>
              <button
                id="record-delete-confirm-btn"
                onClick={handleDelete}
                disabled={deleting}
                className="flex-1 rounded-md bg-red-600 px-3 py-2.5 text-sm font-semibold text-white hover:bg-red-700 disabled:opacity-60"
              >
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

