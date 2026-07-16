import { useState, useEffect, useCallback } from 'react';
import { X } from 'lucide-react';
import {
  listRecordTags,
  addRecordTag,
  removeRecordTag,
  getTags,
  type Tag,
} from '../../lib/api';
import { usePermissions } from '../../lib/auth';

interface RecordTagsProps {
  slug: string;
  recordId: string;
  /**
   * Parent-owned tags for this record: an array hydrates the panel (possibly
   * after mount), null means the parent's request is still in flight. Omit
   * entirely (undefined) to have the component fetch for itself.
   */
  prefetchedTags?: Tag[] | null;
  /** Parent-owned tag palette — same null/undefined semantics. */
  prefetchedAllTags?: Tag[] | null;
}

// Tag add/remove are record writes: gate them on the caller's OLS edit bit so
// edit-denied viewers aren't offered controls that only 403 (U3). usePermissions
// throws outside an AuthProvider (ObjectDetailView mounts bare in unit tests) —
// fall open there, matching canAccess while the OLS map is unknown; the server
// enforces the write regardless.
function useCanEditRecord(slug: string): boolean {
  try {
    return usePermissions().canAccess(slug, 'edit');
  } catch {
    return true;
  }
}

// RecordTags renders a record's tags for ANY object, from the uniform tag API
// (the backend hides the contact_tags vs object_links split). It is the tag half
// of the former RecordRelations panel — the free-text "link any record" half was
// retired in favor of schema-driven related lists (RelatedLists), so relationships
// now come from typed relation fields rather than a parallel, manual store.
export default function RecordTags({ slug, recordId, prefetchedTags, prefetchedAllTags }: RecordTagsProps) {
  const [tags, setTags] = useState<Tag[]>(prefetchedTags ?? []);
  const [allTags, setAllTags] = useState<Tag[]>(prefetchedAllTags ?? []);
  const [error, setError] = useState('');
  const canEdit = useCanEditRecord(slug);

  const refresh = useCallback(async () => {
    try {
      setTags(await listRecordTags(slug, recordId));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load tags');
    }
  }, [slug, recordId]);

  // Hydrate from the parent's prefetch, which may land after mount (the record
  // page no longer blocks first paint on the tag requests). Local add/remove
  // edits are safe: the prop's identity only changes when the parent reloads.
  useEffect(() => {
    if (prefetchedTags != null) setTags(prefetchedTags);
  }, [prefetchedTags]);
  useEffect(() => {
    if (prefetchedAllTags != null) setAllTags(prefetchedAllTags);
  }, [prefetchedAllTags]);

  // Self-fetch only what no parent will ever supply (undefined, not null —
  // null means the parent's request is still in flight).
  const selfFetchTags = prefetchedTags === undefined;
  const selfFetchAllTags = prefetchedAllTags === undefined;
  useEffect(() => {
    if (selfFetchTags) refresh();
    if (selfFetchAllTags) getTags().then(setAllTags).catch(() => {});
  }, [refresh, selfFetchTags, selfFetchAllTags]);

  const handleAddTag = async (tagId: string) => {
    setError('');
    try {
      await addRecordTag(slug, recordId, tagId);
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to add tag');
    }
  };

  const handleRemoveTag = async (tagId: string) => {
    setError('');
    try {
      await removeRecordTag(slug, recordId, tagId);
      setTags((prev) => prev.filter((t) => t.id !== tagId));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to remove tag');
    }
  };

  const appliedTagIds = new Set(tags.map((t) => t.id));
  const availableTags = allTags.filter((t) => !appliedTagIds.has(t.id));

  return (
    <div className="mt-2 border-t border-border pt-4">
      {error && (
        <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
      )}
      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Tags</div>
      <div className="flex flex-wrap items-center gap-1.5">
        {tags.length === 0 && <span className="text-sm text-muted-foreground">No tags</span>}
        {tags.map((t) => (
          <span
            key={t.id}
            className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-medium ${
              t.color ? '' : 'border-border bg-muted text-foreground'
            }`}
            // The chip's palette is the tag's own user-picked color — data, not chrome.
            style={t.color ? { borderColor: t.color, color: t.color, background: `${t.color}1a` } : undefined}
          >
            {t.name}
            {canEdit && (
              <button
                onClick={() => handleRemoveTag(t.id)}
                aria-label={`Remove tag ${t.name}`}
                className="rounded-full text-current transition-opacity hover:opacity-70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <X aria-hidden className="h-3 w-3" />
              </button>
            )}
          </span>
        ))}
        {canEdit && availableTags.length > 0 && (
          <select
            value=""
            onChange={(e) => e.target.value && handleAddTag(e.target.value)}
            aria-label="Add tag"
            className="cursor-pointer rounded-full border border-dashed border-input bg-background px-2 py-0.5 text-xs text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <option value="">+ Add tag</option>
            {availableTags.map((t) => (
              <option key={t.id} value={t.id}>{t.name}</option>
            ))}
          </select>
        )}
      </div>
    </div>
  );
}
