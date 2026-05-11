import React, { useState, useEffect } from 'react';
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

  useEffect(() => {
    getFieldDefs('contact').then(setFieldDefs).catch(() => {});
  }, []);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = 'Full name is required';
    if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) e.email = 'Enter a valid email address';
    for (const def of fieldDefs) {
      if (def.required && !customFields[def.key]) {
        e[`cf_${def.key}`] = `${def.label} is required`;
      }
    }
    return e;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) { setErrors(errs); return; }
    setLoading(true);
    setErrors({});
    try {
      const [first, ...rest] = name.trim().split(' ');
      const result = await createContact({
        first_name: first,
        last_name: rest.join(' ') || '',
        email: email || undefined,
        phone: phone || undefined,
        company_id: companyId || undefined,
        custom_fields: Object.keys(customFields).length > 0 ? customFields : undefined,
      } as Parameters<typeof createContact>[0]);
      setDone(true);
      const idRef = result?.id ? ` (id: ${result.id})` : '';
      setTimeout(() => onSuccess(`✅ Contact **${name}**${idRef} created successfully!`), 900);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : 'Failed to create contact' });
    } finally {
      setLoading(false);
    }
  };

  if (done) return <ModalShell onClose={onCancel}><SuccessCard icon="👤" message={`Contact **${name}** created!`} /></ModalShell>;

  return (
    <ModalShell onClose={onCancel}>
      <form onSubmit={submit} noValidate>
        <FormHeader icon="👤" title="New Contact" subtitle="Fill in the details below" />

        <div style={s.grid2}>
          <Field label="Full Name" required error={errors.name}>
            <input style={fieldInputStyle(!!errors.name)} value={name}
              onChange={e => { setName(e.target.value); setErrors(p => ({ ...p, name: '' })); }}
              placeholder="Jane Smith" autoFocus />
          </Field>
          <Field label="Email" error={errors.email}>
            <input style={fieldInputStyle(!!errors.email)} type="email" value={email}
              onChange={e => { setEmail(e.target.value); setErrors(p => ({ ...p, email: '' })); }}
              placeholder="jane@example.com" />
          </Field>
        </div>

        <Field label="Phone">
          <input style={fieldInputStyle(false)} value={phone}
            onChange={e => setPhone(e.target.value)} placeholder="+1 555 000 0000" />
        </Field>

        {/* Dynamic custom fields */}
        {fieldDefs.length > 0 && (
          <>
            <div style={s.sectionDivider}>
              <div style={s.dividerLine} />
              <span style={s.dividerLabel}>Custom Fields</span>
              <div style={s.dividerLine} />
            </div>
            <div style={s.grid2}>
              {fieldDefs.map(def => (
                <CustomFieldInput
                  key={def.key}
                  def={def}
                  value={customFields[def.key]}
                  error={errors[`cf_${def.key}`]}
                  onChange={v => setCustomFields(prev => ({ ...prev, [def.key]: v }))}
                />
              ))}
            </div>
          </>
        )}

        {errors._form && <p style={s.formError}>{errors._form}</p>}
        <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Contact" />
      </form>
    </ModalShell>
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

  useEffect(() => {
    getStages()
      .then(st => { setStages(st); if (st.length > 0) setStageId(st[0].id); })
      .catch(() => {});
    getFieldDefs('deal').then(setFieldDefs).catch(() => {});
    const searchQ = payload.prefill_contact_name || '';
    getContacts({ q: searchQ || undefined, limit: 20 })
      .then(r => setContacts(r.contacts))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!contactSearch.trim()) return;
    const timer = setTimeout(() => {
      getContacts({ q: contactSearch, limit: 10 })
        .then(r => setContacts(r.contacts))
        .catch(() => {});
    }, 300);
    return () => clearTimeout(timer);
  }, [contactSearch]);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!title.trim()) e.title = 'Deal title is required';
    if (value && isNaN(Number(value))) e.value = 'Value must be a number';
    if (value && Number(value) < 0) e.value = 'Value cannot be negative';
    const prob = Number(probability);
    if (probability && (isNaN(prob) || prob < 0 || prob > 100)) e.probability = 'Probability must be 0–100';
    for (const def of fieldDefs) {
      if (def.required && !customFields[def.key]) {
        e[`cf_${def.key}`] = `${def.label} is required`;
      }
    }
    return e;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) { setErrors(errs); return; }
    setLoading(true);
    setErrors({});
    try {
      const result = await createDeal({
        title: title.trim(),
        value:       value       ? Number(value)       : undefined,
        stage_id:    stageId     ? stageId             : undefined,
        probability: probability ? Number(probability) : undefined,
        contact_id:  contactId   ? contactId           : undefined,
      });
      setDone(true);
      const valueStr = value ? ` ($${Number(value).toLocaleString()})` : '';
      const idRef = result?.id ? ` (id: ${result.id})` : '';
      const contactName = contactId
        ? contacts.find(c => c.id === contactId)
          ? ` linked to **${contacts.find(c => c.id === contactId)!.first_name} ${contacts.find(c => c.id === contactId)!.last_name}**`
          : ''
        : '';
      setTimeout(() => onSuccess(`✅ Deal **${title}**${valueStr}${contactName}${idRef} created successfully!`), 900);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : 'Failed to create deal' });
    } finally {
      setLoading(false);
    }
  };

  if (done) return <ModalShell onClose={onCancel}><SuccessCard icon="💼" message={`Deal **${title}** created!`} /></ModalShell>;

  return (
    <ModalShell onClose={onCancel}>
      <form onSubmit={submit} noValidate>
        <FormHeader icon="💼" title="New Deal" subtitle="Set up deal details" />

        <Field label="Deal Title" required error={errors.title}>
          <input style={fieldInputStyle(!!errors.title)} value={title}
            onChange={e => { setTitle(e.target.value); setErrors(p => ({ ...p, title: '' })); }}
            placeholder="e.g. Acme Corp - Enterprise Plan" autoFocus />
        </Field>

        <div style={s.grid2}>
          <Field label="Value ($)" error={errors.value}>
            <input style={fieldInputStyle(!!errors.value)} type="number" min="0" value={value}
              onChange={e => { setValue(e.target.value); setErrors(p => ({ ...p, value: '' })); }}
              placeholder="0" />
          </Field>
          <Field label="Win Probability (%)" error={errors.probability}>
            <input style={fieldInputStyle(!!errors.probability)} type="number" min="0" max="100"
              value={probability}
              onChange={e => { setProbability(e.target.value); setErrors(p => ({ ...p, probability: '' })); }}
              placeholder="20" />
          </Field>
        </div>

        {/* Contact selector */}
        <Field label="Link to Contact">
          <input
            style={fieldInputStyle(false)}
            value={contactSearch}
            onChange={e => { setContactSearch(e.target.value); setContactId(''); }}
            placeholder="Search contacts…"
          />
          {contacts.length > 0 && (
            <select
              style={{ ...s.select, marginTop: 6 }}
              value={contactId}
              onChange={e => {
                setContactId(e.target.value);
                const c = contacts.find(ct => ct.id === e.target.value);
                if (c) setContactSearch(`${c.first_name} ${c.last_name}`.trim());
              }}
            >
              <option value="">— None —</option>
              {contacts.map(c => (
                <option key={c.id} value={c.id}>
                  {`${c.first_name} ${c.last_name}`.trim()}
                  {c.email ? ` (${c.email})` : ''}
                </option>
              ))}
            </select>
          )}
        </Field>

        {stages.length > 0 && (
          <Field label="Pipeline Stage">
            <select style={s.select} value={stageId} onChange={e => setStageId(e.target.value)}>
              {stages.map(st => (
                <option key={st.id} value={st.id}>{st.name}</option>
              ))}
            </select>
          </Field>
        )}

        {/* Dynamic custom fields */}
        {fieldDefs.length > 0 && (
          <>
            <div style={s.sectionDivider}>
              <div style={s.dividerLine} />
              <span style={s.dividerLabel}>Custom Fields</span>
              <div style={s.dividerLine} />
            </div>
            <div style={s.grid2}>
              {fieldDefs.map(def => (
                <CustomFieldInput
                  key={def.key}
                  def={def}
                  value={customFields[def.key]}
                  error={errors[`cf_${def.key}`]}
                  onChange={v => setCustomFields(prev => ({ ...prev, [def.key]: v }))}
                />
              ))}
            </div>
          </>
        )}

        {errors._form && <p style={s.formError}>{errors._form}</p>}
        <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Deal" />
      </form>
    </ModalShell>
  );
}

