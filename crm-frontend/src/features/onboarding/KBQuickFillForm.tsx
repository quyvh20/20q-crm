import { useState } from 'react';
import { upsertKBSection } from '../../lib/api';
import { Button } from '../../components/ui/button';

// The knowledge-base quick fill (U7.5) — the useful half of the retired welcome
// wizard, kept alive.
//
// It is a 60-second way to give the AI assistant the three things it otherwise
// guesses at: who you are, what you sell, and how you talk. Writing that by hand in
// Settings → Knowledge Base means facing an empty markdown editor, which nobody
// does. So the form survives the wizard's removal — it just isn't compulsory, and
// isn't the first thing a new user is trapped behind. It now renders as the BODY of
// the shared <Modal> (no gradient banner of its own, no page reload on save).

interface KBQuickFillFormProps {
  /** Tailors the placeholders to the template the user just deployed. */
  templateId?: string | null;
  onSaved: () => void;
  onSkip: () => void;
  skipLabel?: string;
}

// Keyed on the SERVER template slugs (/api/templates), not the two ids the old
// hardcoded modal used ('real-estate', 'saas') — those matched nothing once the
// catalog moved to the backend, so every template silently fell through to
// `default`. Templates without an entry here still fall through by design; the
// default copy is deliberately generic rather than wrong.
const PLACEHOLDERS = {
  real_estate: {
    company: 'e.g. Prestige Realty',
    industry: 'e.g. Real Estate Brokerage',
    usp: 'e.g. Top 1% in sales volume, exclusive luxury listings',
    products: 'e.g. - Buyer representation: 3% commission\n- Listing services: 2.5% commission, full marketing suite',
    objection: 'e.g. "Commission is too high" → We sell homes 14 days faster on average and for 2% more money.',
  },
  b2b_saas: {
    company: 'e.g. CloudFlow SaaS',
    industry: 'e.g. B2B SaaS',
    usp: 'e.g. 10x cheaper than the incumbent, AI-native',
    products: 'e.g. - Starter: $29/mo — 3 users\n- Pro: $79/mo — unlimited users, AI features',
    objection: 'e.g. "Too expensive" → Our ROI pays back in 3 months. Annual plan saves 20%.',
  },
  default: {
    company: 'e.g. Acme Corp',
    industry: 'e.g. B2B SaaS, Professional Services',
    usp: 'e.g. 10x faster execution, amazing support',
    products: 'e.g. - Basic Tier: $500\n- Enterprise: $2000 custom setup',
    objection: 'e.g. "Too expensive" → Our ROI pays back in 3 months.',
  },
};

const TONES = [
  { value: 'professional', label: 'Professional' },
  { value: 'friendly', label: 'Friendly' },
  { value: 'bold', label: 'Bold & direct' },
  { value: 'consultative', label: 'Consultative' },
];

const inputClass =
  'w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring';

