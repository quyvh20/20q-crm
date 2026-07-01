import { useState, useEffect, useCallback } from 'react';
import {
  listRegistryObjects, getObjectSchema, getObjectDef,
  createObjectDef, updateObjectDef, deleteObjectDef,
  createFieldDef, updateFieldDef, deleteFieldDef, setObjectNumberPrefix,
  listObjectLayouts, createObjectLayout, updateObjectLayout, deleteObjectLayout, setLayoutRoles,
  getPermissionGrid,
  type ObjectSummary, type CustomFieldDef, type FieldType, type ObjectFieldDescriptor,
  type ObjectLayout, type LayoutSection, type LayoutField, type PermRoleInfo,
} from '../../lib/api';

// ObjectsManager is the single admin surface for every object's schema (P7 — it
// replaces the separate CustomFieldManager + ObjectDefManager now that object_defs/
// object_fields is one store). It lists all objects from the registry; custom
// objects are created/edited/deleted via the custom-object API, while system
// objects' native fields are read-only and their admin-defined custom fields are
// managed through the settings field API. Both write to object_fields.
//
// P8 adds a Layouts tab to every object editor (except deals, which have a fixed
// Kanban-centric layout).

const ICONS = ['📦', '🏗️', '🚗', '📋', '🎯', '💼', '🏠', '📊', '🔧', '📝', '🎪', '🧩', '📁', '🗂️', '⚙️', '🛒'];
const FIELD_TYPES: FieldType[] = ['text', 'number', 'date', 'select', 'boolean', 'url', 'relation', 'mirror'];
const typeLabel = (t: string) => ({ text: 'Aa Text', number: '# Number', date: '📅 Date', select: '▼ Select', boolean: '✓ Yes/No', url: '🔗 URL', relation: '↗ Relation', mirror: '⇄ Mirror' }[t] || t);
const autoSlug = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '').slice(0, 50);

const inputStyle = { width: '100%', padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, boxSizing: 'border-box' as const };
const btn = (bg: string) => ({ padding: '8px 16px', background: bg, color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 });

type Mode = { kind: 'list' } | { kind: 'new' } | { kind: 'edit'; slug: string; isSystem: boolean };

