import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Check, ChevronRight, Circle, Sparkles } from 'lucide-react';
import HelpTip from '../../components/common/HelpTip';
import { Button } from '../../components/ui/button';
import { useAuth, usePermissions } from '../../lib/auth';
import {
  dismissSetupChecklist,
  isLegacyOnboardingDone,
  useChecklistVisibility,
} from './checklistState';
import StarterTemplateModal from './StarterTemplateModal';
import { useSetupChecklist } from './useSetupChecklist';

// The setup checklist (U7.5) — what replaced the one-shot welcome wizard.
//
// The wizard was blocking, appeared exactly once, and vanished forever the moment
// you clicked past it: the single most useful screen in the product was also the
// only one you could never get back to. This card inverts every one of those
// properties. It sits ON the dashboard rather than over it, every step is derived
// live from the workspace (so it can't tell you to do something you already did),
// every step is gated on the capability it needs (so a rep is never told to invite
// people they can't invite), and hiding it is reversible from the account menu.
//
// Visibility, in order of precedence:
//   forceOpen (the user asked for it)     → always show
//   dismissed / onboarding_completed      → hide (but the account menu brings it back)
//   every visible step done               → hide (its job is finished)
//   no visible steps at all               → hide (nothing this user may set up)
export default function SetupChecklist() {
  const { user, activeWorkspace } = useAuth();
  const { can } = usePermissions();
  const orgId = activeWorkspace?.org_id ?? '';
  const { dismissed, forceOpen } = useChecklistVisibility(orgId);
  const { steps, doneCount, allDone, loading } = useSetupChecklist();
  const [showTemplates, setShowTemplates] = useState(false);

  const canTemplates = can('objects.manage');
  const canKB = can('knowledge.manage');

  // `onboarding_completed` is the server flag the retired wizard set. Honoring it
  // (and the localStorage key its earlier version wrote) is what keeps an
  // established user from being greeted like a brand-new one.
  const suppressed = dismissed || !!user?.onboarding_completed || isLegacyOnboardingDone();

  const handleDismiss = () => {
    // Per-ORG dismissal only. Deliberately does NOT set the global
    // `onboarding_completed` flag: that flag suppresses the checklist in EVERY
    // workspace (see `suppressed` above), so writing it here would hide the
    // checklist in all the user's other workspaces — including brand-new empty
    // ones, where it's the most useful thing on the dashboard. The per-org
    // localStorage key records this dismissal for this workspace alone.
    dismissSetupChecklist(orgId);
  };

  if (loading || steps.length === 0) return null;
  if (!forceOpen && (suppressed || allDone)) return null;

  return (
    <section
      aria-labelledby="setup-checklist-heading"
      className="rounded-xl border border-border bg-card p-5 text-card-foreground"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-1.5">
            <h2 id="setup-checklist-heading" className="font-semibold">
              Set up your workspace
            </h2>
            <HelpTip label="How the setup checklist works" title="How this checklist works">
              <p>
                Each step ticks itself the moment it's genuinely done in your workspace — nothing here is
                tracked separately, so it can't fall out of step with reality.
              </p>
              <p>You only see the steps you have permission to do. Hide the card whenever you like; it's in your account menu if you want it back.</p>
            </HelpTip>
          </div>
          <p className="mt-0.5 text-sm text-muted-foreground">
            {allDone
              ? "You're all set — every step below is done."
              : `${doneCount} of ${steps.length} done. Pick up wherever you left off.`}
          </p>
        </div>
        <Button variant="ghost" size="sm" onClick={handleDismiss} className="text-muted-foreground hover:text-foreground">
          Hide
        </Button>
      </div>

      <ul className="mt-4 space-y-1">
        {steps.map((step) => (
          <li key={step.id}>
            <Link
              to={step.to}
              className="group flex items-center gap-3 rounded-lg px-2 py-2.5 transition-colors hover:bg-accent"
            >
              {step.done ? (
                <span
                  role="img"
                  aria-label={`${step.title} — done`}
                  className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-emerald-500/15"
                >
                  <Check className="h-4 w-4 text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
                </span>
              ) : (
                <span
                  role="img"
                  aria-label={`${step.title} — not done yet`}
                  className="flex h-6 w-6 shrink-0 items-center justify-center"
                >
                  <Circle className="h-4 w-4 text-muted-foreground/60" aria-hidden="true" />
                </span>
              )}
              <span className="min-w-0 flex-1">
                <span
                  className={`block text-sm font-medium ${
                    step.done ? 'text-muted-foreground line-through decoration-muted-foreground/40' : 'text-foreground'
                  }`}
                >
                  {step.title}
                </span>
                <span className="block text-xs text-muted-foreground">{step.why}</span>
              </span>
              <span className="hidden shrink-0 items-center gap-0.5 text-xs font-medium text-primary sm:inline-flex">
                {step.cta}
                <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />
              </span>
            </Link>
          </li>
        ))}
      </ul>

      {(canTemplates || canKB) && (
        <div className="mt-3 border-t border-border pt-3">
          <button
            onClick={() => setShowTemplates(true)}
            className="inline-flex items-center gap-1.5 rounded-lg px-2 py-1.5 text-sm font-medium text-primary transition-colors hover:bg-accent"
          >
            <Sparkles className="h-4 w-4" aria-hidden="true" />
            {canTemplates ? 'Start from a template' : 'Train your AI assistant'}
          </button>
          <p className="px-2 text-xs text-muted-foreground">
            {canTemplates
              ? 'Create the objects and fields for your industry in one click — real estate or B2B SaaS.'
              : 'Tell the AI assistant what you sell and how you talk, so its answers fit your business.'}
          </p>
        </div>
      )}

      {showTemplates && <StarterTemplateModal open onClose={() => setShowTemplates(false)} />}
    </section>
  );
}
