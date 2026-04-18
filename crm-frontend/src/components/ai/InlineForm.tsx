import React, { useState, useEffect } from 'react';
import type { FormPayload } from './chatTypes';
import { createContact, createDeal, getStages, type PipelineStage } from '../../lib/api';

interface Props {
  payload: FormPayload;
  onSuccess: (message: string) => void;
  onCancel: () => void;
}

// ── Contact Form ──────────────────────────────────────────────────────────────

function ContactForm({ payload, onSuccess, onCancel }: Props) {
  const [name, setName] = useState(payload.prefill_name || '');
  const [email, setEmail] = useState(payload.prefill_email || '');
  const [phone, setPhone] = useState('');
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!name.trim()) e.name = 'Full name is required';
    if (email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) e.email = 'Enter a valid email address';
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
      await createContact({
        first_name: first,
        last_name: rest.join(' ') || '',
        email: email || undefined,
        phone: phone || undefined,
      });
      setDone(true);
      setTimeout(() => onSuccess(`✅ Contact **${name}** created successfully!`), 900);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : 'Failed to create contact' });
    } finally {
      setLoading(false);
    }
  };

  if (done) return <SuccessCard icon="👤" message={`Contact **${name}** created!`} />;

  return (
    <form onSubmit={submit} style={styles.wrapper} noValidate>
      <FormHeader icon="👤" title="New Contact" />

      <Field label="Full Name *" error={errors.name}>
        <input style={fieldInputStyle(!!errors.name)} value={name}
          onChange={e => { setName(e.target.value); setErrors(p => ({ ...p, name: '' })); }}
          placeholder="Jane Smith" autoFocus />
      </Field>
      <Field label="Email" error={errors.email}>
        <input style={fieldInputStyle(!!errors.email)} type="email" value={email}
          onChange={e => { setEmail(e.target.value); setErrors(p => ({ ...p, email: '' })); }}
          placeholder="jane@example.com" />
      </Field>
      <Field label="Phone">
        <input style={fieldInputStyle(false)} value={phone}
          onChange={e => setPhone(e.target.value)} placeholder="+1 555 000 0000" />
      </Field>

      {errors._form && <p style={styles.formError}>{errors._form}</p>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Contact" />
    </form>
  );
}

// ── Deal Form ─────────────────────────────────────────────────────────────────