export default function ObjectsManager() {
  const [objects, setObjects] = useState<ObjectSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [mode, setMode] = useState<Mode>({ kind: 'list' });
  const [error, setError] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  const fetchObjects = useCallback(async () => {
    setLoading(true);
    try {
      setObjects(await listRegistryObjects());
    } catch {
      setObjects([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetchObjects(); }, [fetchObjects]);

  const handleDelete = async (slug: string) => {
    setError('');
    try {
      await deleteObjectDef(slug);
      setConfirmDelete(null);
      fetchObjects();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed');
    }
  };

  if (loading) return <p style={{ color: '#94a3b8', padding: 20 }}>Loading...</p>;

  if (mode.kind === 'new') {
    return <CustomObjectForm onDone={() => { setMode({ kind: 'list' }); fetchObjects(); }} onCancel={() => setMode({ kind: 'list' })} />;
  }
  if (mode.kind === 'edit') {
    return mode.isSystem
      ? <SystemFieldsEditor slug={mode.slug} onBack={() => { setMode({ kind: 'list' }); fetchObjects(); }} />
      : <CustomObjectForm editSlug={mode.slug} onDone={() => { setMode({ kind: 'list' }); fetchObjects(); }} onCancel={() => setMode({ kind: 'list' })} />;
  }

  return (
    <div>
      <p style={{ color: '#64748b', fontSize: 13, marginTop: 0 }}>
        Every object — built-in or custom — and its fields, in one place.
      </p>
      {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

      <table style={{ width: '100%', borderCollapse: 'collapse', marginBottom: 16 }}>
        <thead>
          <tr style={{ borderBottom: '1px solid #e2e8f0', textAlign: 'left' }}>
            {['', 'Label', 'Slug', 'Type', 'Fields', ''].map((h, i) => (
              <th key={i} style={{ padding: '8px 12px', fontSize: 13, color: '#64748b', textAlign: i === 5 ? 'right' : 'left' }}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {objects.map(o => (
            <tr key={o.slug} style={{ borderBottom: '1px solid #f1f5f9' }}>
              <td style={{ padding: '10px 12px', fontSize: 20 }}>{o.icon}</td>
              <td style={{ padding: '10px 12px', fontWeight: 500 }}>{o.label} <span style={{ color: '#94a3b8', fontWeight: 400 }}>/ {o.label_plural}</span></td>
              <td style={{ padding: '10px 12px' }}><code style={{ background: '#f1f5f9', padding: '2px 6px', borderRadius: 4, fontSize: 13 }}>{o.slug}</code></td>
              <td style={{ padding: '10px 12px' }}>
                <span style={{ fontSize: 12, padding: '2px 8px', borderRadius: 12, background: o.is_system ? '#eff6ff' : '#f0fdf4', color: o.is_system ? '#3b82f6' : '#16a34a' }}>
                  {o.is_system ? 'Built-in' : 'Custom'}
                </span>
              </td>
              <td style={{ padding: '10px 12px', color: '#64748b' }}>{o.field_count}</td>
              <td style={{ padding: '10px 12px', textAlign: 'right' }}>
                {confirmDelete === o.slug ? (
                  <>
                    <button onClick={() => handleDelete(o.slug)} style={{ ...btn('#ef4444'), padding: '4px 12px', marginRight: 4, fontSize: 13 }}>Confirm</button>
                    <button onClick={() => setConfirmDelete(null)} style={{ background: '#e2e8f0', border: 'none', padding: '4px 12px', borderRadius: 4, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
                  </>
                ) : (
                  <>
                    <button onClick={() => setMode({ kind: 'edit', slug: o.slug, isSystem: o.is_system })} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, padding: 4 }} title="Edit fields">✏️</button>
                    {!o.is_system && (
                      <button onClick={() => setConfirmDelete(o.slug)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, padding: 4 }} title="Delete object">🗑️</button>
                    )}
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <button onClick={() => setMode({ kind: 'new' })} style={{ ...btn('#3b82f6'), display: 'inline-flex', alignItems: 'center', gap: 6 }}>
        + New Object
      </button>
    </div>
  );
}

// ============================================================
// Shared tab bar
// ============================================================

function TabBar({ active, onSelect, tabs }: { active: string; onSelect: (t: string) => void; tabs: string[] }) {
  return (
    <div style={{ display: 'flex', gap: 2, borderBottom: '1px solid #e2e8f0', marginBottom: 20 }}>
      {tabs.map(t => (
        <button key={t} onClick={() => onSelect(t)} style={{
          padding: '8px 16px', border: 'none', borderBottom: active === t ? '2px solid #3b82f6' : '2px solid transparent',
          background: 'none', cursor: 'pointer', fontWeight: active === t ? 600 : 400,
          color: active === t ? '#3b82f6' : '#64748b', fontSize: 13,
        }}>{t}</button>
      ))}
    </div>
  );
}

// ============================================================
// Record-number prefix editor (admin sets the DEAL-0001 style prefix per object)
// ============================================================

function NumberPrefixEditor({ slug }: { slug: string }) {
  const [prefix, setPrefix] = useState('');
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    let cancelled = false;
    getObjectSchema(slug)
      .then(s => { if (!cancelled) setPrefix(s.number_prefix || ''); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [slug]);

  const save = async () => {
    setSaving(true); setErr(''); setSaved(false);
    try { await setObjectNumberPrefix(slug, prefix.trim()); setSaved(true); }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to save prefix'); }
    finally { setSaving(false); }
  };

  const sample = `${(prefix.trim() || slug).toUpperCase()}-0001`;
  return (
    <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, padding: 12, background: '#fafbfc', marginBottom: 12 }}>
      <label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}>Record number prefix</label>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        <input
          value={prefix}
          onChange={e => { setPrefix(e.target.value.toUpperCase().slice(0, 16)); setSaved(false); }}
          placeholder={slug.toUpperCase()}
          style={{ ...inputStyle, width: 160, padding: '6px 8px', fontSize: 13 }}
        />
        <span style={{ fontSize: 12, color: '#64748b' }}>e.g. <code>{sample}</code></span>
        <button onClick={save} disabled={saving} style={{ ...btn(saving ? '#94a3b8' : '#3b82f6'), padding: '6px 12px', fontSize: 13 }}>{saving ? 'Saving…' : 'Save'}</button>
        {saved && <span style={{ color: '#16a34a', fontSize: 12 }}>Saved ✓</span>}
      </div>
      {err && <p style={{ color: '#dc2626', fontSize: 12, margin: '4px 0 0' }}>{err}</p>}
      <p style={{ fontSize: 11, color: '#94a3b8', margin: '4px 0 0' }}>A friendly identifier shown on each record instead of its database id. Blank uses the object name.</p>
    </div>
  );
}

// ============================================================
// Field builder (shared add/edit form for one field)
// ============================================================

interface FieldDraft {
  key: string;
  label: string;
  type: string;
  options: string[];
  required: boolean;
  target_slug?: string;
  via_field?: string;
  source_field?: string;
}
const emptyDraft: FieldDraft = { key: '', label: '', type: 'text', options: [], required: false, target_slug: '', via_field: '', source_field: '' };

function FieldBuilder({ draft, setDraft, onAdd, editing, currentSlug }: {
  draft: FieldDraft; setDraft: (d: FieldDraft) => void; onAdd: () => void; editing: boolean;
  /** The object being edited, excluded from relation targets to avoid an obvious self-loop. */
  currentSlug?: string;
}) {
  const [optInput, setOptInput] = useState('');
  const [objects, setObjects] = useState<ObjectSummary[]>([]);
  // This object's relation fields (the "via" choices for a mirror) and the fields
  // of the currently-chosen via relation's target (the "source" choices).
  const [thisRelations, setThisRelations] = useState<ObjectFieldDescriptor[]>([]);
  const [sourceFields, setSourceFields] = useState<ObjectFieldDescriptor[]>([]);

  // Relation targets are every registered object; loaded lazily so the field
  // builder only pays for it when relations are in play.
  useEffect(() => {
    let cancelled = false;
    listRegistryObjects().then(o => { if (!cancelled) setObjects(o); }).catch(() => {});
    return () => { cancelled = true; };
  }, []);

  // A mirror follows one of this object's relation fields, so load them.
  useEffect(() => {
    if (!currentSlug) { setThisRelations([]); return; }
    let cancelled = false;
    getObjectSchema(currentSlug)
      .then(s => { if (!cancelled) setThisRelations(s.fields.filter(f => f.type === 'relation' && f.target_slug)); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [currentSlug]);

  // Once a via relation is chosen, load its target object's fields to mirror from.
  const viaTarget = thisRelations.find(f => f.key === draft.via_field)?.target_slug;
  useEffect(() => {
    if (draft.type !== 'mirror' || !viaTarget) { setSourceFields([]); return; }
    let cancelled = false;
    getObjectSchema(viaTarget)
      .then(s => { if (!cancelled) setSourceFields(s.fields); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [draft.type, viaTarget]);

  const isRelation = draft.type === 'relation';
  const isMirror = draft.type === 'mirror';
  // A relation needs a target; a mirror needs both a via relation and a source field.
  const canSubmit = draft.label.trim()
    && (!isRelation || !!draft.target_slug)
    && (!isMirror || (!!draft.via_field && !!draft.source_field));

  return (
    <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, padding: 12, background: '#fafbfc' }}>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr auto auto', gap: 8, alignItems: 'end' }}>
        <div>
          <label style={{ fontSize: 12, color: '#64748b' }}>Field Label</label>
          <input value={draft.label} onChange={e => setDraft({ ...draft, label: e.target.value, key: editing ? draft.key : autoSlug(e.target.value) })} placeholder="e.g. Priority" style={{ ...inputStyle, padding: '6px 8px', fontSize: 13 }} />
        </div>
        <div>
          <label style={{ fontSize: 12, color: '#64748b' }}>Type</label>
          <select value={draft.type} onChange={e => setDraft({ ...draft, type: e.target.value })} style={{ ...inputStyle, padding: '6px 8px', fontSize: 13 }}>
            {FIELD_TYPES.map(t => <option key={t} value={t}>{typeLabel(t)}</option>)}
          </select>
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, color: '#64748b', cursor: 'pointer' }}>
          <input type="checkbox" checked={draft.required} onChange={e => setDraft({ ...draft, required: e.target.checked })} /> Req
        </label>
        <button onClick={onAdd} disabled={!canSubmit} style={{ ...btn(canSubmit ? '#3b82f6' : '#94a3b8'), padding: '6px 14px', fontSize: 13 }}>{editing ? 'Save' : '+ Add'}</button>
      </div>
      {isRelation && (
        <div style={{ marginTop: 8 }}>
          <label style={{ fontSize: 12, color: '#64748b' }}>Related object</label>
          <select
            value={draft.target_slug || ''}
            onChange={e => setDraft({ ...draft, target_slug: e.target.value })}
            style={{ ...inputStyle, padding: '6px 8px', fontSize: 13 }}
          >
            <option value="">— Choose an object —</option>
            {objects.filter(o => o.slug !== currentSlug).map(o => (
              <option key={o.slug} value={o.slug}>{o.icon} {o.label}</option>
            ))}
          </select>
          <p style={{ fontSize: 11, color: '#94a3b8', margin: '4px 0 0' }}>
            This field links each record to one {draft.target_slug ? objects.find(o => o.slug === draft.target_slug)?.label || 'record' : 'record'}; the related object will show these in a related list.
          </p>
        </div>
      )}
      {isMirror && (
        <div style={{ marginTop: 8, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
          <div>
            <label style={{ fontSize: 12, color: '#64748b' }}>Via relation</label>
            <select
              value={draft.via_field || ''}
              onChange={e => setDraft({ ...draft, via_field: e.target.value, source_field: '' })}
              style={{ ...inputStyle, padding: '6px 8px', fontSize: 13 }}
            >
              <option value="">— Choose a relation —</option>
              {thisRelations.map(f => <option key={f.key} value={f.key}>{f.label}</option>)}
            </select>
          </div>
          <div>
            <label style={{ fontSize: 12, color: '#64748b' }}>Show which field</label>
            <select
              value={draft.source_field || ''}
              onChange={e => setDraft({ ...draft, source_field: e.target.value })}
              disabled={!draft.via_field}
              style={{ ...inputStyle, padding: '6px 8px', fontSize: 13 }}
            >
              <option value="">{draft.via_field ? '— Choose a field —' : 'Pick a relation first'}</option>
              {sourceFields.map(f => <option key={f.key} value={f.key}>{f.label}</option>)}
            </select>
          </div>
          <p style={{ gridColumn: '1 / -1', fontSize: 11, color: '#94a3b8', margin: 0 }}>
            {thisRelations.length === 0
              ? 'Add a relation field to this object first — a mirror displays a field from a linked record.'
              : `Read-only: shows the ${sourceFields.find(f => f.key === draft.source_field)?.label || 'chosen field'} of the linked ${objects.find(o => o.slug === viaTarget)?.label || 'record'}, kept in sync.`}
          </p>
        </div>
      )}
      {draft.type === 'select' && (
        <div style={{ marginTop: 8 }}>
          <label style={{ fontSize: 12, color: '#64748b' }}>Options (press Enter)</label>
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginBottom: 4 }}>
            {draft.options.map(opt => (
              <span key={opt} style={{ background: '#eff6ff', color: '#3b82f6', padding: '2px 8px', borderRadius: 12, fontSize: 12, display: 'flex', alignItems: 'center', gap: 4 }}>
                {opt} <button onClick={() => setDraft({ ...draft, options: draft.options.filter(o => o !== opt) })} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#3b82f6', fontSize: 12, padding: 0 }}>✕</button>
              </span>
            ))}
          </div>
          <input value={optInput} onChange={e => setOptInput(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter' && optInput.trim()) { e.preventDefault(); setDraft({ ...draft, options: [...draft.options, optInput.trim()] }); setOptInput(''); } }}
            placeholder="Type and press Enter" style={{ ...inputStyle, padding: '6px 8px', fontSize: 13 }} />
        </div>
      )}
    </div>
  );
}

// ============================================================
// Custom object create/edit (label/icon/searchable + fields array)
// ============================================================

function CustomObjectForm({ editSlug, onDone, onCancel }: { editSlug?: string; onDone: () => void; onCancel: () => void }) {
  const [tab, setTab] = useState<'fields' | 'layouts'>('fields');
  const [label, setLabel] = useState('');
  const [slug, setSlug] = useState('');
  const [labelPlural, setLabelPlural] = useState('');
  const [icon, setIcon] = useState('📦');
  const [searchable, setSearchable] = useState(false);
  const [fields, setFields] = useState<CustomFieldDef[]>([]);
  const [draft, setDraft] = useState<FieldDraft>({ ...emptyDraft });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(!!editSlug);

  useEffect(() => {
    if (!editSlug) return;
    getObjectDef(editSlug).then(def => {
      setLabel(def.label); setSlug(def.slug); setLabelPlural(def.label_plural);
      setIcon(def.icon); setSearchable(def.searchable ?? false); setFields(def.fields || []);
      setLoading(false);
    }).catch(() => { setError('Failed to load object'); setLoading(false); });
  }, [editSlug]);

  const onLabel = (v: string) => { setLabel(v); if (!editSlug) { setSlug(autoSlug(v)); setLabelPlural(v ? v + 's' : ''); } };

  const addField = () => {
    if (!draft.label.trim()) return;
    const key = draft.key || autoSlug(draft.label);
    if (fields.some(f => f.key === key)) { setError(`Duplicate field key: ${key}`); return; }
    if (draft.type === 'relation' && !draft.target_slug) { setError('Choose a related object for the relation field'); return; }
    if (draft.type === 'mirror' && (!draft.via_field || !draft.source_field)) { setError('Choose a relation and a field for the mirror'); return; }
    const f: CustomFieldDef = { key, label: draft.label.trim(), type: draft.type as FieldType, required: draft.required, position: fields.length };
    if (draft.type === 'select') f.options = [...draft.options];
    if (draft.type === 'relation') f.target_slug = draft.target_slug;
    if (draft.type === 'mirror') { f.via_field = draft.via_field; f.source_field = draft.source_field; }
    setFields([...fields, f]);
    setDraft({ ...emptyDraft });
    setError('');
  };

  const save = async () => {
    setError('');
    try {
      if (editSlug) await updateObjectDef(editSlug, { label, label_plural: labelPlural, icon, fields, searchable });
      else await createObjectDef({ slug, label, label_plural: labelPlural, icon, fields, searchable });
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    }
  };

  if (loading) return <p style={{ color: '#94a3b8', padding: 20 }}>Loading...</p>;

  return (
    <div>
      <h4 style={{ margin: '0 0 16px' }}>{editSlug ? `Edit ${label}` : 'New Custom Object'}</h4>
      {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

      {/* Layout tab is only available once the object exists */}
      {editSlug && <TabBar active={tab} onSelect={t => setTab(t as 'fields' | 'layouts')} tabs={['fields', 'layouts']} />}

      {tab === 'fields' ? (
        <>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12, marginBottom: 16 }}>
            <div><label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}>Label *</label><input value={label} onChange={e => onLabel(e.target.value)} placeholder="e.g. Project" style={inputStyle} /></div>
            <div><label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}>Slug</label><input value={slug} onChange={e => setSlug(e.target.value)} disabled={!!editSlug} style={{ ...inputStyle, background: editSlug ? '#f1f5f9' : '#fff' }} /></div>
            <div><label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}>Plural Label</label><input value={labelPlural} onChange={e => setLabelPlural(e.target.value)} placeholder="e.g. Projects" style={inputStyle} /></div>
          </div>

          <div style={{ marginBottom: 16 }}>
            <label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}>Icon</label>
            <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
              {ICONS.map(ic => (
                <button key={ic} onClick={() => setIcon(ic)} style={{ fontSize: 20, padding: '4px 8px', border: icon === ic ? '2px solid #3b82f6' : '1px solid #e2e8f0', borderRadius: 6, background: icon === ic ? '#eff6ff' : '#fff', cursor: 'pointer' }}>{ic}</button>
              ))}
            </div>
          </div>

          <div style={{ marginBottom: 16 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 13, fontWeight: 500, cursor: 'pointer' }}>
              <input type="checkbox" checked={searchable} onChange={e => setSearchable(e.target.checked)} /> 🔍 Searchable
            </label>
            <p style={{ fontSize: 12, color: '#94a3b8', margin: '4px 0 0 24px' }}>Index records for semantic + full-text global search and AI.</p>
          </div>

          {/* Record-number prefix is editable once the object exists (has a slug). */}
          {editSlug && <NumberPrefixEditor slug={editSlug} />}

          <div style={{ marginBottom: 16 }}>
            <label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 8 }}>Fields ({fields.length})</label>
            {fields.length > 0 && (
              <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, overflow: 'hidden', marginBottom: 8 }}>
                {fields.map((f, i) => (
                  <div key={f.key} style={{ display: 'flex', alignItems: 'center', padding: '6px 10px', borderBottom: i < fields.length - 1 ? '1px solid #f1f5f9' : 'none' }}>
                    <span style={{ flex: 1, fontWeight: 500, fontSize: 13 }}>{f.label}</span>
                    <code style={{ fontSize: 12, color: '#64748b', marginRight: 8 }}>{f.key}</code>
                    <span style={{ fontSize: 12, color: '#3b82f6', marginRight: 8 }}>{typeLabel(f.type)}</span>
                    {f.required && <span style={{ fontSize: 11, color: '#ef4444', marginRight: 8 }}>Required</span>}
                    <button onClick={() => setFields(fields.filter(x => x.key !== f.key))} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#ef4444', fontSize: 14 }}>✕</button>
                  </div>
                ))}
              </div>
            )}
            <FieldBuilder draft={draft} setDraft={setDraft} onAdd={addField} editing={false} currentSlug={slug} />
          </div>

          <div style={{ display: 'flex', gap: 8 }}>
            <button onClick={save} style={btn('#3b82f6')}>{editSlug ? 'Update Object' : 'Create Object'}</button>
            <button onClick={onCancel} style={{ padding: '8px 16px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>Cancel</button>
          </div>
        </>
      ) : (
        // Layouts tab — only reachable when editSlug is set
        <LayoutsEditor slug={editSlug!} fieldKeys={fields.map(f => ({ key: f.key, label: f.label }))} />
      )}
    </div>
  );
}

// ============================================================
// System object fields editor (native read-only; custom fields via settings API)
// ============================================================

interface SchemaFieldRow { key: string; label: string; type: string; is_system: boolean; required: boolean; options?: string[]; target_slug?: string; via_field?: string; source_field?: string }

function SystemFieldsEditor({ slug, onBack }: { slug: string; onBack: () => void }) {
  const [tab, setTab] = useState<'fields' | 'layouts'>('fields');
  const [label, setLabel] = useState(slug);
  const [icon, setIcon] = useState('📦');
  const [rows, setRows] = useState<SchemaFieldRow[]>([]);
  const [draft, setDraft] = useState<FieldDraft>({ ...emptyDraft });
  const [editingKey, setEditingKey] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const s = await getObjectSchema(slug);
      setLabel(s.label); setIcon(s.icon);
      setRows(s.fields.map(f => ({ key: f.key, label: f.label, type: f.type, is_system: f.is_system, required: f.required, options: f.options, target_slug: f.target_slug, via_field: f.via_field, source_field: f.source_field })));
    } catch {
      setError('Failed to load fields');
    } finally {
      setLoading(false);
    }
  }, [slug]);
  useEffect(() => { load(); }, [load]);

  const saveField = async () => {
    if (!draft.label.trim()) return;
    setError('');
    const key = draft.key || autoSlug(draft.label);
    if (draft.type === 'relation' && !draft.target_slug) { setError('Choose a related object for the relation field'); return; }
    if (draft.type === 'mirror' && (!draft.via_field || !draft.source_field)) { setError('Choose a relation and a field for the mirror'); return; }
    const payload = {
      label: draft.label.trim(), type: draft.type, required: draft.required,
      options: draft.type === 'select' ? draft.options : undefined,
      target_slug: draft.type === 'relation' ? draft.target_slug : undefined,
      via_field: draft.type === 'mirror' ? draft.via_field : undefined,
      source_field: draft.type === 'mirror' ? draft.source_field : undefined,
    };
    try {
      if (editingKey) {
        await updateFieldDef(editingKey, payload);
      } else {
        if (rows.some(r => r.key === key)) { setError(`Duplicate field key: ${key}`); return; }
        await createFieldDef({ key, entity_type: slug, ...payload });
      }
      setDraft({ ...emptyDraft });
      setEditingKey(null);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    }
  };

  const editField = (r: SchemaFieldRow) => {
    setEditingKey(r.key);
    setDraft({ key: r.key, label: r.label, type: r.type, options: r.options || [], required: r.required, target_slug: r.target_slug || '', via_field: r.via_field || '', source_field: r.source_field || '' });
  };

  const removeField = async (key: string) => {
    setError('');
    try { await deleteFieldDef(key); load(); } catch (e) { setError(e instanceof Error ? e.message : 'Delete failed'); }
  };

  if (loading) return <p style={{ color: '#94a3b8', padding: 20 }}>Loading...</p>;

  // Deals use a Kanban layout — no point offering a custom section builder for them.
  const showLayouts = slug !== 'deal';

  return (
    <div>
      <h4 style={{ margin: '0 0 4px' }}>{icon} {label} <span style={{ fontSize: 12, color: '#3b82f6', fontWeight: 400 }}>· Built-in</span></h4>

      {showLayouts
        ? <TabBar active={tab} onSelect={t => setTab(t as 'fields' | 'layouts')} tabs={['fields', 'layouts']} />
        : <p style={{ color: '#64748b', fontSize: 13, marginTop: 0 }}>Built-in fields are fixed; add or edit your own custom fields below.</p>
      }

      {tab === 'fields' ? (
        <>
          {showLayouts || (
            <p style={{ color: '#64748b', fontSize: 13, marginTop: 0 }}>Built-in fields are fixed; add or edit your own custom fields below.</p>
          )}
          {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

          <NumberPrefixEditor slug={slug} />

          <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, overflow: 'hidden', marginBottom: 12 }}>
            {rows.map((r, i) => (
              <div key={r.key} style={{ display: 'flex', alignItems: 'center', padding: '8px 10px', borderBottom: i < rows.length - 1 ? '1px solid #f1f5f9' : 'none', background: r.is_system ? '#fafbfc' : '#fff' }}>
                <span style={{ flex: 1, fontWeight: 500, fontSize: 13 }}>{r.label}</span>
                <code style={{ fontSize: 12, color: '#64748b', marginRight: 8 }}>{r.key}</code>
                <span style={{ fontSize: 12, color: '#3b82f6', marginRight: 8 }}>{typeLabel(r.type)}</span>
                {r.is_system ? (
                  <span style={{ fontSize: 11, color: '#94a3b8' }}>Built-in</span>
                ) : (
                  <>
                    <button onClick={() => editField(r)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, padding: 2 }} title="Edit">✏️</button>
                    <button onClick={() => removeField(r.key)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#ef4444', fontSize: 14, padding: 2 }} title="Delete">✕</button>
                  </>
                )}
              </div>
            ))}
          </div>

          <label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 6 }}>{editingKey ? `Edit field "${editingKey}"` : 'Add a custom field'}</label>
          <FieldBuilder draft={draft} setDraft={setDraft} onAdd={saveField} editing={!!editingKey} currentSlug={slug} />

          <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
            <button onClick={onBack} style={{ padding: '8px 16px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>← Back to objects</button>
            {editingKey && <button onClick={() => { setEditingKey(null); setDraft({ ...emptyDraft }); }} style={{ padding: '8px 16px', background: '#fff', border: '1px solid #d1d5db', borderRadius: 6, cursor: 'pointer' }}>Cancel edit</button>}
          </div>
        </>
      ) : (
        <LayoutsEditor
          slug={slug}
          fieldKeys={rows.map(r => ({ key: r.key, label: r.label }))}
        />
      )}

      {tab === 'layouts' && (
        <div style={{ marginTop: 20 }}>
          <button onClick={onBack} style={{ padding: '8px 16px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>← Back to objects</button>
        </div>
      )}
    </div>
  );
}

// ============================================================
// P8 — LayoutsEditor (list + create/edit a single layout)
// ============================================================

interface FieldEntry { key: string; label: string }

function LayoutsEditor({ slug, fieldKeys }: { slug: string; fieldKeys: FieldEntry[] }) {
  const [layouts, setLayouts] = useState<ObjectLayout[]>([]);
  const [roles, setRoles] = useState<PermRoleInfo[]>([]);
  const [editing, setEditing] = useState<ObjectLayout | null>(null);
  const [creating, setCreating] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [ls, grid] = await Promise.all([listObjectLayouts(slug), getPermissionGrid()]);
      setLayouts(ls);
      // Filter out owner-bypass roles since layout assignment for them is meaningless.
      setRoles(grid.roles.filter(r => !r.is_owner));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load layouts');
    } finally {
      setLoading(false);
    }
  }, [slug]);

  useEffect(() => { load(); }, [load]);

  const handleDelete = async (id: string) => {
    try { await deleteObjectLayout(slug, id); load(); } catch (e) { setError(e instanceof Error ? e.message : 'Delete failed'); }
  };

  if (loading) return <p style={{ color: '#94a3b8' }}>Loading layouts…</p>;

  if (creating || editing) {
    return (
      <LayoutForm
        slug={slug}
        fieldKeys={fieldKeys}
        roles={roles}
        initial={editing ?? undefined}
        onSave={() => { setCreating(false); setEditing(null); load(); }}
        onCancel={() => { setCreating(false); setEditing(null); }}
      />
    );
  }

  const roleMap = Object.fromEntries(roles.map(r => [r.id, r.name]));

  return (
    <div>
      <p style={{ fontSize: 13, color: '#64748b', marginTop: 0 }}>
        Layouts control how fields are arranged on the detail page — by role. The effective layout
        is served via the schema API: role-assigned → default → flat field order.
      </p>
      {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

      {layouts.length === 0 ? (
        <div style={{ padding: '32px 0', textAlign: 'center', color: '#94a3b8', fontSize: 13 }}>
          No layouts yet. All roles see a flat list of fields ordered by position.
        </div>
      ) : (
        <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, overflow: 'hidden', marginBottom: 12 }}>
          {layouts.map((l, i) => (
            <div key={l.id} style={{ display: 'flex', alignItems: 'center', padding: '10px 12px', borderBottom: i < layouts.length - 1 ? '1px solid #f1f5f9' : 'none' }}>
              <div style={{ flex: 1 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontWeight: 500, fontSize: 13 }}>{l.name}</span>
                  {l.is_default && (
                    <span style={{ fontSize: 11, padding: '1px 7px', background: '#f0fdf4', color: '#16a34a', borderRadius: 10, fontWeight: 600 }}>default</span>
                  )}
                </div>
                {l.role_ids.length > 0 && (
                  <div style={{ marginTop: 3, display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                    {l.role_ids.map(rid => (
                      <span key={rid} style={{ fontSize: 11, padding: '1px 7px', background: '#eff6ff', color: '#3b82f6', borderRadius: 10 }}>
                        {roleMap[rid] ?? rid}
                      </span>
                    ))}
                  </div>
                )}
              </div>
              <span style={{ fontSize: 12, color: '#94a3b8', marginRight: 12 }}>{(l.layout ?? []).length} sections</span>
              <button onClick={() => setEditing(l)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, padding: 4 }} title="Edit">✏️</button>
              <button onClick={() => handleDelete(l.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#ef4444', fontSize: 14, padding: 4 }} title="Delete">🗑️</button>
            </div>
          ))}
        </div>
      )}

      <button onClick={() => setCreating(true)} style={{ ...btn('#3b82f6'), fontSize: 13 }}>+ New Layout</button>
    </div>
  );
}

// ============================================================
// LayoutForm — create or edit one named layout
// ============================================================

function LayoutForm({
  slug,
  fieldKeys,
  roles,
  initial,
  onSave,
  onCancel,
}: {
  slug: string;
  fieldKeys: FieldEntry[];
  roles: PermRoleInfo[];
  initial?: ObjectLayout;
  onSave: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? '');
  const [isDefault, setIsDefault] = useState(initial?.is_default ?? false);
  const [selectedRoles, setSelectedRoles] = useState<Set<string>>(new Set(initial?.role_ids ?? []));
  const [sections, setSections] = useState<LayoutSection[]>(initial?.layout ?? []);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const addSection = () => {
    const id = `sec_${Date.now()}`;
    setSections(prev => [...prev, { id, label: 'New Section', columns: 1, fields: [] }]);
  };

  const removeSection = (id: string) => setSections(prev => prev.filter(s => s.id !== id));

  const updateSection = (id: string, patch: Partial<LayoutSection>) =>
    setSections(prev => prev.map(s => s.id === id ? { ...s, ...patch } : s));

  const addFieldToSection = (sectionId: string, key: string) => {
    setSections(prev => prev.map(s => {
      if (s.id !== sectionId) return s;
      if (s.fields.some(f => f.key === key)) return s;
      return { ...s, fields: [...s.fields, { key }] };
    }));
  };

  const removeFieldFromSection = (sectionId: string, key: string) =>
    setSections(prev => prev.map(s =>
      s.id === sectionId ? { ...s, fields: s.fields.filter(f => f.key !== key) } : s
    ));

  const setFieldWidth = (sectionId: string, key: string, width: LayoutField['width']) =>
    setSections(prev => prev.map(s =>
      s.id === sectionId
        ? { ...s, fields: s.fields.map(f => f.key === key ? { ...f, width } : f) }
        : s
    ));

  const moveSectionUp = (idx: number) => {
    if (idx === 0) return;
    setSections(prev => { const a = [...prev]; [a[idx - 1], a[idx]] = [a[idx], a[idx - 1]]; return a; });
  };
  const moveSectionDown = (idx: number) => {
    setSections(prev => {
      if (idx >= prev.length - 1) return prev;
      const a = [...prev]; [a[idx], a[idx + 1]] = [a[idx + 1], a[idx]]; return a;
    });
  };

  const toggleRole = (id: string) =>
    setSelectedRoles(prev => { const s = new Set(prev); s.has(id) ? s.delete(id) : s.add(id); return s; });

  const save = async () => {
    if (!name.trim()) { setError('Name is required'); return; }
    setSaving(true); setError('');
    try {
      const roleIds = Array.from(selectedRoles);
      if (initial) {
        await updateObjectLayout(slug, initial.id, { name, layout: sections, is_default: isDefault });
        await setLayoutRoles(slug, initial.id, roleIds);
      } else {
        await createObjectLayout(slug, { name, layout: sections, is_default: isDefault, role_ids: roleIds });
      }
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
      setSaving(false);
    }
  };

  // Fields already placed in any section (across all sections) — to show in the "add" pickers.
  const placedKeys = new Set(sections.flatMap(s => s.fields.map(f => f.key)));
  const unplacedFields = fieldKeys.filter(f => !placedKeys.has(f.key));
  const fieldLabelMap = Object.fromEntries(fieldKeys.map(f => [f.key, f.label]));

  return (
    <div>
      <h4 style={{ margin: '0 0 16px' }}>{initial ? `Edit layout: ${initial.name}` : 'New layout'}</h4>
      {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

      {/* Name + options row */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr auto', gap: 16, alignItems: 'end', marginBottom: 16 }}>
        <div>
          <label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 4 }}>Layout name *</label>
          <input value={name} onChange={e => setName(e.target.value)} placeholder="e.g. Sales view" style={inputStyle} />
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, fontWeight: 500, cursor: 'pointer', paddingBottom: 2 }}>
          <input type="checkbox" checked={isDefault} onChange={e => setIsDefault(e.target.checked)} />
          Default for all roles
        </label>
      </div>

      {/* Role assignment */}
      {roles.length > 0 && (
        <div style={{ marginBottom: 16 }}>
          <label style={{ fontSize: 13, fontWeight: 500, display: 'block', marginBottom: 6 }}>Show this layout to</label>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {roles.map(r => {
              const active = selectedRoles.has(r.id);
              return (
                <button key={r.id} onClick={() => toggleRole(r.id)} style={{
                  padding: '5px 12px', borderRadius: 20, fontSize: 12, fontWeight: 500,
                  border: active ? '1px solid #3b82f6' : '1px solid #d1d5db',
                  background: active ? '#eff6ff' : '#fff', color: active ? '#3b82f6' : '#64748b',
                  cursor: 'pointer',
                }}>
                  {r.name}
                </button>
              );
            })}
          </div>
          <p style={{ fontSize: 11, color: '#94a3b8', margin: '4px 0 0' }}>
            Roles without an assignment fall back to the default layout, then flat field order.
          </p>
        </div>
      )}

      {/* Unplaced fields hint */}
      {unplacedFields.length > 0 && (
        <div style={{ background: '#fffbeb', border: '1px solid #fde68a', borderRadius: 6, padding: '8px 12px', marginBottom: 12, fontSize: 12, color: '#92400e' }}>
          {unplacedFields.length} field{unplacedFields.length > 1 ? 's' : ''} not in any section ({unplacedFields.map(f => f.label).join(', ')}) — they will appear in an "Other" section on the detail page.
        </div>
      )}

      {/* Sections */}
      <div style={{ marginBottom: 12 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <label style={{ fontSize: 13, fontWeight: 500 }}>Sections ({sections.length})</label>
          <button onClick={addSection} style={{ ...btn('#3b82f6'), padding: '5px 12px', fontSize: 12 }}>+ Section</button>
        </div>

        {sections.length === 0 && (
          <div style={{ padding: '24px', textAlign: 'center', border: '1px dashed #d1d5db', borderRadius: 6, color: '#94a3b8', fontSize: 13 }}>
            Add sections to group fields. Without sections, all fields render in the "Other" fallback.
          </div>
        )}

        {sections.map((sec, idx) => (
          <SectionEditor
            key={sec.id}
            section={sec}
            fieldLabelMap={fieldLabelMap}
            availableFields={fieldKeys.filter(f => !placedKeys.has(f.key))}
            onUpdate={patch => updateSection(sec.id, patch)}
            onRemove={() => removeSection(sec.id)}
            onMoveUp={idx > 0 ? () => moveSectionUp(idx) : undefined}
            onMoveDown={idx < sections.length - 1 ? () => moveSectionDown(idx) : undefined}
            onAddField={key => addFieldToSection(sec.id, key)}
            onRemoveField={key => removeFieldFromSection(sec.id, key)}
            onSetFieldWidth={(key, w) => setFieldWidth(sec.id, key, w)}
          />
        ))}
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        <button onClick={save} disabled={saving} style={btn(saving ? '#94a3b8' : '#3b82f6')}>
          {saving ? 'Saving…' : initial ? 'Update Layout' : 'Create Layout'}
        </button>
        <button onClick={onCancel} style={{ padding: '8px 16px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>Cancel</button>
      </div>
    </div>
  );
}

// ============================================================
// SectionEditor — one section's label, columns, and field list
// ============================================================

function SectionEditor({
  section,
  fieldLabelMap,
  availableFields,
  onUpdate,
  onRemove,
  onMoveUp,
  onMoveDown,
  onAddField,
  onRemoveField,
  onSetFieldWidth,
}: {
  section: LayoutSection;
  fieldLabelMap: Record<string, string>;
  availableFields: FieldEntry[];
  onUpdate: (patch: Partial<LayoutSection>) => void;
  onRemove: () => void;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  onAddField: (key: string) => void;
  onRemoveField: (key: string) => void;
  onSetFieldWidth: (key: string, width: LayoutField['width']) => void;
}) {
  const [fieldPick, setFieldPick] = useState('');

  return (
    <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, padding: 12, marginBottom: 8, background: '#fff' }}>
      {/* Section header controls */}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 10 }}>
        <input
          value={section.label}
          onChange={e => onUpdate({ label: e.target.value })}
          placeholder="Section label"
          style={{ ...inputStyle, padding: '5px 8px', fontSize: 13 }}
        />
        <select
          value={section.columns}
          onChange={e => onUpdate({ columns: Number(e.target.value) as 1 | 2 })}
          style={{ padding: '5px 8px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 13, cursor: 'pointer' }}
        >
          <option value={1}>1 column</option>
          <option value={2}>2 columns</option>
        </select>
        <div style={{ display: 'flex', gap: 2 }}>
          <button onClick={onMoveUp} disabled={!onMoveUp} style={{ background: 'none', border: '1px solid #e2e8f0', borderRadius: 4, cursor: onMoveUp ? 'pointer' : 'default', padding: '3px 7px', opacity: onMoveUp ? 1 : 0.3 }}>↑</button>
          <button onClick={onMoveDown} disabled={!onMoveDown} style={{ background: 'none', border: '1px solid #e2e8f0', borderRadius: 4, cursor: onMoveDown ? 'pointer' : 'default', padding: '3px 7px', opacity: onMoveDown ? 1 : 0.3 }}>↓</button>
        </div>
        <button onClick={onRemove} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#ef4444', fontSize: 16, padding: '0 4px' }} title="Remove section">✕</button>
      </div>

      {/* Field list */}
      {section.fields.length === 0 ? (
        <p style={{ color: '#94a3b8', fontSize: 12, margin: '0 0 8px' }}>No fields yet — add from the list below.</p>
      ) : (
        <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, overflow: 'hidden', marginBottom: 8 }}>
          {section.fields.map((f, fi) => (
            <div key={f.key} style={{ display: 'flex', alignItems: 'center', padding: '5px 8px', borderBottom: fi < section.fields.length - 1 ? '1px solid #f1f5f9' : 'none', fontSize: 13 }}>
              <span style={{ flex: 1, color: '#0f172a' }}>{fieldLabelMap[f.key] ?? f.key}</span>
              <code style={{ fontSize: 11, color: '#94a3b8', marginRight: 8 }}>{f.key}</code>
              {section.columns === 2 && (
                <select
                  value={f.width ?? 'half'}
                  onChange={e => onSetFieldWidth(f.key, e.target.value as LayoutField['width'])}
                  style={{ fontSize: 11, padding: '2px 5px', border: '1px solid #e2e8f0', borderRadius: 4, marginRight: 6, cursor: 'pointer' }}
                >
                  <option value="half">½ col</option>
                  <option value="full">full</option>
                </select>
              )}
              <button onClick={() => onRemoveField(f.key)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#ef4444', fontSize: 13 }}>✕</button>
            </div>
          ))}
        </div>
      )}

      {/* Add field picker */}
      {availableFields.length > 0 && (
        <div style={{ display: 'flex', gap: 6 }}>
          <select
            value={fieldPick}
            onChange={e => setFieldPick(e.target.value)}
            style={{ flex: 1, padding: '5px 8px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 12, cursor: 'pointer' }}
          >
            <option value="">— add a field —</option>
            {availableFields.map(f => <option key={f.key} value={f.key}>{f.label} ({f.key})</option>)}
          </select>
          <button
            onClick={() => { if (fieldPick) { onAddField(fieldPick); setFieldPick(''); } }}
            disabled={!fieldPick}
            style={{ ...btn(fieldPick ? '#3b82f6' : '#94a3b8'), padding: '5px 12px', fontSize: 12 }}
          >Add</button>
        </div>
      )}
    </div>
  );
}
