import React, { useState, useEffect, useRef } from 'react';
import type { FormPayload } from './chatTypes';
import {
  createContact, createDeal, getStages, getFieldDefs, getContacts,
  type PipelineStage, type CustomFieldDef, type Contact,
} from '../../lib/api';

interface Props {
  payload: FormPayload;
  onSuccess: (message: string) => void;
  onCancel: () => void;
}

// ── Contact Form ──────────────────────────────────────────────────────────────

function ContactForm({ payload, onSuccess, onCancel }: Props) {
  const [name, setName] = useState(payload.prefill_name || '');
  const [email, setEmail] = useState(payload.prefill_email || '');
  const [phone, setPhone] = useState(payload.prefill_phone || '');
  const [companyId] = useState('');
  const [customFields, setCustomFields] = useState<Record<string, unknown>>(payload.prefill_custom_fields || {});
  const [fieldDefs, setFieldDefs] = useState<CustomFieldDef[]>([]);
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);
  const formEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    getFieldDefs('contact').then(setFieldDefs).catch(() => {});
  }, []);

  // Scroll form buttons into view when form first renders or fieldDefs load
  useEffect(() => {
    setTimeout(() => formEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }), 150);
  }, [fieldDefs]);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = 'Full name is required';
    if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) e.email = 'Enter a valid email';
    for (const def of fieldDefs) {
      if (def.required && !customFields[def.key]) e[`cf_${def.key}`] = `${def.label} is required`;
    }
    return e;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) { setErrors(errs); return; }
    setLoading(true); setErrors({});
    try {
      const [first, ...rest] = name.trim().split(' ');
      const result = await createContact({
        first_name: first, last_name: rest.join(' ') || '',
        email: email || undefined, phone: phone || undefined,
        company_id: companyId || undefined,
        custom_fields: Object.keys(customFields).length > 0 ? customFields : undefined,
      } as Parameters<typeof createContact>[0]);
      setDone(true);
      const idRef = result?.id ? ` (id: ${result.id})` : '';
      setTimeout(() => onSuccess(`✅ Contact **${name}**${idRef} created successfully!`), 800);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : 'Failed to create contact' });
    } finally { setLoading(false); }
  };

  if (done) return <SuccessCard icon="👤" message={`Contact **${name}** created!`} />;

  return (
    <form onSubmit={submit} className="ai-inline-form" noValidate>
      <FormHeader icon="👤" title="New Contact" />
      <Field label="Full Name" required error={errors.name}>
        <input className="ai-f-input" data-err={errors.name ? '1' : ''} value={name}
          onChange={e => { setName(e.target.value); setErrors(p => ({ ...p, name: '' })); }}
          placeholder="Jane Smith" autoFocus />
      </Field>
      <Field label="Email" error={errors.email}>
        <input className="ai-f-input" data-err={errors.email ? '1' : ''} type="email" value={email}
          onChange={e => { setEmail(e.target.value); setErrors(p => ({ ...p, email: '' })); }}
          placeholder="jane@example.com" />
      </Field>
      <Field label="Phone">
        <input className="ai-f-input" value={phone}
          onChange={e => setPhone(e.target.value)} placeholder="+1 555 000 0000" />
      </Field>
      {fieldDefs.length > 0 && (
        <>
          <div className="ai-f-divider"><span>Custom Fields</span></div>
          {fieldDefs.map(def => (
            <CustomFieldInput key={def.key} def={def} value={customFields[def.key]}
              error={errors[`cf_${def.key}`]}
              onChange={v => setCustomFields(prev => ({ ...prev, [def.key]: v }))} />
          ))}
        </>
      )}
      {errors._form && <p className="ai-f-form-err">{errors._form}</p>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Contact" />
      <div ref={formEndRef} />
      <style>{formCSS}</style>
    </form>
  );
}

// ── Deal Form ─────────────────────────────────────────────────────────────────

