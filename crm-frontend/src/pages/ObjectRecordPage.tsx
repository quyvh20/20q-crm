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
import { ArrowLeft, Search } from 'lucide-react';
import { ObjectDetailView, ObjectForm } from '../features/objects';
import { listPath } from '../features/objects/recordRoutes';
import ShareRecordModal from '../components/records/ShareRecordModal';
import AccessDeniedPanel from '../components/common/AccessDeniedPanel';
import Modal from '../components/common/Modal';
import { Badge, Button, EmptyState, Skeleton } from '@/components/ui';
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

  const closeDelete = () => {
    setConfirmingDelete(false);
    setDeleteError('');
  };

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
      <div className="mx-auto w-full max-w-6xl">
        <Skeleton className="mb-4 h-7 w-52" />
        <Skeleton className="h-80 w-full rounded-xl" />
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
      <div className="mx-auto w-full max-w-6xl">
        <Button variant="ghost" size="sm" onClick={() => navigate(slug ? listPath(slug) : '/')} className="mb-4 -ml-2 text-muted-foreground">
          <ArrowLeft aria-hidden /> Back
        </Button>
        {accessDenied ? (
          <AccessDeniedPanel message={`Your role can't view ${slug ?? 'these'} records — ask an admin for access.`} />
        ) : (
          <EmptyState icon={Search} title={error || 'Record not found.'} />
        )}
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-6xl">
      {/* Back link */}
      <Button variant="ghost" size="sm" onClick={backToList} className="mb-4 -ml-2 text-muted-foreground">
        <ArrowLeft aria-hidden /> Back to {schema.label_plural}
      </Button>

      {/* Header section */}
      <div className="mb-8 flex items-start justify-between gap-4 border-b border-border pb-8 pt-4">
        <div className="min-w-0">
          <div className="mb-1 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            {/* schema.icon is the object's user-chosen emoji (data). */}
            <span>{schema.icon} {schema.label}</span>
            {record.number && (
              <Badge variant="secondary" className="font-semibold tracking-wide">{record.number}</Badge>
            )}
          </div>
          <h1 className="break-words text-2xl font-semibold tracking-tight text-foreground">
            {record.display || 'Untitled'}
          </h1>
          <div className="mt-1.5 text-xs text-muted-foreground">
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
              <Button id="record-edit-btn" onClick={() => setEditing(true)}>
                Edit
              </Button>
            )}
            {/* Sharing a record is a 'manage' action — hidden for someone who
                merely holds view/edit on it. */}
            {(effectiveLevel === undefined || effectiveLevel === 'manage') && (
              <Button id="record-share-btn" variant="outline" onClick={() => setSharing(true)}>
                Share
              </Button>
            )}
            {slug && canAccess(slug, 'delete') && effectiveLevel !== 'view' && (
              <Button
                id="record-delete-btn"
                variant="outline"
                onClick={() => setConfirmingDelete(true)}
                className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
              >
                Delete
              </Button>
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

      {/* Delete confirmation — shared Radix modal (U7). hideClose because the two
          buttons ARE the exits, and every dismissal path clears the stale error
          (only Cancel used to, so Escape/outside-click would re-open showing it). */}
      <Modal
        open={confirmingDelete}
        onClose={closeDelete}
        title={`Delete ${schema.label}?`}
        size="md"
        padded={false}
        hideClose
        dismissable={!deleting}
      >
        <>
          <div className="px-6 py-5 text-sm">
            {deleteError && (
              <div className="mb-4 rounded-md bg-destructive/10 px-3 py-2 text-[13px] text-destructive">{deleteError}</div>
            )}
            This permanently removes <strong>{record.display || 'this record'}</strong>. This cannot be undone.
          </div>
          <div className="flex gap-2 border-t border-border px-6 py-4">
            <Button variant="outline" onClick={closeDelete} className="flex-1">
              Cancel
            </Button>
            <Button
              id="record-delete-confirm-btn"
              variant="destructive"
              onClick={handleDelete}
              disabled={deleting}
              className="flex-1"
            >
              {deleting ? 'Deleting…' : 'Delete'}
            </Button>
          </div>
        </>
      </Modal>
    </div>
  );
}

