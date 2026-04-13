import { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import {
  getObjectDef, getObjectRecords, createObjectRecord, updateObjectRecord, deleteObjectRecord,
  getContacts,
  type CustomObjectDef, type CustomObjectRecord, type CustomFieldDef, type Contact,
} from '../lib/api';

export default function CustomObjectPage() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [def, setDef] = useState<CustomObjectDef | null>(null);
  const [records, setRecords] = useState<CustomObjectRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(0);
  const limit = 25;

  // Form state
  const [showForm, setShowForm] = useState(false);
  const [editId, setEditId] = useState<string | null>(null);
  const [formData, setFormData] = useState<Record<string, unknown>>({});
  const [contactId, setContactId] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  // Contact dropdown
  const [contacts, setContacts] = useState<Contact[]>([]);

  const fetchDef = useCallback(async () => {
    if (!slug) return;
    try {
      const d = await getObjectDef(slug);
      setDef(d);
    } catch {
      navigate('/settings');
    }
  }, [slug, navigate]);

  const fetchRecords = useCallback(async () => {
    if (!slug) return;
    try {
      setLoading(true);
      const { records: recs, total: t } = await getObjectRecords(slug, { limit, offset: page * limit, q: search });
      setRecords(recs);
      setTotal(t);
    } catch { /* ignore */ } finally { setLoading(false); }
  }, [slug, page, search]);

  useEffect(() => { fetchDef(); }, [fetchDef]);
  useEffect(() => { fetchRecords(); }, [fetchRecords]);
  useEffect(() => {
    getContacts({ limit: 100 }).then(res => setContacts(res.contacts || [])).catch(() => {});
  }, []);

  const resetForm = () => {
    setEditId(null); setFormData({}); setContactId(''); setShowForm(false); setError(''); setSaving(false);
  };

  const handleEdit = (rec: CustomObjectRecord) => {
    setEditId(rec.id);
    setFormData(rec.data || {});
    setContactId(rec.contact_id || '');
    setShowForm(true);
    setError('');
  };

  const handleSubmit = async () => {
    if (!slug || !def) return;
    setError(''); setSaving(true);
    try {
      if (editId) {
        await updateObjectRecord(slug, editId, { data: formData, contact_id: contactId || undefined });
      } else {
        await createObjectRecord(slug, { data: formData, contact_id: contactId || undefined });
      }
      resetForm();
      fetchRecords();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally { setSaving(false); }
  };

  const handleDelete = async (id: string) => {
    if (!slug) return;
    try {
      await deleteObjectRecord(slug, id);
      setConfirmDelete(null);
      fetchRecords();
    } catch { /* ignore */ }
  };

  const renderFieldValue = (field: CustomFieldDef, val: unknown) => {
    if (val === null || val === undefined || val === '') return <span style={{ color: '#94a3b8' }}>—</span>;
    if (field.type === 'boolean') return val ? '✅' : '❌';
    if (field.type === 'url') return <a href={String(val)} target="_blank" rel="noreferrer" style={{ color: '#3b82f6' }}>{String(val).replace(/^https?:\/\//, '').slice(0, 30)}</a>;
    if (field.type === 'date') return new Date(String(val)).toLocaleDateString();
    return String(val);
  };

  const renderFieldInput = (field: CustomFieldDef) => {
    const val = formData[field.key] ?? '';
    const common = { width: '100%' as const, padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, boxSizing: 'border-box' as const };

    switch (field.type) {
      case 'text':
        return <input type="text" value={String(val)} onChange={e => setFormData({ ...formData, [field.key]: e.target.value })} placeholder={`Enter ${field.label.toLowerCase()}`} style={common} />;
      case 'number':
        return <input type="number" value={val === '' ? '' : Number(val)} onChange={e => setFormData({ ...formData, [field.key]: e.target.value ? Number(e.target.value) : '' })} style={common} />;
      case 'date':
        return <input type="date" value={String(val)} onChange={e => setFormData({ ...formData, [field.key]: e.target.value })} style={common} />;
      case 'url':
        return <input type="url" value={String(val)} onChange={e => setFormData({ ...formData, [field.key]: e.target.value })} placeholder="https://..." style={common} />;
      case 'boolean':
        return <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}><input type="checkbox" checked={!!val} onChange={e => setFormData({ ...formData, [field.key]: e.target.checked })} /> Yes</label>;
      case 'select':
        return (
          <select value={String(val)} onChange={e => setFormData({ ...formData, [field.key]: e.target.value })} style={common}>
            <option value="">— Select —</option>
            {(field.options || []).map(opt => <option key={opt} value={opt}>{opt}</option>)}
          </select>
        );
      default:
        return <input type="text" value={String(val)} onChange={e => setFormData({ ...formData, [field.key]: e.target.value })} style={common} />;
    }
  };

  if (!def) return <div style={{ padding: 40, color: '#94a3b8', textAlign: 'center' }}>Loading...</div>;

  const fields = def.fields || [];
  const displayFields = fields.slice(0, 5); // Show at most 5 columns

  return (
    <div>
      {/* Header */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 24 }}>
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 700, margin: 0 }}>{def.icon} {def.label_plural}</h1>
          <p style={{ color: '#64748b', marginTop: 4, fontSize: 14 }}>Manage your {def.label_plural.toLowerCase()}</p>
        </div>
        <button onClick={() => { resetForm(); setShowForm(true); }}
          style={{ padding: '10px 20px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 8, cursor: 'pointer', fontWeight: 600, fontSize: 14 }}>
          + Add {def.label}
        </button>
      </div>

      {/* Search */}
      <div style={{ marginBottom: 16 }}>
        <input value={search} onChange={e => { setSearch(e.target.value); setPage(0); }} placeholder={`Search ${def.label_plural.toLowerCase()}...`}
          style={{ width: 300, padding: '8px 12px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14 }} />
      </div>

      {/* Records table */}
      <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, overflow: 'hidden', background: '#fff' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse' }}>
          <thead>
            <tr style={{ borderBottom: '1px solid #e2e8f0', background: '#f8fafc' }}>
              <th style={{ padding: '10px 12px', textAlign: 'left', fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase' }}>Name</th>
              {displayFields.map(f => (
                <th key={f.key} style={{ padding: '10px 12px', textAlign: 'left', fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase' }}>{f.label}</th>
              ))}
              <th style={{ padding: '10px 12px', textAlign: 'left', fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase' }}>Linked Contact</th>
              <th style={{ padding: '10px 12px', textAlign: 'left', fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase' }}>Created</th>
              <th style={{ padding: '10px 12px', textAlign: 'right', fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={displayFields.length + 4} style={{ padding: 40, textAlign: 'center', color: '#94a3b8' }}>Loading...</td></tr>
            ) : records.length === 0 ? (
              <tr><td colSpan={displayFields.length + 4} style={{ padding: 40, textAlign: 'center', color: '#94a3b8' }}>
                <div style={{ fontSize: 32, marginBottom: 8 }}>{def.icon}</div>
                No {def.label_plural.toLowerCase()} yet. Click "+ Add {def.label}" to create one.
              </td></tr>
            ) : (
              records.map(rec => (
                <tr key={rec.id} style={{ borderBottom: '1px solid #f1f5f9' }}>
                  <td style={{ padding: '10px 12px', fontWeight: 500 }}>{rec.display_name || 'Untitled'}</td>
                  {displayFields.map(f => (
                    <td key={f.key} style={{ padding: '10px 12px', fontSize: 13 }}>
                      {renderFieldValue(f, rec.data?.[f.key])}
                    </td>
                  ))}
                  <td style={{ padding: '10px 12px', fontSize: 13 }}>
                    {rec.contact ? `${rec.contact.first_name} ${rec.contact.last_name}` : <span style={{ color: '#94a3b8' }}>—</span>}
                  </td>
                  <td style={{ padding: '10px 12px', fontSize: 13, color: '#64748b' }}>
                    {new Date(rec.created_at).toLocaleDateString()}
                  </td>
                  <td style={{ padding: '10px 12px', textAlign: 'right' }}>
                    {confirmDelete === rec.id ? (
                      <>
                        <button onClick={() => handleDelete(rec.id)} style={{ background: '#ef4444', color: '#fff', border: 'none', padding: '4px 12px', borderRadius: 4, cursor: 'pointer', fontSize: 12, marginRight: 4 }}>Confirm</button>
                        <button onClick={() => setConfirmDelete(null)} style={{ background: '#e2e8f0', border: 'none', padding: '4px 12px', borderRadius: 4, cursor: 'pointer', fontSize: 12 }}>Cancel</button>
                      </>
                    ) : (
                      <>
                        <button onClick={() => handleEdit(rec)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, padding: 4 }} title="Edit">✏️</button>
                        <button onClick={() => setConfirmDelete(rec.id)} style={{ background: 'none', border: 'none', cursor: 'pointer', fontSize: 14, padding: 4 }} title="Delete">🗑️</button>
                      </>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      {total > limit && (
        <div style={{ display: 'flex', justifyContent: 'center', gap: 8, marginTop: 16, alignItems: 'center' }}>
          <button onClick={() => setPage(p => Math.max(0, p - 1))} disabled={page === 0}
            style={{ padding: '6px 14px', border: '1px solid #d1d5db', borderRadius: 4, cursor: page === 0 ? 'default' : 'pointer', opacity: page === 0 ? 0.5 : 1 }}>← Prev</button>
          <span style={{ fontSize: 13, color: '#64748b' }}>Page {page + 1} of {Math.ceil(total / limit)}</span>
          <button onClick={() => setPage(p => p + 1)} disabled={(page + 1) * limit >= total}
            style={{ padding: '6px 14px', border: '1px solid #d1d5db', borderRadius: 4, cursor: (page + 1) * limit >= total ? 'default' : 'pointer', opacity: (page + 1) * limit >= total ? 0.5 : 1 }}>Next →</button>
        </div>
      )}

      <div style={{ marginTop: 8, textAlign: 'center', color: '#94a3b8', fontSize: 13 }}>
        Showing {records.length} of {total} {def.label_plural.toLowerCase()}
      </div>

      {/* Create / Edit slide-over */}
      {showForm && (
        <div style={{ position: 'fixed', top: 0, right: 0, bottom: 0, width: 420, background: '#fff', boxShadow: '-4px 0 20px rgba(0,0,0,0.1)', zIndex: 50, display: 'flex', flexDirection: 'column' }}>
          <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>{editId ? `Edit ${def.label}` : `New ${def.label}`}</h3>
            <button onClick={resetForm} style={{ background: 'none', border: 'none', fontSize: 20, cursor: 'pointer', color: '#64748b' }}>×</button>
          </div>

          <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
            {error && <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 16, fontSize: 13 }}>{error}</div>}

            {fields.map(field => (
              <div key={field.key} style={{ marginBottom: 16 }}>
                <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>
                  {field.label}{field.required && <span style={{ color: '#ef4444' }}> *</span>}
                </label>
                {renderFieldInput(field)}
              </div>
            ))}

            {/* Contact link */}
            <div style={{ marginTop: 24, paddingTop: 16, borderTop: '1px solid #e2e8f0' }}>
              <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>Link to Contact (optional)</label>
              <select value={contactId} onChange={e => setContactId(e.target.value)}
                style={{ width: '100%', padding: '8px 10px', border: '1px solid #d1d5db', borderRadius: 6, fontSize: 14, boxSizing: 'border-box' }}>
                <option value="">— None —</option>
                {contacts.map(c => (
                  <option key={c.id} value={c.id}>{c.first_name} {c.last_name}{c.email ? ` (${c.email})` : ''}</option>
                ))}
              </select>
            </div>
          </div>

          <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', gap: 8 }}>
            <button onClick={resetForm} style={{ flex: 1, padding: '10px', background: '#f1f5f9', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Cancel</button>
            <button onClick={handleSubmit} disabled={saving}
              style={{ flex: 1, padding: '10px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600 }}>
              {saving ? 'Saving...' : (editId ? `Update ${def.label}` : `Create ${def.label}`)}
            </button>
          </div>
        </div>
      )}

      {/* Backdrop */}
      {showForm && (
        <div onClick={resetForm} style={{ position: 'fixed', top: 0, left: 0, right: 0, bottom: 0, background: 'rgba(0,0,0,0.3)', zIndex: 49 }} />
      )}
    </div>
  );
}
