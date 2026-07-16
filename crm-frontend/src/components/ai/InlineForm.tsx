import React, { useState, useEffect, useRef } from 'react';
import { Briefcase, User } from 'lucide-react';
import type { FormPayload } from './chatTypes';
import {
  createContact, createDeal, getStages, getFieldDefs, getContacts,
  getObjectDef, createObjectRecord,
  type PipelineStage, type CustomFieldDef, type Contact, type CustomObjectDef,
} from '../../lib/api';
import { Button } from '../ui/button';
import { Spinner } from '../ui/spinner';

interface Props {
  payload: FormPayload;
  onSuccess: (message: string) => void;
  onCancel: () => void;
}

// ── Shared presentation (token classes; `.ai-inline-form` stays as a marker
//    class so host pages can constrain the form's width) ───────────────────────

const formClass = 'ai-inline-form mb-3 mt-1 w-full overflow-hidden rounded-xl border border-border bg-card p-4 shadow-sm';
const inputClass =
  'w-full rounded-lg border border-input bg-background px-3 py-2 text-[13px] text-foreground outline-none transition-colors placeholder:text-muted-foreground focus:border-ring focus:ring-2 focus:ring-ring/20 data-[err="1"]:border-destructive data-[err="1"]:ring-2 data-[err="1"]:ring-destructive/10';
