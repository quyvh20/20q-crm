import { useState, useEffect, useCallback } from 'react';
import {
  listRecordLinks,
  addRecordLink,
  removeRecordLink,
  listRecordTags,
  addRecordTag,
  removeRecordTag,
  listRegistryObjects,
  listObjectRecordsUnified,
  getTags,
  type RecordLink,
  type Tag,
  type ObjectSummary,
  type UniformRecord,
} from '../../lib/api';

interface RecordRelationsProps {
  slug: string;
  recordId: string;
}

// RecordRelations renders the universal relationships + tags for ANY record (P4),
// from the same three endpoints for every object. Tags are uniform across system
// and custom objects (the backend hides the contact_tags vs object_links split);
// relationships connect a record to any other object's record.
export default function RecordRelations({ slug, recordId }: RecordRelationsProps) {
  const [links, setLinks] = useState<RecordLink[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [allTags, setAllTags] = useState<Tag[]>([]);
  const [objects, setObjects] = useState<ObjectSummary[]>([]);
  const [error, setError] = useState('');
  const [showLinkForm, setShowLinkForm] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [l, t] = await Promise.all([listRecordLinks(slug, recordId), listRecordTags(slug, recordId)]);
      setLinks(l);
      setTags(t);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load relationships');
    }
  }, [slug, recordId]);

  useEffect(() => {
    refresh();
    getTags().then(setAllTags).catch(() => {});
    listRegistryObjects().then(setObjects).catch(() => {});
  }, [refresh]);

  const handleRemoveLink = async (linkId: string) => {
    setError('');
    try {
      await removeRecordLink(linkId);
      setLinks((prev) => prev.filter((l) => l.id !== linkId));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to remove link');
    }
  };

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

  // Tags not yet applied, offered in the picker.
  const appliedTagIds = new Set(tags.map((t) => t.id));
  const availableTags = allTags.filter((t) => !appliedTagIds.has(t.id));

  return (
    <div style={{ borderTop: '1px solid #e2e8f0', paddingTop: 16, marginTop: 8 }}>
      {error && (
        <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>
      )}

      {/* Tags */}
      <div style={{ marginBottom: 20 }}>
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

      {/* Relationships */}
      <div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <div style={{ fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase' }}>Related records</div>
          <button
            onClick={() => setShowLinkForm((v) => !v)}
            style={{ background: 'none', border: 'none', color: '#3b82f6', cursor: 'pointer', fontSize: 13, fontWeight: 500, padding: 0 }}
          >{showLinkForm ? 'Cancel' : '+ Link record'}</button>
        </div>

        {showLinkForm && (
          <AddLinkForm
            slug={slug}
            recordId={recordId}
            objects={objects}
            onAdded={() => { setShowLinkForm(false); refresh(); }}
            onError={setError}
          />
        )}

        {links.length === 0 && !showLinkForm && (
          <span style={{ color: '#94a3b8', fontSize: 13 }}>No related records</span>
        )}
        {links.map((l) => (
          <div key={l.id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid #f1f5f9' }}>
            <div style={{ fontSize: 13 }}>
              <span style={{ color: '#64748b' }}>{l.relation_key}</span>{' · '}
              <span style={{ fontWeight: 500 }}>{l.to_display || l.to_id}</span>
              <span style={{ color: '#94a3b8', fontSize: 11 }}> ({l.to_slug})</span>
            </div>
            <button
              onClick={() => handleRemoveLink(l.id)}
              aria-label="Remove link"
              style={{ background: 'none', border: 'none', color: '#dc2626', cursor: 'pointer', fontSize: 13 }}
            >Unlink</button>
          </div>
        ))}
      </div>
    </div>
  );
}

interface AddLinkFormProps {
  slug: string;
  recordId: string;
  objects: ObjectSummary[];
  onAdded: () => void;
  onError: (msg: string) => void;
}

// AddLinkForm: pick a target object, pick one of its records, name the relation.
// The record picker reuses the same uniform list endpoint every object uses.
function AddLinkForm({ slug, recordId, objects, onAdded, onError }: AddLinkFormProps) {
  const [toSlug, setToSlug] = useState('');
  const [toId, setToId] = useState('');
  const [relationKey, setRelationKey] = useState('related');
  const [candidates, setCandidates] = useState<UniformRecord[]>([]);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setToId('');
    setCandidates([]);
    if (!toSlug) return;
    listObjectRecordsUnified(toSlug, { limit: 50 })
      .then((page) => setCandidates(page.records))
      .catch(() => setCandidates([]));
  }, [toSlug]);

  const submit = async () => {
    if (!toSlug || !toId || !relationKey.trim()) return;
    setSaving(true);
    onError('');
    try {
      await addRecordLink(slug, recordId, { relation_key: relationKey.trim(), to_slug: toSlug, to_id: toId });
      onAdded();
    } catch (e) {
      onError(e instanceof Error ? e.message : 'Failed to add link');
    } finally {
      setSaving(false);
    }
  };

  const inputStyle = { padding: '6px 8px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 13, width: '100%', boxSizing: 'border-box' as const };

  return (
    <div style={{ background: '#f8fafc', border: '1px solid #e2e8f0', borderRadius: 8, padding: 12, marginBottom: 12, display: 'flex', flexDirection: 'column', gap: 8 }}>
      <select value={toSlug} onChange={(e) => setToSlug(e.target.value)} aria-label="Target object" style={inputStyle}>
        <option value="">— Choose object —</option>
        {objects.map((o) => (
          <option key={o.slug} value={o.slug}>{o.icon} {o.label}</option>
        ))}
      </select>
      <select value={toId} onChange={(e) => setToId(e.target.value)} aria-label="Target record" disabled={!toSlug} style={inputStyle}>
        <option value="">{toSlug ? '— Choose record —' : 'Pick an object first'}</option>
        {candidates.map((r) => (
          <option key={r.id} value={r.id}>{r.display || 'Untitled'}</option>
        ))}
      </select>
      <input
        value={relationKey}
        onChange={(e) => setRelationKey(e.target.value)}
        placeholder="Relationship (e.g. account)"
        aria-label="Relationship name"
        style={inputStyle}
      />
      <button
        onClick={submit}
        disabled={saving || !toSlug || !toId || !relationKey.trim()}
        style={{ padding: '8px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: saving ? 'default' : 'pointer', fontWeight: 600, fontSize: 13, opacity: !toSlug || !toId ? 0.5 : 1 }}
      >{saving ? 'Linking…' : 'Link'}</button>
    </div>
  );
}
