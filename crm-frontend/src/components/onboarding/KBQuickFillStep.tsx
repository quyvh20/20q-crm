import { useState } from 'react';
import { upsertKBSection } from '../../lib/api';

interface KBQuickFillStepProps {
  templateId?: string | null;
  onComplete: () => void;
  onSkip: () => void;
}

export default function KBQuickFillStep({ templateId, onComplete, onSkip }: KBQuickFillStepProps) {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [form, setForm] = useState({
    companyName: '',
    industry: '',
    teamSize: '',
    products: '',
    pricing: '',
    tone: 'professional',
    topObjection: '',
    usp: '',
  });

  const placeholders = {
    'real-estate': {
      company: 'e.g. Prestige Realty',
      industry: 'e.g. Real Estate Brokerage',
      usp: 'e.g. Top 1% in sales volume, exclusive luxury listings',
      products: 'e.g. - Buyer representation: 3% commission\n- Listing services: 2.5% commission, full marketing suite',
      objection: 'e.g. "Commission is too high" → We sell homes 14 days faster on average and for 2% more money.',
    },
    'saas': {
      company: 'e.g. CloudFlow SaaS',
      industry: 'e.g. B2B SaaS',
      usp: 'e.g. 10x cheaper than Salesforce, AI-native',
      products: 'e.g. - Starter: $29/mo — 3 users\n- Pro: $79/mo — unlimited users, AI features',
      objection: `e.g. "Too expensive" → Our ROI pays back in 3 months. Annual plan saves 20%.`,
    },
    'default': {
      company: 'e.g. Acme Corp',
      industry: 'e.g. B2B SaaS, Professional Services',
      usp: 'e.g. 10x faster execution, amazing support',
      products: 'e.g. - Basic Tier: $500\n- Enterprise: $2000 custom setup',
      objection: `e.g. "Too expensive" → Our ROI pays back in 3 months.`,
    }
  };

  const currentPlaceholders = placeholders[templateId as keyof typeof placeholders] || placeholders.default;

  const set = (k: keyof typeof form, v: string) => setForm(f => ({ ...f, [k]: v }));

  const handleSave = async () => {
    if (!form.companyName.trim()) {
      setError('Company name is required.');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      // Build company section markdown
      const companyMd = [
        `## ${form.companyName}`,
        form.industry && `**Industry:** ${form.industry}`,
        form.teamSize && `**Team size:** ${form.teamSize}`,
        form.usp && `\n**Unique Value Proposition:** ${form.usp}`,
      ].filter(Boolean).join('\n');

      // Build playbook section markdown
      const playbookLines = [`## Sales Playbook`, `**Tone:** ${form.tone}.`];
      if (form.products) playbookLines.push(`\n**Key Products & Pricing:**\n${form.products}${form.pricing ? `\n${form.pricing}` : ''}`);
      if (form.topObjection) playbookLines.push(`\n**Top Objection & Response:**\n${form.topObjection}`);
      const playbookMd = playbookLines.join('\n');

      await Promise.all([
        upsertKBSection('company', { title: 'Company', content: companyMd }),
        upsertKBSection('playbook', { title: 'Sales Playbook', content: playbookMd }),
      ]);

      onComplete();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Save failed');
      setSaving(false);
    }
  };

  const tones = [
    { value: 'professional', label: '💼 Professional' },
    { value: 'friendly', label: '😊 Friendly' },
    { value: 'bold', label: '⚡ Bold & Direct' },
    { value: 'consultative', label: '🎯 Consultative' },
  ];

  return (
    <div className="w-full max-w-2xl overflow-hidden rounded-2xl bg-card shadow-2xl border">
      {/* Header */}
      <div className="bg-gradient-to-br from-violet-600 to-indigo-700 px-8 py-8 text-center">
        <div className="text-4xl mb-3">🧠</div>
        <h2 className="text-2xl font-bold text-white mb-2">Train Your AI Assistant</h2>
        <p className="text-violet-100 text-sm max-w-md mx-auto">
          A 60-second setup so your AI knows your business — and gives you relevant, specific answers from day one.
        </p>
      </div>

      {/* Form */}
      <div className="p-8 space-y-5 flex-1 max-h-[60vh] overflow-y-auto">
        {error && (
          <div className="p-3 bg-red-500/10 border border-red-500/20 rounded-lg text-red-500 text-sm">{error}</div>
        )}

        {/* Company name */}
        <div>
          <label className="block text-sm font-medium mb-1">Company name <span className="text-red-400">*</span></label>
          <input
            id="kb-company-name"
            value={form.companyName}
            onChange={e => set('companyName', e.target.value)}
            placeholder={currentPlaceholders.company}
            className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          {/* Industry */}
          <div>
            <label className="block text-sm font-medium mb-1">Industry</label>
            <input
              id="kb-industry"
              value={form.industry}
              onChange={e => set('industry', e.target.value)}
              placeholder={currentPlaceholders.industry}
              className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          {/* Team size */}
          <div>
            <label className="block text-sm font-medium mb-1">Team size</label>
            <select
              id="kb-team-size"
              value={form.teamSize}
              onChange={e => set('teamSize', e.target.value)}
              className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="">Select…</option>
              <option>1–10</option>
              <option>11–50</option>
              <option>51–200</option>
              <option>200+</option>
            </select>
          </div>
        </div>

        {/* USP */}
        <div>
          <label className="block text-sm font-medium mb-1">What makes you different?</label>
          <input
            id="kb-usp"
            value={form.usp}
            onChange={e => set('usp', e.target.value)}
            placeholder={currentPlaceholders.usp}
            className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>

        {/* Key products */}
        <div>
          <label className="block text-sm font-medium mb-1">Key products / services</label>
          <textarea
            id="kb-products"
            value={form.products}
            onChange={e => set('products', e.target.value)}
            placeholder={currentPlaceholders.products}
            rows={3}
            className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
          />
        </div>

        {/* Sales tone */}
        <div>
          <label className="block text-sm font-medium mb-2">Sales communication tone</label>
          <div id="kb-tone-selector" className="grid grid-cols-2 gap-2">
            {tones.map(t => (
              <button
                key={t.value}
                type="button"
                onClick={() => set('tone', t.value)}
                className={`px-3 py-2 rounded-lg border text-sm font-medium transition-all text-left ${
                  form.tone === t.value
                    ? 'border-blue-500 bg-blue-50 text-blue-700'
                    : 'border-border hover:border-blue-300 text-muted-foreground'
                }`}
              >
                {t.label}
              </button>
            ))}
          </div>
        </div>

        {/* Top objection */}
        <div>
          <label className="block text-sm font-medium mb-1">
            Top objection you hear — and your response
            <span className="ml-1 text-muted-foreground font-normal">(optional)</span>
          </label>
          <textarea
            id="kb-objection"
            value={form.topObjection}
            onChange={e => set('topObjection', e.target.value)}
            placeholder={currentPlaceholders.objection}
            rows={2}
            className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
          />
        </div>
      </div>

      {/* Footer */}
      <div className="flex bg-card items-center justify-between px-8 py-4 border-t">
        <button
          id="kb-skip-btn"
          onClick={onSkip}
          disabled={saving}
          className="text-sm text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50"
        >
          Skip for now →
        </button>
        <button
          id="kb-save-btn"
          onClick={handleSave}
          disabled={saving || !form.companyName.trim()}
          className="flex items-center gap-2 px-6 py-2.5 rounded-xl bg-gradient-to-r from-violet-600 to-indigo-600 text-white font-semibold text-sm hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {saving ? (
            <>
              <span className="animate-spin h-4 w-4 border-2 border-white border-t-transparent flex-shrink-0 rounded-full" />
              Saving…
            </>
          ) : (
            <>🚀 Save &amp; Finish Setup</>
          )}
        </button>
      </div>
    </div>
  );
}