function DealForm({ payload, onSuccess, onCancel }: Props) {
  const [title, setTitle] = useState(payload.prefill_title || '');
  const [value, setValue] = useState(payload.prefill_value != null ? String(payload.prefill_value) : '');
  const [probability, setProbability] = useState('20');
  const [stageId, setStageId] = useState('');
  const [contactId, setContactId] = useState(payload.prefill_contact_id || '');
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [contactSearch, setContactSearch] = useState(payload.prefill_contact_name || '');
  const [stages, setStages] = useState<PipelineStage[]>([]);
  const [customFields, setCustomFields] = useState<Record<string, unknown>>(payload.prefill_custom_fields || {});
  const [fieldDefs, setFieldDefs] = useState<CustomFieldDef[]>([]);
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);
  const formEndRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    getStages().then(st => { setStages(st); if (st.length > 0) setStageId(st[0].id); }).catch(() => {});
    getFieldDefs('deal').then(setFieldDefs).catch(() => {});
    const sq = payload.prefill_contact_name || '';
    getContacts({ q: sq || undefined, limit: 20 }).then(r => setContacts(r.contacts)).catch(() => {});
  }, []);

  // Scroll form buttons into view when form first renders or fieldDefs load
  useEffect(() => {
    setTimeout(() => formEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }), 150);
  }, [fieldDefs, stages]);

  useEffect(() => {
    if (!contactSearch.trim()) return;
    const t = setTimeout(() => {
      getContacts({ q: contactSearch, limit: 10 }).then(r => setContacts(r.contacts)).catch(() => {});
    }, 300);
    return () => clearTimeout(t);
  }, [contactSearch]);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!title.trim()) e.title = 'Deal title is required';
    if (value && isNaN(Number(value))) e.value = 'Must be a number';
    if (value && Number(value) < 0) e.value = 'Cannot be negative';
    const prob = Number(probability);
    if (probability && (isNaN(prob) || prob < 0 || prob > 100)) e.probability = '0–100';
    for (const def of fieldDefs) {
      if (def.required && !customFields[def.key]) e[`cf_${def.key}`] = `${def.label} is required`;
    }
    return e;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) { setErrors(errs); return; }
    setLoading(true); setErrors({});
    try {
      const result = await createDeal({
        title: title.trim(),
        value: value ? Number(value) : undefined,
        stage_id: stageId ? stageId : undefined,
        probability: probability ? Number(probability) : undefined,
        contact_id: contactId ? contactId : undefined,
      });
      setDone(true);
      const valueStr = value ? ` ($${Number(value).toLocaleString()})` : '';
      const idRef = result?.id ? ` (id: ${result.id})` : '';
      const cn = contactId ? contacts.find(c => c.id === contactId) : null;
      const contactName = cn ? ` linked to **${cn.first_name} ${cn.last_name}**` : '';
      setTimeout(() => onSuccess(`✅ Deal **${title}**${valueStr}${contactName}${idRef} created successfully!`), 800);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : 'Failed to create deal' });
    } finally { setLoading(false); }
  };

  if (done) return <SuccessCard icon="💼" message={`Deal **${title}** created!`} />;

  return (
    <form onSubmit={submit} className="ai-inline-form" noValidate>
      <FormHeader icon="💼" title="New Deal" />
      <Field label="Deal Title" required error={errors.title}>
        <input className="ai-f-input" data-err={errors.title ? '1' : ''} value={title}
          onChange={e => { setTitle(e.target.value); setErrors(p => ({ ...p, title: '' })); }}
          placeholder="e.g. Acme Corp - Enterprise" autoFocus />
      </Field>
      <div className="ai-f-row">
        <Field label="Value ($)" error={errors.value}>
          <input className="ai-f-input" data-err={errors.value ? '1' : ''} type="number" min="0" value={value}
            onChange={e => { setValue(e.target.value); setErrors(p => ({ ...p, value: '' })); }} placeholder="0" />
        </Field>
        <Field label="Probability (%)" error={errors.probability}>
          <input className="ai-f-input" data-err={errors.probability ? '1' : ''} type="number" min="0" max="100"
            value={probability}
            onChange={e => { setProbability(e.target.value); setErrors(p => ({ ...p, probability: '' })); }} placeholder="20" />
        </Field>
      </div>
      <Field label="Contact">
        <input className="ai-f-input" value={contactSearch}
          onChange={e => { setContactSearch(e.target.value); setContactId(''); }} placeholder="Search…" />
        {contacts.length > 0 && (
          <select className="ai-f-select" value={contactId}
            onChange={e => {
              setContactId(e.target.value);
              const c = contacts.find(ct => ct.id === e.target.value);
              if (c) setContactSearch(`${c.first_name} ${c.last_name}`.trim());
            }}>
            <option value="">— None —</option>
            {contacts.map(c => (
              <option key={c.id} value={c.id}>
                {`${c.first_name} ${c.last_name}`.trim()}{c.email ? ` (${c.email})` : ''}
              </option>
            ))}
          </select>
        )}
      </Field>
      {stages.length > 0 && (
        <Field label="Stage">
          <select className="ai-f-select" value={stageId} onChange={e => setStageId(e.target.value)}>
            {stages.map(st => <option key={st.id} value={st.id}>{st.name}</option>)}
          </select>
        </Field>
      )}
      {fieldDefs.length > 0 && (
        <>
          <div className="ai-f-divider"><span>Custom Fields</span></div>
          {fieldDefs.map(def => (
            <CustomFieldInput key={def.key} def={def} value={customFields[def.key]}
              error={errors[`cf_${def.key}`]}
              onChange={v => setCustomFields(prev => ({ ...prev, [def.key]: v }))} />
          ))}
        </>
      )}
      {errors._form && <p className="ai-f-form-err">{errors._form}</p>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Deal" />
      <div ref={formEndRef} />
      <style>{formCSS}</style>
    </form>
  );
}

