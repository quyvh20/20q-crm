import { useState, useEffect, useCallback } from 'react';
import {
  getObjectDefs, createObjectDef, updateObjectDef, deleteObjectDef,
  type CustomObjectDef, type CustomFieldDef,
} from '../../lib/api';

const ICONS = ['📦', '🏗️', '🚗', '📋', '🎯', '💼', '🏠', '📊', '🔧', '📝', '🎪', '🧩', '📁', '🗂️', '⚙️', '🛒'];

export default function ObjectDefManager() {
  const [defs, setDefs] = useState<CustomObjectDef[]>([]);
  const [loading, setLoading] = useState(true);
  const [showForm, setShowForm] = useState(false);
  const [editSlug, setEditSlug] = useState<string | null>(null);
  const [error, setError] = useState('');

  // Form state
  const [label, setLabel] = useState('');
  const [slug, setSlug] = useState('');
  const [labelPlural, setLabelPlural] = useState('');
  const [icon, setIcon] = useState('📦');
  const [fields, setFields] = useState<CustomFieldDef[]>([]);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  // Field builder state
  const [newFieldKey, setNewFieldKey] = useState('');
  const [newFieldLabel, setNewFieldLabel] = useState('');
  const [newFieldType, setNewFieldType] = useState('text');
  const [newFieldOptions, setNewFieldOptions] = useState<string[]>([]);
  const [newFieldOptionInput, setNewFieldOptionInput] = useState('');
  const [newFieldRequired, setNewFieldRequired] = useState(false);

  const fetchDefs = useCallback(async () => {
    try {
      setLoading(true);
      const data = await getObjectDefs();
      setDefs(data);
    } catch { /* ignore */ } finally { setLoading(false); }
  }, []);

  useEffect(() => { fetchDefs(); }, [fetchDefs]);

  const resetForm = () => {
    setLabel(''); setSlug(''); setLabelPlural(''); setIcon('📦');
    setFields([]); setEditSlug(null); setShowForm(false); setError('');
    resetFieldBuilder();
  };

  const resetFieldBuilder = () => {
    setNewFieldKey(''); setNewFieldLabel(''); setNewFieldType('text');
    setNewFieldOptions([]); setNewFieldOptionInput(''); setNewFieldRequired(false);
  };

  const autoSlug = (lbl: string) => lbl.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '');

  const handleLabelChange = (val: string) => {
    setLabel(val);
    if (!editSlug) {
      setSlug(autoSlug(val));
      setLabelPlural(val ? val + 's' : '');
    }
  };

  const addField = () => {
    if (!newFieldLabel.trim()) return;
    const key = newFieldKey || autoSlug(newFieldLabel);
    if (fields.some(f => f.key === key)) { setError(`Duplicate field key: ${key}`); return; }
    const field: CustomFieldDef = {
      key, label: newFieldLabel.trim(), type: newFieldType,
      entity_type: '', required: newFieldRequired, position: fields.length,
    };
    if (newFieldType === 'select') field.options = [...newFieldOptions];
    setFields([...fields, field]);
    resetFieldBuilder();
    setError('');
  };

  const removeField = (key: string) => setFields(fields.filter(f => f.key !== key));

  const handleSubmit = async () => {
    setError('');
    try {
      if (editSlug) {
        await updateObjectDef(editSlug, { label, label_plural: labelPlural, icon, fields });
      } else {
        await createObjectDef({ slug, label, label_plural: labelPlural, icon, fields });
      }
      resetForm();
      fetchDefs();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    }
  };

  const handleEdit = (def: CustomObjectDef) => {
    setEditSlug(def.slug);
    setLabel(def.label);
    setSlug(def.slug);
    setLabelPlural(def.label_plural);
    setIcon(def.icon);
    setFields(def.fields || []);
    setShowForm(true);
  };

  const handleDelete = async (defSlug: string) => {
    try {
      await deleteObjectDef(defSlug);
      setConfirmDelete(null);
      fetchDefs();
    } catch (e: unknown) { setError(e instanceof Error ? e.message : 'Delete failed'); }
  };

  const typeLabel = (t: string) => ({ text: 'Aa Text', number: '# Number', date: '📅 Date', select: '▼ Select', boolean: '✓ Yes/No', url: '🔗 URL' }[t] || t);

  if (loading) return <p style={{ color: '#94a3b8', padding: 20 }}>Loading...</p>;

  return (
    <div>
      {/* Object definitions table */}
      {defs.length === 0 && !showForm && (
        <div style={{ textAlign: 'center', padding: '48px 0', color: '#94a3b8' }}>
          <div style={{ fontSize: 40, marginBottom: 8 }}>📦</div>
          <p>No custom objects defined yet.</p>
          <p style={{ fontSize: 13 }}>Click "New Object" to create your first custom entity.</p>
        </div>
      )}

      {defs.length > 0 && (
        <table style={{ width: '100%', borderCollapse: 'collapse', marginBottom: 16 }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #e2e8f0', textAlign: 'left' }}>
              <th style={{ padding: '8px 12px', fontSize: 13, color: '#64748b' }}>Icon</th>
              <th style={{ padding: '8px 12px', fontSize: 13, color: '#64748b' }}>Label</th>
              <th style={{ padding: '8px 12px', fontSize: 13, color: '#64748b' }}>Slug</th>
              <th style={{ padding: '8px 12px', fontSize: 13, color: '#64748b' }}>Fields</th>
              <th style={{ padding: '8px 12px', fontSize: 13, color: '#64748b', textAlign: 'right' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {defs.map(def => (
              <tr key={def.id} style={{ borderBottom: '1px solid #f1f5f9' }}>
                <td style={{ padding: '10px 12px', fontSize: 20 }}>{def.icon}</td>
                <td style={{ padding: '10px 12px', fontWeight: 500 }}>{def.label} <span style={{ color: '#94a3b8', fontWeight: 400 }}>/ {def.label_plural}</span></td>
                <td style={{ padding: '10px 12px' }}><code style={{ background: '#f1f5f9', padding: '2px 6px', borderRadius: 4, fontSize: 13 }}>{def.slug}</code></td>
                <td style={{ padding: '10px 12px', color: '#64748b' }}>{(def.fields || []).length} fields</td>
                <td style={{ padding: '10px 12px', textAlign: 'right' }}>
                  {confirmDelete === def.slug ? (
                    <>
                      <button onClick={() => handleDelete(def.slug)} style={{ background: '#ef4444', color: '#fff', border: 'none', padding: '4px 12px', borderRadius: 4, cursor: 'pointer', marginRight: 4, fontSize: 13 }}>Confirm</button>
                      <button onClick={() => setConfirmDelete(null)} style={{ background: '#e2e8f0', border: 'none', padding: '4px 12px', borderRadius: 4, cursor: 'pointer', fontSize: 13 }}>Cancel</button>
                    </>
                  ) : (
                    <>
                      <button onClick={() => handleEdit(def)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, padding: 4 }} title="Edit">✏️</button>
                      <button onClick={() => setConfirmDelete(def.slug)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 16, padding: 4 }} title="Delete">🗑️</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {/* Add / Edit form */}
      {showForm && (
        <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, padding: 20, marginBottom: 16, background: '#fafbfc' }}>
          <h4 style={{ margin: '0 0 16px', color: '#1e293b' }}>{editSlug ? 'Edit Object' : 'New Custom Object'}</h4>

          {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{error}</div>}

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12, marginBottom: 16 }}>
            <div>
              <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>Label *</label>
              <input value={label} onChange={e => handleLabelChange(e.target.value)} placeholder="e.g. Project"
                style={{ width: '100%', padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, boxSizing: 'border-box' }} />
            </div>
            <div>
              <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>Slug</label>
              <input value={slug} onChange={e => setSlug(e.target.value)} disabled={!!editSlug}
                style={{ width: '100%', padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, boxSizing: 'border-box', background: editSlug ? '#f1f5f9' : '#fff' }} />
            </div>
            <div>
              <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>Plural Label</label>
              <input value={labelPlural} onChange={e => setLabelPlural(e.target.value)} placeholder="e.g. Projects"
                style={{ width: '100%', padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, boxSizing: 'border-box' }} />
            </div>
          </div>

          {/* Icon picker */}
          <div style={{ marginBottom: 16 }}>
            <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>Icon</label>
            <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
              {ICONS.map(ic => (
                <button key={ic} onClick={() => setIcon(ic)}
                  style={{ fontSize: 20, padding: '4px 8px', border: icon === ic ? '2px solid #3b82f6' : '1px solid #e2e8f0', borderRadius: 6, background: icon === ic ? '#eff6ff' : '#fff', cursor: 'pointer' }}>
                  {ic}
                </button>
              ))}
            </div>
          </div>

          {/* Fields list */}
          <div style={{ marginBottom: 16 }}>
            <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 8 }}>Fields ({fields.length})</label>
            {fields.length > 0 && (
              <div style={{ border: '1px solid #e2e8f0', borderRadius: 6, overflow: 'hidden', marginBottom: 8 }}>
                {fields.map((f, i) => (
                  <div key={f.key} style={{ display: 'flex', alignItems: 'center', padding: '6px 10px', borderBottom: i < fields.length - 1 ? '1px solid #f1f5f9' : 'none', background: '#fff' }}>
                    <span style={{ flex: 1, fontWeight: 500, fontSize: 13 }}>{f.label}</span>
                    <code style={{ fontSize: 12, color: '#64748b', marginRight: 8 }}>{f.key}</code>
                    <span style={{ fontSize: 12, color: '#3b82f6', marginRight: 8 }}>{typeLabel(f.type)}</span>
                    {f.required && <span style={{ fontSize: 11, color: '#ef4444', marginRight: 8 }}>Required</span>}
                    <button onClick={() => removeField(f.key)} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#ef4444', fontSize: 14 }}>✕</button>
                  </div>
                ))}
              </div>
            )}

            {/* Add field builder */}
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr auto auto', gap: 8, alignItems: 'end' }}>
              <div>
                <label style={{ fontSize: 12, color: '#64748b' }}>Field Label</label>
                <input value={newFieldLabel} onChange={e => { setNewFieldLabel(e.target.value); setNewFieldKey(autoSlug(e.target.value)); }}
                  placeholder="e.g. Priority" style={{ width: '100%', padding: '6px 8px', border: '1px solid #d1d5db', borderRadius: 4, fontSize: 13, boxSizing: 'border-box' }} />
              </div>
              <div>
                <label style={{ fontSize: 12, color: '#64748b' }}>Type</label>
                <select value={newFieldType} onChange={e => setNewFieldType(e.target.value)}
                  style={{ width: '100%', padding: '6px 8px', border: '1px solid #d1d5db', borderRadius: 4, fontSize: 13, boxSizing: 'border-box' }}>
                  <option value="text">Text</option>
                  <option value="number">Number</option>
                  <option value="date">Date</option>
                  <option value="select">Select</option>
                  <option value="boolean">Yes/No</option>
                  <option value="url">URL</option>
                </select>
              </div>
              <label style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, color: '#64748b', cursor: 'pointer' }}>
                <input type="checkbox" checked={newFieldRequired} onChange={e => setNewFieldRequired(e.target.checked)} /> Req
              </label>
              <button onClick={addField} disabled={!newFieldLabel.trim()}
                style={{ padding: '6px 14px', border: 'none', borderRadius: 4, cursor: 'pointer', fontSize: 13, fontWeight: 500, background: newFieldLabel.trim() ? '#3b82f6' : '#94a3b8', color: '#fff' }}>+ Add</button>
            </div>

            {newFieldType === 'select' && (
              <div style={{ marginTop: 8 }}>
                <label style={{ fontSize: 12, color: '#64748b' }}>Options (press Enter)</label>
                <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginBottom: 4 }}>
                  {newFieldOptions.map(opt => (
                    <span key={opt} style={{ background: '#eff6ff', color: '#3b82f6', padding: '2px 8px', borderRadius: 12, fontSize: 12, display: 'flex', alignItems: 'center', gap: 4 }}>
                      {opt} <button onClick={() => setNewFieldOptions(newFieldOptions.filter(o => o !== opt))} style={{ background: 'none', border: 'none', cursor: 'pointer', color: '#3b82f6', fontSize: 12, padding: 0 }}>✕</button>
                    </span>
                  ))}
                </div>
                <input value={newFieldOptionInput} onChange={e => setNewFieldOptionInput(e.target.value)}
                  onKeyDown={e => { if (e.key === 'Enter' && newFieldOptionInput.trim()) { e.preventDefault(); setNewFieldOptions([...newFieldOptions, newFieldOptionInput.trim()]); setNewFieldOptionInput(''); } }}
                  placeholder="Type and press Enter" style={{ width: '100%', padding: '6px 8px', border: '1px solid #d1d5db', borderRadius: 4, fontSize: 13, boxSizing: 'border-box' }} />
              </div>
            )}
          </div>

          <div style={{ display: 'flex', gap: 8 }}>
            <button onClick={handleSubmit}
              style={{ padding: '8px 20px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>
              {editSlug ? 'Update Object' : 'Create Object'}
            </button>
            <button onClick={resetForm} style={{ padding: '8px 20px', background: '#e2e8f0', border: 'none', borderRadius: 6, cursor: 'pointer' }}>Cancel</button>
          </div>
        </div>
      )}

      <button onClick={() => { resetForm(); setShowForm(true); }}
        style={{ padding: '8px 20px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500, display: showForm ? 'none' : 'inline-flex', alignItems: 'center', gap: 6 }}>
        + New Object
      </button>
    </div>
  );
}