const selectClass = `${inputClass} cursor-pointer`;

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

  if (done) return <SuccessCard icon={<User aria-hidden className="h-[18px] w-[18px]" />} message={`Contact **${name}** created!`} />;

  return (
    <form onSubmit={submit} className={formClass} noValidate>
      <FormHeader icon={<User aria-hidden className="h-4 w-4 text-primary" />} title="New Contact" />
      <Field label="Full Name" required error={errors.name}>
        <input className={inputClass} data-err={errors.name ? '1' : ''} value={name}
          onChange={e => { setName(e.target.value); setErrors(p => ({ ...p, name: '' })); }}
          placeholder="Jane Smith" autoFocus />
      </Field>
      <Field label="Email" error={errors.email}>
        <input className={inputClass} data-err={errors.email ? '1' : ''} type="email" value={email}
          onChange={e => { setEmail(e.target.value); setErrors(p => ({ ...p, email: '' })); }}
          placeholder="jane@example.com" />
      </Field>
      <Field label="Phone">
        <input className={inputClass} value={phone}
          onChange={e => setPhone(e.target.value)} placeholder="+1 555 000 0000" />
      </Field>
      {fieldDefs.length > 0 && (
        <>
          <FormDivider>Custom Fields</FormDivider>
          {fieldDefs.map(def => (
            <CustomFieldInput key={def.key} def={def} value={customFields[def.key]}
              error={errors[`cf_${def.key}`]}
              onChange={v => setCustomFields(prev => ({ ...prev, [def.key]: v }))} />
          ))}
        </>
      )}
      {errors._form && <FormError>{errors._form}</FormError>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Contact" />
      <div ref={formEndRef} />
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

  if (done) return <SuccessCard icon={<Briefcase aria-hidden className="h-[18px] w-[18px]" />} message={`Deal **${title}** created!`} />;

  return (
    <form onSubmit={submit} className={formClass} noValidate>
      <FormHeader icon={<Briefcase aria-hidden className="h-4 w-4 text-primary" />} title="New Deal" />
      <Field label="Deal Title" required error={errors.title}>
        <input className={inputClass} data-err={errors.title ? '1' : ''} value={title}
          onChange={e => { setTitle(e.target.value); setErrors(p => ({ ...p, title: '' })); }}
          placeholder="e.g. Acme Corp - Enterprise" autoFocus />
      </Field>
      <div className="flex gap-2.5 [&>div]:min-w-0 [&>div]:flex-1">
        <Field label="Value ($)" error={errors.value}>
          <input className={inputClass} data-err={errors.value ? '1' : ''} type="number" min="0" value={value}
            onChange={e => { setValue(e.target.value); setErrors(p => ({ ...p, value: '' })); }} placeholder="0" />
        </Field>
        <Field label="Probability (%)" error={errors.probability}>
          <input className={inputClass} data-err={errors.probability ? '1' : ''} type="number" min="0" max="100"
            value={probability}
            onChange={e => { setProbability(e.target.value); setErrors(p => ({ ...p, probability: '' })); }} placeholder="20" />
        </Field>
      </div>
      <Field label="Contact">
        <input className={inputClass} value={contactSearch}
          onChange={e => { setContactSearch(e.target.value); setContactId(''); }} placeholder="Search…" />
        {contacts.length > 0 && (
          <select className={`${selectClass} mt-1.5`} value={contactId}
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
          <select className={selectClass} value={stageId} onChange={e => setStageId(e.target.value)}>
            {stages.map(st => <option key={st.id} value={st.id}>{st.name}</option>)}
          </select>
        </Field>
      )}
      {fieldDefs.length > 0 && (
        <>
          <FormDivider>Custom Fields</FormDivider>
          {fieldDefs.map(def => (
            <CustomFieldInput key={def.key} def={def} value={customFields[def.key]}
              error={errors[`cf_${def.key}`]}
              onChange={v => setCustomFields(prev => ({ ...prev, [def.key]: v }))} />
          ))}
        </>
      )}
      {errors._form && <FormError>{errors._form}</FormError>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Deal" />
      <div ref={formEndRef} />
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
        <label className="flex cursor-pointer items-center gap-2 py-1">
          <input type="checkbox" className="h-4 w-4 cursor-pointer accent-primary" checked={!!value} onChange={e => onChange(e.target.checked)} />
          <span className="text-[13px] text-foreground">{value ? 'Yes' : 'No'}</span>
        </label>
      </Field>
    );
  }
  if (def.type === 'select') {
    return (
      <Field label={def.label} required={def.required} error={error}>
        <select className={selectClass} value={(value as string) ?? ''} onChange={e => onChange(e.target.value || null)}>
          <option value="">Select…</option>
          {def.options?.map(opt => <option key={opt} value={opt}>{opt}</option>)}
        </select>
      </Field>
    );
  }
  const typeMap: Record<string, string> = { text: 'text', number: 'number', date: 'date', url: 'url' };
  return (
    <Field label={def.label} required={def.required} error={error}>
      <input className={inputClass} data-err={error ? '1' : ''}
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

function FormHeader({ icon, title }: { icon: React.ReactNode; title: string }) {
  return (
    <div className="mb-3.5 flex items-center gap-2.5 border-b border-border pb-3">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-base">{icon}</span>
      <span className="text-[15px] font-bold text-foreground">{title}</span>
    </div>
  );
}

function Field({ label, required, error, children }: {
  label: string; required?: boolean; error?: string; children: React.ReactNode;
}) {
  return (
    <div className="mb-3">
      <label className="mb-1 block text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}{required && <span className="text-destructive"> *</span>}
      </label>
      {children}
      {error && <p className="mt-1 text-[11px] font-medium text-destructive">{error}</p>}
    </div>
  );
}

function FormDivider({ children }: { children: React.ReactNode }) {
  return (
    <div className="my-3.5 flex items-center gap-2 before:h-px before:flex-1 before:bg-border after:h-px after:flex-1 after:bg-border">
      <span className="text-[9px] font-bold uppercase tracking-widest text-muted-foreground">{children}</span>
    </div>
  );
}

function FormError({ children }: { children: React.ReactNode }) {
  return (
    <p className="my-1.5 rounded-lg border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">{children}</p>
  );
}

function FormActions({ loading, onCancel, submitLabel }: {
  loading: boolean; onCancel: () => void; submitLabel: string;
}) {
  return (
    <div className="mt-3.5 flex justify-end gap-2 border-t border-border pt-3">
      <Button type="button" variant="outline" size="sm" onClick={onCancel} disabled={loading}>Cancel</Button>
      <Button type="submit" size="sm" disabled={loading}>
        {loading ? 'Saving…' : submitLabel}
      </Button>
    </div>
  );
}

function SuccessCard({ icon, message }: { icon: React.ReactNode; message: string }) {
  const parts = message.split(/\*\*(.*?)\*\*/g);
  return (
    <div className="mb-2 mt-1 flex items-center gap-2.5 rounded-xl border border-emerald-500/40 bg-emerald-500/10 px-4 py-3">
      <span className="shrink-0 text-emerald-600 dark:text-emerald-400">{icon}</span>
      <p className="m-0 text-[13px] leading-normal text-foreground">
        {parts.map((p, i) => i % 2 === 1 ? <strong key={i}>{p}</strong> : p)}
      </p>
    </div>
  );
}

// ── Custom Object Form (dynamic, schema-driven) ──────────────────────────────

function CustomObjectForm({ payload, onSuccess, onCancel }: Props) {
  const slug = payload.object_slug || '';
  const [objectDef, setObjectDef] = useState<CustomObjectDef | null>(null);
  const [displayName, setDisplayName] = useState(payload.prefill_display_name || '');
  const [fieldValues, setFieldValues] = useState<Record<string, unknown>>(payload.prefill_fields || {});
  const [loading, setLoading] = useState(false);
  const [loadingSchema, setLoadingSchema] = useState(true);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);
  const formEndRef = useRef<HTMLDivElement>(null);

  // Fetch the object schema on mount
  useEffect(() => {
    if (!slug) {
      setLoadingSchema(false);
      return;
    }
    getObjectDef(slug)
      .then(def => {
        setObjectDef(def);
        // Auto-populate display_name from schema's first text field if not prefilled
        if (!displayName && payload.prefill_fields) {
          const firstTextField = def.fields?.find(f => f.type === 'text');
          if (firstTextField && payload.prefill_fields[firstTextField.key]) {
            setDisplayName(String(payload.prefill_fields[firstTextField.key]));
          }
        }
      })
      .catch(() => {})
      .finally(() => setLoadingSchema(false));
  }, [slug]);

  // Scroll form buttons into view when schema loads
  useEffect(() => {
    if (!loadingSchema) {
      setTimeout(() => formEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' }), 150);
    }
  }, [loadingSchema]);

  const fields = objectDef?.fields || [];
  // The registry stores each object's icon as an emoji string — that's data, so
  // it renders as text here (unlike the hardcoded contact/deal icons above).
  const icon = objectDef?.icon || '📦';
  const label = objectDef?.label || slug;

  const validate = () => {
    const e: Record<string, string> = {};
    if (!displayName.trim()) e.display_name = 'Name is required';
    for (const def of fields) {
      if (def.required && (fieldValues[def.key] === undefined || fieldValues[def.key] === null || fieldValues[def.key] === '')) {
        e[`f_${def.key}`] = `${def.label} is required`;
      }
    }
    return e;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) { setErrors(errs); return; }
    setLoading(true); setErrors({});
    try {
      // Build data payload: include display_name as a field + all form field values
      const data: Record<string, unknown> = { display_name: displayName.trim() };
      for (const def of fields) {
        if (fieldValues[def.key] !== undefined && fieldValues[def.key] !== null && fieldValues[def.key] !== '') {
          data[def.key] = fieldValues[def.key];
        }
      }
      const result = await createObjectRecord(slug, { data });
      setDone(true);
      const idRef = result?.id ? ` (id: ${result.id})` : '';
      setTimeout(() => onSuccess(`✅ ${label} **${displayName}**${idRef} created successfully!`), 800);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : `Failed to create ${label}` });
    } finally { setLoading(false); }
  };

  if (done) return <SuccessCard icon={<span className="text-lg">{icon}</span>} message={`${label} **${displayName}** created!`} />;

  // Loading skeleton
  if (loadingSchema) {
    return (
      <div className={`${formClass} flex justify-center px-4 py-6 text-center`}>
        <Spinner label={`Loading ${slug} schema…`} />
      </div>
    );
  }

  return (
    <form onSubmit={submit} className={formClass} noValidate>
      <FormHeader icon={<span className="text-base">{icon}</span>} title={`New ${label}`} />
      <Field label="Display Name" required error={errors.display_name}>
        <input className={inputClass} data-err={errors.display_name ? '1' : ''} value={displayName}
          onChange={e => { setDisplayName(e.target.value); setErrors(p => ({ ...p, display_name: '' })); }}
          placeholder={`Enter ${label.toLowerCase()} name`} autoFocus />
      </Field>
      {fields.length > 0 && (
        <>
          <FormDivider>Fields</FormDivider>
          {fields.map(def => (
            <CustomFieldInput key={def.key} def={def} value={fieldValues[def.key]}
              error={errors[`f_${def.key}`]}
              onChange={v => {
                setFieldValues(prev => ({ ...prev, [def.key]: v }));
                setErrors(prev => ({ ...prev, [`f_${def.key}`]: '' }));
              }} />
          ))}
        </>
      )}
      {errors._form && <FormError>{errors._form}</FormError>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel={`Create ${label}`} />
      <div ref={formEndRef} />
    </form>
  );
}

// ── Router entry point ────────────────────────────────────────────────────────

export default function InlineForm(props: Props) {
  if (props.payload.form_type === 'deal') return <DealForm {...props} />;
  if (props.payload.form_type === 'contact') return <ContactForm {...props} />;
  if (props.payload.form_type === 'custom_object') return <CustomObjectForm {...props} />;
  return null;
}