// ── Custom Field Input ────────────────────────────────────────────────────────

function CustomFieldInput({
  def, value, error, onChange,
}: {
  def: CustomFieldDef;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}) {
  const label = def.label;
  const req = def.required;

  if (def.type === 'boolean') {
    return (
      <Field label={label} required={req} error={error}>
        <label style={s.checkboxRow}>
          <input type="checkbox" checked={!!value} onChange={e => onChange(e.target.checked)} style={s.checkbox} />
          <span style={s.checkboxLabel}>{value ? 'Yes' : 'No'}</span>
        </label>
      </Field>
    );
  }

  if (def.type === 'select') {
    return (
      <Field label={label} required={req} error={error}>
        <select style={s.select} value={(value as string) ?? ''} onChange={e => onChange(e.target.value || null)}>
          <option value="">Select {def.label.toLowerCase()}…</option>
          {def.options?.map(opt => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </select>
      </Field>
    );
  }

  const inputType: Record<string, string> = { text: 'text', number: 'number', date: 'date', url: 'url' };

  return (
    <Field label={label} required={req} error={error}>
      <input
        style={fieldInputStyle(!!error)}
        type={inputType[def.type] || 'text'}
        value={value !== undefined && value !== null ? String(value) : ''}
        onChange={e => {
          const v = e.target.value;
          if (def.type === 'number') onChange(v === '' ? null : parseFloat(v));
          else onChange(v);
        }}
        placeholder={def.type === 'url' ? 'https://example.com' : `Enter ${def.label.toLowerCase()}`}
      />
    </Field>
  );
}

// ── Modal Shell ───────────────────────────────────────────────────────────────

function ModalShell({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  // Close on Escape
  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onClose]);

  return (
    <>
      <div className="ai-form-backdrop" onClick={onClose} />
      <div className="ai-form-modal">
        {children}
      </div>
      <style>{modalCSS}</style>
    </>
  );
}

// ── Shared sub-components ─────────────────────────────────────────────────────

function FormHeader({ icon, title, subtitle }: { icon: string; title: string; subtitle: string }) {
  return (
    <div style={s.header}>
      <div style={s.headerIconWrap}><span style={s.headerIcon}>{icon}</span></div>
      <div>
        <h3 style={s.headerTitle}>{title}</h3>
        <p style={s.headerSubtitle}>{subtitle}</p>
      </div>
    </div>
  );
}

function Field({ label, required, error, children }: { label: string; required?: boolean; error?: string; children: React.ReactNode }) {
  return (
    <div style={s.field}>
      <label style={s.label}>{label}{required && <span style={s.required}> *</span>}</label>
      {children}
      {error && <p style={s.fieldError}>{error}</p>}
    </div>
  );
}

function FormActions({ loading, onCancel, submitLabel }: { loading: boolean; onCancel: () => void; submitLabel: string }) {
  return (
    <div style={s.actions}>
      <button type="button" className="ai-form-cancel-btn" onClick={onCancel} disabled={loading}>Cancel</button>
      <button type="submit" className="ai-form-submit-btn" disabled={loading}>
        {loading ? 'Saving…' : submitLabel}
      </button>
    </div>
  );
}

function SuccessCard({ icon, message }: { icon: string; message: string }) {
  const parts = message.split(/\*\*(.*?)\*\*/g);
  return (
    <div style={s.successCard}>
      <div style={s.successIconWrap}><span style={{ fontSize: 28 }}>{icon}</span></div>
      <p style={s.successText}>
        {parts.map((p, i) => i % 2 === 1 ? <strong key={i}>{p}</strong> : p)}
      </p>
    </div>
  );
}

function fieldInputStyle(hasError: boolean): React.CSSProperties {
  return {
    ...s.input,
    borderColor: hasError ? '#ef4444' : 'var(--border, #e5e7eb)',
    boxShadow: hasError ? '0 0 0 3px rgba(239,68,68,0.1)' : undefined,
  };
}

// ── Router entry point ────────────────────────────────────────────────────────

export default function InlineForm(props: Props) {
  if (props.payload.form_type === 'deal') return <DealForm {...props} />;
  if (props.payload.form_type === 'contact') return <ContactForm {...props} />;
  return null;
}

// ── Modal CSS ─────────────────────────────────────────────────────────────────

const modalCSS = `
  .ai-form-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.4);
    backdrop-filter: blur(4px);
    z-index: 1100;
    animation: aiFadeIn 0.2s ease;
  }
  .ai-form-modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: 480px;
    max-width: calc(100vw - 32px);
    max-height: calc(100vh - 64px);
    overflow-y: auto;
    background: var(--card, #fff);
    border-radius: 20px;
    box-shadow: 0 24px 64px rgba(0,0,0,0.18), 0 0 0 1px rgba(0,0,0,0.05);
    z-index: 1101;
    padding: 28px 32px 24px;
    animation: aiSlideUp 0.25s cubic-bezier(0.16,1,0.3,1);
  }
  @keyframes aiFadeIn {
    from { opacity: 0; }
    to   { opacity: 1; }
  }
  @keyframes aiSlideUp {
    from { opacity: 0; transform: translate(-50%, -46%); }
    to   { opacity: 1; transform: translate(-50%, -50%); }
  }
  .ai-form-cancel-btn {
    padding: 10px 20px;
    border-radius: 10px;
    border: 1px solid var(--border, #e5e7eb);
    background: transparent;
    cursor: pointer;
    font-size: 13px;
    font-weight: 600;
    color: var(--muted-foreground, #6b7280);
    transition: all 0.15s;
  }
  .ai-form-cancel-btn:hover {
    background: var(--background, #f9fafb);
    border-color: var(--foreground, #374151);
    color: var(--foreground, #374151);
  }
  .ai-form-submit-btn {
    padding: 10px 24px;
    border-radius: 10px;
    border: none;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    color: #fff;
    cursor: pointer;
    font-size: 13px;
    font-weight: 700;
    transition: all 0.15s;
    box-shadow: 0 2px 8px rgba(245,158,11,0.3);
  }
  .ai-form-submit-btn:hover {
    transform: translateY(-1px);
    box-shadow: 0 4px 16px rgba(245,158,11,0.4);
  }
  .ai-form-submit-btn:disabled {
    opacity: 0.7;
    cursor: not-allowed;
    transform: none;
  }
  .ai-form-modal input:focus,
  .ai-form-modal select:focus {
    border-color: #f59e0b !important;
    box-shadow: 0 0 0 3px rgba(245,158,11,0.12) !important;
    outline: none;
  }
`;

// ── Inline styles ─────────────────────────────────────────────────────────────

const s: Record<string, React.CSSProperties> = {
  header: {
    display: 'flex',
    alignItems: 'center',
    gap: 14,
    marginBottom: 24,
    paddingBottom: 20,
    borderBottom: '1px solid var(--border, #e5e7eb)',
  },
  headerIconWrap: {
    width: 44,
    height: 44,
    borderRadius: 12,
    background: 'linear-gradient(135deg, rgba(245,158,11,0.12), rgba(239,68,68,0.08))',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
  },
  headerIcon: { fontSize: 22 },
  headerTitle: { margin: 0, fontSize: 18, fontWeight: 700, color: 'var(--foreground, #111)' },
  headerSubtitle: { margin: '2px 0 0', fontSize: 13, color: 'var(--muted-foreground, #6b7280)' },
  grid2: {
    display: 'grid',
    gridTemplateColumns: '1fr 1fr',
    gap: '0 16px',
  },
  field: { marginBottom: 16 },
  label: {
    display: 'block',
    fontSize: 12,
    fontWeight: 600,
    color: 'var(--foreground, #374151)',
    marginBottom: 6,
  },
  required: { color: '#ef4444' },
  input: {
    width: '100%',
    border: '1.5px solid var(--border, #e5e7eb)',
    borderRadius: 10,
    padding: '10px 14px',
    fontSize: 14,
    background: 'var(--background, #f9fafb)',
    color: 'var(--foreground, #111)',
    boxSizing: 'border-box' as const,
    outline: 'none',
    transition: 'border-color 0.15s, box-shadow 0.15s',
    fontFamily: 'inherit',
  },
  select: {
    width: '100%',
    border: '1.5px solid var(--border, #e5e7eb)',
    borderRadius: 10,
    padding: '10px 14px',
    fontSize: 14,
    background: 'var(--background, #f9fafb)',
    color: 'var(--foreground, #111)',
    boxSizing: 'border-box' as const,
    outline: 'none',
    cursor: 'pointer',
    fontFamily: 'inherit',
  },
  fieldError: { color: '#ef4444', fontSize: 11, margin: '4px 0 0', fontWeight: 500 },
  formError: {
    color: '#ef4444',
    fontSize: 13,
    margin: '8px 0',
    padding: '10px 14px',
    background: 'rgba(239,68,68,0.06)',
    borderRadius: 10,
    border: '1px solid rgba(239,68,68,0.15)',
  },
  sectionDivider: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    margin: '20px 0 16px',
  },
  dividerLine: { flex: 1, height: 1, background: 'var(--border, #e5e7eb)' },
  dividerLabel: {
    fontSize: 10,
    fontWeight: 700,
    color: 'var(--muted-foreground, #9ca3af)',
    textTransform: 'uppercase' as const,
    letterSpacing: '0.1em',
  },
  actions: {
    display: 'flex',
    gap: 12,
    justifyContent: 'flex-end',
    marginTop: 24,
    paddingTop: 20,
    borderTop: '1px solid var(--border, #e5e7eb)',
  },
  successCard: {
    textAlign: 'center' as const,
    padding: '32px 24px',
  },
  successIconWrap: {
    width: 64,
    height: 64,
    borderRadius: 20,
    background: 'linear-gradient(135deg, rgba(34,197,94,0.12), rgba(16,185,129,0.08))',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    margin: '0 auto 16px',
  },
  successText: { margin: 0, fontSize: 15, color: 'var(--foreground)', lineHeight: 1.6 },
  checkboxRow: { display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', padding: '6px 0' },
  checkbox: { width: 18, height: 18, cursor: 'pointer', accentColor: '#f59e0b' },
  checkboxLabel: { fontSize: 14, color: 'var(--foreground, #374151)' },
};
