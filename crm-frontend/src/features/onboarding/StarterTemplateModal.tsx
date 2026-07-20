import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Building2, Loader2, MonitorSmartphone } from 'lucide-react';
import Modal from '../../components/common/Modal';
import { Button } from '../../components/ui/button';
import { createFieldDef, createObjectDef } from '../../lib/api';
import { usePermissions } from '../../lib/auth';
import KBQuickFillForm from './KBQuickFillForm';

// "Start from a template" (U7.5) — the OTHER half of the retired welcome wizard.
//
// The wizard's template packs were real: one click deployed a whole custom object
// plus the custom fields that go with it. What was wrong was the delivery — a
// blocking full-screen overlay, shown exactly once, on your very first minute in
// the product, when you had no idea what a custom object even was. Dismiss it and
// the packs became unreachable forever.
//
// Same functionality, opened on purpose: from the setup checklist, in the shared
// Modal (Escape, focus trap, focus restore), any time you like. Deploying no longer
// force-reloads the page — it invalidates the sidebar's object list instead.

type TemplateId = 'real-estate' | 'saas';
type Step = 'templates' | 'kb' | 'done';

interface StarterTemplateModalProps {
  open: boolean;
  onClose: () => void;
}

const TEMPLATES: {
  id: TemplateId;
  name: string;
  blurb: string;
  Icon: typeof Building2;
}[] = [
  {
    id: 'real-estate',
    name: 'Real estate',
    blurb: 'Adds a Properties object (address, price, status, bedrooms) and a Lead Source field on contacts.',
    Icon: Building2,
  },
  {
    id: 'saas',
    name: 'B2B SaaS',
    blurb: 'Adds a Subscriptions object (plan, MRR, renewal date) and a Job Role field on contacts.',
    Icon: MonitorSmartphone,
  },
];

async function deploy(templateId: TemplateId) {
  if (templateId === 'real-estate') {
    await createObjectDef({
      slug: 'property',
      label: 'Property',
      label_plural: 'Properties',
      icon: '🏠',
      fields: [
        { key: 'address', label: 'Address', type: 'text', required: true, position: 0 },
        { key: 'price', label: 'Listing Price', type: 'number', required: true, position: 1 },
        { key: 'status', label: 'Status', type: 'select', required: false, position: 2, options: ['Active', 'Pending', 'Sold'] },
        { key: 'bedrooms', label: 'Bedrooms', type: 'number', required: false, position: 3 },
      ],
    });
    await createFieldDef({
      key: 'lead_source',
      label: 'Lead Source',
      type: 'select',
      entity_type: 'contact',
      options: ['Zillow', 'Referral', 'Open House', 'Direct'],
      required: false,
    });
    return;
  }
  await createObjectDef({
    slug: 'subscription',
    label: 'Subscription',
    label_plural: 'Subscriptions',
    icon: '🔄',
    fields: [
      { key: 'plan', label: 'Plan Tier', type: 'select', required: true, position: 0, options: ['Starter', 'Pro', 'Enterprise'] },
      { key: 'mrr', label: 'MRR', type: 'number', required: true, position: 1 },
      { key: 'renewal_date', label: 'Renewal Date', type: 'date', required: false, position: 2 },
    ],
  });
  await createFieldDef({
    key: 'job_role',
    label: 'Job Role',
    type: 'text',
    entity_type: 'contact',
    required: false,
  });
}

export default function StarterTemplateModal({ open, onClose }: StarterTemplateModalProps) {
  const qc = useQueryClient();
  const { can } = usePermissions();
  const canTemplates = can('objects.manage');
  const canKB = can('knowledge.manage');

  // Someone with knowledge.manage but not objects.manage lands straight on the AI
  // step rather than on two cards whose every call would 403.
  const [step, setStep] = useState<Step>(canTemplates ? 'templates' : 'kb');
  const [deploying, setDeploying] = useState<TemplateId | null>(null);
  const [deployed, setDeployed] = useState<TemplateId | null>(null);
  const [error, setError] = useState<string | null>(null);

  const close = () => {
    if (deploying) return; // don't strand a half-created object
    onClose();
  };

  const runDeploy = async (id: TemplateId) => {
    setDeploying(id);
    setError(null);
    try {
      await deploy(id);
      setDeployed(id);
      // The new object belongs in the sidebar and the new field in every contact
      // form — the old wizard achieved that with window.location.reload().
      // Both registry caches: the sidebar's and the report builder's.
      qc.invalidateQueries({ queryKey: ['sidebar-objects'] });
      qc.invalidateQueries({ queryKey: ['registry-objects'] });
      qc.invalidateQueries({ queryKey: ['field-defs'] });
      setStep(canKB ? 'kb' : 'done');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to apply the template.');
    } finally {
      setDeploying(null);
    }
  };

  const title =
    step === 'templates' ? 'Start from a template' : step === 'kb' ? 'Train your AI assistant' : 'Template applied';

  return (
    <Modal open={open} onClose={close} title={title} size="xl" dismissable={!deploying}>
      {step === 'templates' && (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            A template creates the objects and fields for your industry in one click. Everything it makes is
            ordinary configuration — rename it, extend it or delete it afterwards.
          </p>

          {error && (
            <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            {TEMPLATES.map(({ id, name, blurb, Icon }) => (
              <button
                key={id}
                onClick={() => runDeploy(id)}
                disabled={!!deploying}
                className="flex flex-col rounded-xl border border-border p-4 text-left transition-colors hover:border-primary hover:bg-accent/40 disabled:opacity-60"
              >
                <Icon className="mb-3 h-6 w-6 text-primary" aria-hidden="true" />
                <span className="font-semibold text-foreground">{name}</span>
                <span className="mt-1 flex-1 text-sm text-muted-foreground">{blurb}</span>
                <span className="mt-3 inline-flex items-center gap-1.5 text-sm font-medium text-primary">
                  {deploying === id ? (
                    <>
                      <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" /> Applying…
                    </>
                  ) : (
                    'Apply template'
                  )}
                </span>
              </button>
            ))}
          </div>

          <div className="flex items-center justify-between gap-3 border-t border-border pt-4">
            <p className="text-sm text-muted-foreground">Prefer to build your own?</p>
            <Button
              variant="outline"
              onClick={() => (canKB ? setStep('kb') : close())}
              disabled={!!deploying}
              className="text-muted-foreground hover:text-foreground"
            >
              {canKB ? 'Skip — train the AI instead' : 'Not now'}
            </Button>
          </div>
        </div>
      )}

      {step === 'kb' && (
        <KBQuickFillForm
          templateId={deployed}
          onSaved={() => setStep('done')}
          onSkip={close}
          skipLabel={deployed ? 'Finish' : 'Skip for now'}
        />
      )}

      {step === 'done' && (
        <div className="space-y-4">
          <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600 dark:text-emerald-400">
            {deployed
              ? 'Template applied — your new object is in the sidebar.'
              : 'Saved to your knowledge base.'}
          </div>
          <p className="text-sm text-muted-foreground">
            Fine-tune anything in{' '}
            <Link to="/settings/objects" onClick={close} className="text-primary underline">
              Settings → Objects
            </Link>
            {canKB && (
              <>
                {' '}or{' '}
                <Link to="/settings/knowledge" onClick={close} className="text-primary underline">
                  Knowledge Base
                </Link>
              </>
            )}
            .
          </p>
          <div className="flex justify-end border-t border-border pt-4">
            <Button onClick={close}>Done</Button>
          </div>
        </div>
      )}
    </Modal>
  );
}