function DealForm({ payload, onSuccess, onCancel }: Props) {
  const [title, setTitle] = useState(payload.prefill_title || '');
  const [value, setValue] = useState(payload.prefill_value != null ? String(payload.prefill_value) : '');
  const [probability, setProbability] = useState('20');
  const [stageId, setStageId] = useState('');
  const [stages, setStages] = useState<PipelineStage[]>([]);
  const [loading, setLoading] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [done, setDone] = useState(false);

  // Load pipeline stages on mount
  useEffect(() => {
    getStages()
      .then(s => {
        setStages(s);
        if (s.length > 0) setStageId(s[0].id);
      })
      .catch(() => { /* silently skip if stages unavailable */ });
  }, []);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!title.trim()) e.title = 'Deal title is required';
    if (value && isNaN(Number(value))) e.value = 'Value must be a number';
    if (value && Number(value) < 0) e.value = 'Value cannot be negative';
    const prob = Number(probability);
    if (probability && (isNaN(prob) || prob < 0 || prob > 100)) e.probability = 'Probability must be 0–100';
    return e;
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const errs = validate();
    if (Object.keys(errs).length > 0) { setErrors(errs); return; }
    setLoading(true);
    setErrors({});
    try {
      await createDeal({
        title: title.trim(),
        value:       value       ? Number(value)       : undefined,
        stage_id:    stageId     ? stageId             : undefined,
        probability: probability ? Number(probability) : undefined,
      });
      setDone(true);
      const valueStr = value ? ` ($${Number(value).toLocaleString()})` : '';
      setTimeout(() => onSuccess(`✅ Deal **${title}**${valueStr} created successfully!`), 900);
    } catch (err) {
      setErrors({ _form: err instanceof Error ? err.message : 'Failed to create deal' });
    } finally {
      setLoading(false);
    }
  };

  if (done) return <SuccessCard icon="💼" message={`Deal **${title}** created!`} />;

  return (
    <form onSubmit={submit} style={styles.wrapper} noValidate>
      <FormHeader icon="💼" title="New Deal" />

      <Field label="Deal Title *" error={errors.title}>
        <input style={fieldInputStyle(!!errors.title)} value={title}
          onChange={e => { setTitle(e.target.value); setErrors(p => ({ ...p, title: '' })); }}
          placeholder="e.g. Acme Corp - Enterprise Plan" autoFocus />
      </Field>
      <Field label="Value ($)" error={errors.value}>
        <input style={fieldInputStyle(!!errors.value)} type="number" min="0" value={value}
          onChange={e => { setValue(e.target.value); setErrors(p => ({ ...p, value: '' })); }}
          placeholder="0" />
      </Field>

      {stages.length > 0 && (
        <Field label="Pipeline Stage">
          <select style={styles.select} value={stageId} onChange={e => setStageId(e.target.value)}>
            {stages.map(s => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
        </Field>
      )}

      <Field label="Win Probability (%)" error={errors.probability}>
        <input style={fieldInputStyle(!!errors.probability)} type="number" min="0" max="100"
          value={probability}
          onChange={e => { setProbability(e.target.value); setErrors(p => ({ ...p, probability: '' })); }}
          placeholder="20" />
      </Field>

      {errors._form && <p style={styles.formError}>{errors._form}</p>}
      <FormActions loading={loading} onCancel={onCancel} submitLabel="Create Deal" />
    </form>
  );
}

// ── Shared sub-components ─────────────────────────────────────────────────────

function FormHeader({ icon, title }: { icon: string; title: string }) {
  return (
    <div style={styles.header}>
      <span style={styles.headerIcon}>{icon}</span>
      <span style={styles.headerTitle}>{title}</span>
    </div>
  );
}

function Field({ label, error, children }: { label: string; error?: string; children: React.ReactNode }) {
  return (
    <div style={styles.field}>
      <label style={styles.label}>{label}</label>
      {children}
      {error && <p style={styles.fieldError}>{error}</p>}
    </div>
  );
}

function FormActions({ loading, onCancel, submitLabel }: { loading: boolean; onCancel: () => void; submitLabel: string }) {
  return (
    <div style={styles.actions}>
      <button type="button" style={styles.cancelBtn} onClick={onCancel} disabled={loading}>Cancel</button>
      <button type="submit" style={{ ...styles.submitBtn, opacity: loading ? 0.7 : 1 }} disabled={loading}>
        {loading ? 'Saving…' : submitLabel}
      </button>
    </div>
  );
}

function SuccessCard({ icon, message }: { icon: string; message: string }) {
  // Parse **bold** inline
  const parts = message.split(/\*\*(.*?)\*\*/g);
  return (
    <div style={styles.successCard}>
      <span style={styles.successIcon}>{icon}</span>
      <p style={styles.successText}>
        {parts.map((p, i) => i % 2 === 1 ? <strong key={i}>{p}</strong> : p)}
      </p>
    </div>
  );
}

function fieldInputStyle(hasError: boolean): React.CSSProperties {
  return {
    ...styles.input,
    borderColor: hasError ? '#dc2626' : 'var(--border)',
    boxShadow: hasError ? '0 0 0 2px rgba(220,38,38,0.12)' : undefined,
  };
}

// ── Router entry point ────────────────────────────────────────────────────────

export default function InlineForm(props: Props) {
  if (props.payload.form_type === 'deal') return <DealForm {...props} />;
  if (props.payload.form_type === 'contact') return <ContactForm {...props} />;
  return null;
}

// ── Styles ────────────────────────────────────────────────────────────────────

const styles: Record<string, React.CSSProperties> = {
  wrapper: {
    border: '1px solid var(--border)',
    borderRadius: 14,
    padding: '14px 16px',
    marginBottom: 6,
    background: 'var(--card)',
    animation: 'fadeSlide 0.2s ease',
    boxShadow: '0 2px 12px rgba(0,0,0,0.06)',
  },
  header: { display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 },
  headerIcon: { fontSize: 16 },
  headerTitle: { fontWeight: 700, fontSize: 14, color: 'var(--foreground)' },
  field: { marginBottom: 10 },
  label: {
    display: 'block',
    fontSize: 10,
    fontWeight: 700,
    color: 'var(--muted-foreground)',
    marginBottom: 3,
    textTransform: 'uppercase',
    letterSpacing: '0.06em',
  },
  input: {
    width: '100%',
    border: '1px solid var(--border)',
    borderRadius: 8,
    padding: '7px 10px',
    fontSize: 13,
    background: 'var(--background)',
    color: 'var(--foreground)',
    boxSizing: 'border-box',
    outline: 'none',
    transition: 'border-color 0.15s, box-shadow 0.15s',
    fontFamily: 'inherit',
  },
  select: {
    width: '100%',
    border: '1px solid var(--border)',
    borderRadius: 8,
    padding: '7px 10px',
    fontSize: 13,
    background: 'var(--background)',
    color: 'var(--foreground)',
    boxSizing: 'border-box',
    outline: 'none',
    cursor: 'pointer',
    fontFamily: 'inherit',
  },
  fieldError: { color: '#dc2626', fontSize: 11, margin: '3px 0 0', fontWeight: 500 },
  formError: {
    color: '#dc2626',
    fontSize: 12,
    margin: '6px 0 4px',
    padding: '6px 10px',
    background: 'rgba(220,38,38,0.06)',
    borderRadius: 6,
    border: '1px solid rgba(220,38,38,0.2)',
  },
  actions: { display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 },
  cancelBtn: {
    padding: '6px 14px', borderRadius: 8,
    border: '1px solid var(--border)', background: 'transparent',
    cursor: 'pointer', fontSize: 12, color: 'var(--muted-foreground)',
  },
  submitBtn: {
    padding: '6px 16px', borderRadius: 8, border: 'none',
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    color: '#fff', cursor: 'pointer', fontSize: 12, fontWeight: 700,
    transition: 'opacity 0.15s',
  },
  successCard: {
    border: '1px solid #22c55e',
    borderRadius: 12,
    padding: '12px 14px',
    background: 'linear-gradient(135deg, rgba(34,197,94,0.08), rgba(16,185,129,0.04))',
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    animation: 'fadeSlide 0.25s ease',
  },
  successIcon: { fontSize: 20 },
  successText: { margin: 0, fontSize: 13, color: 'var(--foreground)', lineHeight: 1.5 },
};