export default function KBQuickFillForm({ templateId, onSaved, onSkip, skipLabel = 'Skip for now' }: KBQuickFillFormProps) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({
    companyName: '',
    industry: '',
    teamSize: '',
    products: '',
    tone: 'professional',
    topObjection: '',
    usp: '',
  });

  const ph = PLACEHOLDERS[templateId as keyof typeof PLACEHOLDERS] ?? PLACEHOLDERS.default;
  const set = (k: keyof typeof form, v: string) => setForm((f) => ({ ...f, [k]: v }));

  const handleSave = async () => {
    if (!form.companyName.trim()) {
      setError('Company name is required.');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const companyMd = [
        `## ${form.companyName}`,
        form.industry && `**Industry:** ${form.industry}`,
        form.teamSize && `**Team size:** ${form.teamSize}`,
        form.usp && `\n**Unique Value Proposition:** ${form.usp}`,
      ]
        .filter(Boolean)
        .join('\n');

      const playbookLines = ['## Sales Playbook', `**Tone:** ${form.tone}.`];
      if (form.products) playbookLines.push(`\n**Key Products & Pricing:**\n${form.products}`);
      if (form.topObjection) playbookLines.push(`\n**Top Objection & Response:**\n${form.topObjection}`);

      await Promise.all([
        upsertKBSection('company', { title: 'Company', content: companyMd }),
        upsertKBSection('playbook', { title: 'Sales Playbook', content: playbookLines.join('\n') }),
      ]);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed');
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        Tell the AI assistant about your business and it can answer with your products, your pricing and your
        voice from day one. You can edit all of this later in Settings → Knowledge Base.
      </p>

      {error && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
      )}

      <div>
        <label htmlFor="kb-company-name" className="mb-1 block text-sm font-medium text-foreground">
          Company name <span className="text-destructive">*</span>
        </label>
        <input
          id="kb-company-name"
          value={form.companyName}
          onChange={(e) => set('companyName', e.target.value)}
          placeholder={ph.company}
          className={inputClass}
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div>
          <label htmlFor="kb-industry" className="mb-1 block text-sm font-medium text-foreground">Industry</label>
          <input
            id="kb-industry"
            value={form.industry}
            onChange={(e) => set('industry', e.target.value)}
            placeholder={ph.industry}
            className={inputClass}
          />
        </div>
        <div>
          <label htmlFor="kb-team-size" className="mb-1 block text-sm font-medium text-foreground">Team size</label>
          <select
            id="kb-team-size"
            value={form.teamSize}
            onChange={(e) => set('teamSize', e.target.value)}
            className={inputClass}
          >
            <option value="">Select…</option>
            <option>1–10</option>
            <option>11–50</option>
            <option>51–200</option>
            <option>200+</option>
          </select>
        </div>
      </div>

      <div>
        <label htmlFor="kb-usp" className="mb-1 block text-sm font-medium text-foreground">What makes you different?</label>
        <input
          id="kb-usp"
          value={form.usp}
          onChange={(e) => set('usp', e.target.value)}
          placeholder={ph.usp}
          className={inputClass}
        />
      </div>

      <div>
        <label htmlFor="kb-products" className="mb-1 block text-sm font-medium text-foreground">Key products / services</label>
        <textarea
          id="kb-products"
          value={form.products}
          onChange={(e) => set('products', e.target.value)}
          placeholder={ph.products}
          rows={3}
          className={`${inputClass} resize-none`}
        />
      </div>

      <fieldset>
        <legend className="mb-2 text-sm font-medium text-foreground">Sales communication tone</legend>
        <div className="grid grid-cols-2 gap-2">
          {TONES.map((t) => (
            <button
              key={t.value}
              type="button"
              aria-pressed={form.tone === t.value}
              onClick={() => set('tone', t.value)}
              className={`rounded-lg border px-3 py-2 text-left text-sm font-medium transition-colors ${
                form.tone === t.value
                  ? 'border-primary bg-primary/10 text-foreground'
                  : 'border-border text-muted-foreground hover:bg-accent hover:text-foreground'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </fieldset>

      <div>
        <label htmlFor="kb-objection" className="mb-1 block text-sm font-medium text-foreground">
          Top objection you hear — and your response
          <span className="ml-1 font-normal text-muted-foreground">(optional)</span>
        </label>
        <textarea
          id="kb-objection"
          value={form.topObjection}
          onChange={(e) => set('topObjection', e.target.value)}
          placeholder={ph.objection}
          rows={2}
          className={`${inputClass} resize-none`}
        />
      </div>

      <div className="flex items-center justify-between gap-3 border-t border-border pt-4">
        <Button variant="ghost" size="sm" onClick={onSkip} disabled={saving} className="text-muted-foreground hover:text-foreground">
          {skipLabel}
        </Button>
        <Button onClick={handleSave} disabled={saving || !form.companyName.trim()}>
          {saving ? 'Saving…' : 'Save to knowledge base'}
        </Button>
      </div>
    </div>
  );
}
