import { useState, useEffect, useCallback } from 'react';
import {
  listRegistryObjects, getObjectSchema, getObjectDef,
  createObjectDef, updateObjectDef, deleteObjectDef,
  createFieldDef, updateFieldDef, deleteFieldDef,
  type ObjectSummary, type CustomFieldDef, type FieldType,
} from '../../lib/api';

// ObjectsManager is the single admin surface for every object's schema (P7 — it
// replaces the separate CustomFieldManager + ObjectDefManager now that object_defs/
// object_fields is one store). It lists all objects from the registry; custom
// objects are created/edited/deleted via the custom-object API, while system
// objects' native fields are read-only and their admin-defined custom fields are
// managed through the settings field API. Both write to object_fields.

const ICONS = ['📦', '🏗️', '🚗', '📋', '🎯', '💼', '🏠', '📊', '🔧', '📝', '🎪', '🧩', '📁', '🗂️', '⚙️', '🛒'];
const FIELD_TYPES: FieldType[] = ['text', 'number', 'date', 'select', 'boolean', 'url'];
const typeLabel = (t: string) => ({ text: 'Aa Text', number: '# Number', date: '📅 Date', select: '▼ Select', boolean: '✓ Yes/No', url: '🔗 URL' }[t] || t);
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
// Field builder (shared add/edit form for one field)
// ============================================================

interface FieldDraft {
  key: string;
  label: string;
  type: string;
  options: string[];
  required: boolean;
}
const emptyDraft: FieldDraft = { key: '', label: '', type: 'text', options: [], required: false };

function FieldBuilder({ draft, setDraft, onAdd, editing }: {
  draft: FieldDraft; setDraft: (d: FieldDraft) => void; onAdd: () => void; editing: boolean;
}) {
  const [optInput, setOptInput] = useState('');
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
        <button onClick={onAdd} disabled={!draft.label.trim()} style={{ ...btn(draft.label.trim() ? '#3b82f6' : '#94a3b8'), padding: '6px 14px', fontSize: 13 }}>{editing ? 'Save' : '+ Add'}</button>
      </div>
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
    const f: CustomFieldDef = { key, label: draft.label.trim(), type: draft.type as FieldType, required: draft.required, position: fields.length };
    if (draft.type === 'select') f.options = [...draft.options];
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
        <FieldBuilder draft={draft} setDraft={setDraft} onAdd={addField} editing={false} />
      </div>

      <div style={{ display: 'flex', gap: 8 }}>
        <button onClick={save} style={btn('#3b82f6')}>{editSlug ? 'Update Object' : 'Create Object'}</button>
        <button onClick={onCancel} style={{ padding: '8px 16px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>Cancel</button>
      </div>
    </div>
  );
}

// ============================================================
// System object fields editor (native read-only; custom fields via settings API)
// ============================================================

interface SchemaFieldRow { key: string; label: string; type: string; is_system: boolean; required: boolean; options?: string[] }

function SystemFieldsEditor({ slug, onBack }: { slug: string; onBack: () => void }) {
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
      setRows(s.fields.map(f => ({ key: f.key, label: f.label, type: f.type, is_system: f.is_system, required: f.required, options: f.options })));
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
    const payload = { label: draft.label.trim(), type: draft.type, required: draft.required, options: draft.type === 'select' ? draft.options : undefined };
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
    setDraft({ key: r.key, label: r.label, type: r.type, options: r.options || [], required: r.required });
  };

  const removeField = async (key: string) => {
    setError('');
    try { await deleteFieldDef(key); load(); } catch (e) { setError(e instanceof Error ? e.message : 'Delete failed'); }
  };

  if (loading) return <p style={{ color: '#94a3b8', padding: 20 }}>Loading...</p>;

  return (
    <div>
      <h4 style={{ margin: '0 0 4px' }}>{icon} {label} <span style={{ fontSize: 12, color: '#3b82f6', fontWeight: 400 }}>· Built-in</span></h4>
      <p style={{ color: '#64748b', fontSize: 13, marginTop: 0 }}>Built-in fields are fixed; add or edit your own custom fields below.</p>
      {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

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
      <FieldBuilder draft={draft} setDraft={setDraft} onAdd={saveField} editing={!!editingKey} />

      <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
        <button onClick={onBack} style={{ padding: '8px 16px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>← Back to objects</button>
        {editingKey && <button onClick={() => { setEditingKey(null); setDraft({ ...emptyDraft }); }} style={{ padding: '8px 16px', background: '#fff', border: '1px solid #d1d5db', borderRadius: 6, cursor: 'pointer' }}>Cancel edit</button>}
      </div>
    </div>
  );
}
