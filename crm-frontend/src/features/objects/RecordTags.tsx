import { useState, useEffect, useCallback } from 'react';
import {
  listRecordTags,
  addRecordTag,
  removeRecordTag,
  getTags,
  type Tag,
} from '../../lib/api';

interface RecordTagsProps {
  slug: string;
  recordId: string;
  /** Pre-fetched tags for this record — skip initial fetch when provided. */
  prefetchedTags?: Tag[] | null;
  /** Pre-fetched tag palette — skip initial fetch when provided. */
  prefetchedAllTags?: Tag[] | null;
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

  const refresh = useCallback(async () => {
    try {
      setTags(await listRecordTags(slug, recordId));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load tags');
    }
  }, [slug, recordId]);

  useEffect(() => {
    // If the parent already fetched both, skip the initial requests.
    if (prefetchedTags != null && prefetchedAllTags != null) return;
    if (prefetchedTags == null) refresh();
    if (prefetchedAllTags == null) getTags().then(setAllTags).catch(() => {});
  }, [refresh, prefetchedTags, prefetchedAllTags]);

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
    <div style={{ borderTop: '1px solid #e2e8f0', paddingTop: 16, marginTop: 8 }}>
      {error && (
        <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>
      )}
      <div style={{ fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase', marginBottom: 8 }}>Tags</div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, alignItems: 'center' }}>
        {tags.length === 0 && <span style={{ color: '#94a3b8', fontSize: 13 }}>No tags</span>}
        {tags.map((t) => (
          <span key={t.id} style={{ display: 'inline-flex', alignItems: 'center', gap: 4, background: t.color || '#e2e8f0', color: '#fff', borderRadius: 12, padding: '2px 10px', fontSize: 12, fontWeight: 500 }}>
            {t.name}
            <button
              onClick={() => handleRemoveTag(t.id)}
              aria-label={`Remove tag ${t.name}`}
              style={{ background: 'none', border: 'none', color: '#fff', cursor: 'pointer', fontSize: 14, lineHeight: 1, padding: 0 }}
            >×</button>
          </span>
        ))}
        {availableTags.length > 0 && (
          <select
            value=""
            onChange={(e) => e.target.value && handleAddTag(e.target.value)}
            aria-label="Add tag"
            style={{ border: '1px dashed #cbd5e1', borderRadius: 12, padding: '2px 8px', fontSize: 12, color: '#64748b', cursor: 'pointer', background: '#fff' }}
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