// ── Custom Field Input ────────────────────────────────────────────────────────

function CustomFieldInput({ def, value, error, onChange }: {
  def: CustomFieldDef; value: unknown; error?: string; onChange: (v: unknown) => void;
}) {
  if (def.type === 'boolean') {
    return (
      <Field label={def.label} required={def.required} error={error}>
        <label className="ai-f-checkbox-row">
          <input type="checkbox" checked={!!value} onChange={e => onChange(e.target.checked)} />
          <span>{value ? 'Yes' : 'No'}</span>
        </label>
      </Field>
    );
  }
  if (def.type === 'select') {
    return (
      <Field label={def.label} required={def.required} error={error}>
        <select className="ai-f-select" value={(value as string) ?? ''} onChange={e => onChange(e.target.value || null)}>
          <option value="">Select…</option>
          {def.options?.map(opt => <option key={opt} value={opt}>{opt}</option>)}
        </select>
      </Field>
    );
  }
  const typeMap: Record<string, string> = { text: 'text', number: 'number', date: 'date', url: 'url' };
  return (
    <Field label={def.label} required={def.required} error={error}>
      <input className="ai-f-input" data-err={error ? '1' : ''}
        type={typeMap[def.type] || 'text'}
        value={value !== undefined && value !== null ? String(value) : ''}
        onChange={e => {
          const v = e.target.value;
          onChange(def.type === 'number' ? (v === '' ? null : parseFloat(v)) : v);
        }}
        placeholder={`Enter ${def.label.toLowerCase()}`} />
    </Field>
  );
}

// ── Shared sub-components ─────────────────────────────────────────────────────

function FormHeader({ icon, title }: { icon: string; title: string }) {
  return (
    <div className="ai-f-header">
      <span className="ai-f-header-icon">{icon}</span>
      <span className="ai-f-header-title">{title}</span>
    </div>
  );
}

function Field({ label, required, error, children }: {
  label: string; required?: boolean; error?: string; children: React.ReactNode;
}) {
  return (
    <div className="ai-f-field">
      <label className="ai-f-label">{label}{required && <span className="ai-f-req"> *</span>}</label>
      {children}
      {error && <p className="ai-f-field-err">{error}</p>}
    </div>
  );
}

function FormActions({ loading, onCancel, submitLabel }: {
  loading: boolean; onCancel: () => void; submitLabel: string;
}) {
  return (
    <div className="ai-f-actions">
      <button type="button" className="ai-f-cancel" onClick={onCancel} disabled={loading}>Cancel</button>
      <button type="submit" className="ai-f-submit" disabled={loading}>
        {loading ? 'Saving…' : submitLabel}
      </button>
    </div>
  );
}

function SuccessCard({ icon, message }: { icon: string; message: string }) {
  const parts = message.split(/\*\*(.*?)\*\*/g);
  return (
    <div className="ai-f-success">
      <span style={{ fontSize: 18 }}>{icon}</span>
      <p>
        {parts.map((p, i) => i % 2 === 1 ? <strong key={i}>{p}</strong> : p)}
      </p>
      <style>{formCSS}</style>
    </div>
  );
}

// ── Router entry point ────────────────────────────────────────────────────────

export default function InlineForm(props: Props) {
  if (props.payload.form_type === 'deal') return <DealForm {...props} />;
  if (props.payload.form_type === 'contact') return <ContactForm {...props} />;
  return null;
}

// ── CSS (class-based, scoped to .ai-inline-form) ─────────────────────────────

const formCSS = `
  .ai-inline-form {
    background: var(--card, #fff);
    border: 1px solid var(--border, #e5e7eb);
    border-radius: 16px;
    padding: 16px;
    margin: 4px 0 8px;
    box-shadow: 0 2px 12px rgba(0,0,0,0.06);
    animation: fadeSlide 0.22s ease;
    overflow: hidden;
  }
  .ai-f-header {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 14px;
    padding-bottom: 12px;
    border-bottom: 1px solid var(--border, #e5e7eb);
  }
  .ai-f-header-icon {
    width: 32px; height: 32px;
    border-radius: 8px;
    background: linear-gradient(135deg, rgba(245,158,11,0.12), rgba(239,68,68,0.08));
    display: flex; align-items: center; justify-content: center;
    font-size: 16px; flex-shrink: 0;
  }
  .ai-f-header-title {
    font-weight: 700; font-size: 15px;
    color: var(--foreground, #111);
  }
  .ai-f-field {
    margin-bottom: 12px;
  }
  .ai-f-label {
    display: block;
    font-size: 11px; font-weight: 600;
    color: var(--muted-foreground, #6b7280);
    margin-bottom: 4px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .ai-f-req { color: #ef4444; }
  .ai-f-input {
    width: 100%;
    border: 1.5px solid var(--border, #e5e7eb);
    border-radius: 10px;
    padding: 8px 12px;
    font-size: 13px;
    background: var(--background, #f9fafb);
    color: var(--foreground, #111);
    box-sizing: border-box;
    outline: none;
    transition: border-color 0.15s, box-shadow 0.15s;
    font-family: inherit;
  }
  .ai-f-input:focus {
    border-color: #f59e0b;
    box-shadow: 0 0 0 2px rgba(245,158,11,0.12);
  }
  .ai-f-input[data-err="1"] {
    border-color: #ef4444;
    box-shadow: 0 0 0 2px rgba(239,68,68,0.1);
  }
  .ai-f-select {
    width: 100%;
    border: 1.5px solid var(--border, #e5e7eb);
    border-radius: 10px;
    padding: 8px 12px;
    font-size: 13px;
    background: var(--background, #f9fafb);
    color: var(--foreground, #111);
    box-sizing: border-box;
    outline: none;
    cursor: pointer;
    font-family: inherit;
  }
  .ai-f-select:focus {
    border-color: #f59e0b;
    box-shadow: 0 0 0 2px rgba(245,158,11,0.12);
  }
  .ai-f-row {
    display: flex; gap: 10px;
  }
  .ai-f-row > .ai-f-field { flex: 1; min-width: 0; }
  .ai-f-divider {
    display: flex; align-items: center; gap: 8px;
    margin: 14px 0 10px;
  }
  .ai-f-divider::before, .ai-f-divider::after {
    content: ''; flex: 1; height: 1px;
    background: var(--border, #e5e7eb);
  }
  .ai-f-divider span {
    font-size: 9px; font-weight: 700;
    color: var(--muted-foreground, #9ca3af);
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .ai-f-field-err {
    color: #ef4444; font-size: 11px;
    margin: 3px 0 0; font-weight: 500;
  }
  .ai-f-form-err {
    color: #ef4444; font-size: 12px;
    margin: 6px 0; padding: 8px 12px;
    background: rgba(239,68,68,0.06);
    border-radius: 8px;
    border: 1px solid rgba(239,68,68,0.15);
  }
  .ai-f-actions {
    display: flex; gap: 8px;
    justify-content: flex-end;
    margin-top: 14px;
    padding-top: 12px;
    border-top: 1px solid var(--border, #e5e7eb);
  }
  .ai-f-cancel {
    padding: 7px 14px; border-radius: 10px;
    border: 1px solid var(--border, #e5e7eb);
    background: transparent; cursor: pointer;
    font-size: 12px; font-weight: 600;
    color: var(--muted-foreground, #6b7280);
    transition: all 0.15s;
  }
  .ai-f-cancel:hover {
    background: var(--background, #f9fafb);
    border-color: var(--foreground, #374151);
    color: var(--foreground, #374151);
  }
  .ai-f-submit {
    padding: 7px 18px; border-radius: 10px;
    border: none;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    color: #fff; cursor: pointer;
    font-size: 12px; font-weight: 700;
    transition: all 0.15s;
    box-shadow: 0 2px 8px rgba(245,158,11,0.25);
  }
  .ai-f-submit:hover {
    transform: translateY(-1px);
    box-shadow: 0 4px 12px rgba(245,158,11,0.35);
  }
  .ai-f-submit:disabled {
    opacity: 0.7; cursor: not-allowed; transform: none;
  }
  .ai-f-success {
    display: flex; align-items: center; gap: 10px;
    border: 1px solid #22c55e;
    border-radius: 14px;
    padding: 12px 16px;
    background: linear-gradient(135deg, rgba(34,197,94,0.08), rgba(16,185,129,0.04));
    margin: 4px 0 8px;
    animation: fadeSlide 0.22s ease;
  }
  .ai-f-success p {
    margin: 0; font-size: 13px;
    color: var(--foreground); line-height: 1.5;
  }
  .ai-f-checkbox-row {
    display: flex; align-items: center; gap: 8px;
    cursor: pointer; padding: 4px 0;
  }
  .ai-f-checkbox-row input {
    width: 16px; height: 16px;
    accent-color: #f59e0b; cursor: pointer;
  }
  .ai-f-checkbox-row span {
    font-size: 13px; color: var(--foreground, #374151);
  }
`;
