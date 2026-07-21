import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { Sparkles } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Spinner } from '../../components/ui/spinner';
import StarterTemplateModal from '../../features/onboarding/StarterTemplateModal';
import { listTemplates, type SystemTemplateSummary } from '../../lib/api';

// Starter templates, reachable permanently.
//
// The picker previously existed ONLY inside the setup checklist on the dashboard,
// which hides itself once every step is done or the card is dismissed
// (SetupChecklist.tsx). So the moment a workspace finished onboarding, the 25
// industry templates became unreachable except through the account menu's "Setup
// guide" — and applying a template is exactly the thing you want AFTER looking
// around, not before. Settings is where people went looking for it.
//
// This deliberately reuses StarterTemplateModal rather than re-listing the catalog:
// the apply flow, the per-item report and the knowledge-base step are all tested
// there, and a second implementation would drift from it.

export default function StarterTemplateSection() {
  const [open, setOpen] = useState(false);

  // Catalog counts, so the page says something concrete before you open the picker.
  // Failure is not surfaced as an error: the button still works, and the modal owns
  // the real error state.
  const { data: templates = [], isLoading } = useQuery<SystemTemplateSummary[]>({
    queryKey: ['templates'],
    queryFn: listTemplates,
  });

  const applied = templates.filter(t => t.applied);
  const categories = new Set(templates.map(t => t.category).filter(Boolean));

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-foreground">Starter Templates</h2>
        <p className="text-sm text-muted-foreground mt-0.5">
          Set up your workspace the way your industry actually works — pipeline stages, the fields
          you track, custom objects, a knowledge base for the AI assistant, and a few starter
          automations. Everything a template creates is ordinary configuration you can rename,
          extend or delete afterwards.
        </p>
      </div>

      <div className="bg-card border border-border rounded-xl p-6">
        {isLoading ? (
          <div className="flex justify-center py-6"><Spinner /></div>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm">
              <span className="text-foreground">
                <span className="font-semibold">{templates.length}</span> templates
              </span>
              {categories.size > 0 && (
                <span className="text-muted-foreground">across {categories.size} industries</span>
              )}
              {applied.length > 0 && (
                <span className="text-muted-foreground">
                  {applied.length} already applied here ({applied.map(t => t.name).join(', ')})
                </span>
              )}
            </div>

            <div className="mt-4 flex flex-wrap items-center gap-3">
              <Button onClick={() => setOpen(true)}>
                <Sparkles aria-hidden className="h-4 w-4" />
                Browse templates
              </Button>
              <p className="text-sm text-muted-foreground">
                Applying a template only <span className="font-medium text-foreground">adds</span>{' '}
                — anything you already have is left untouched.
              </p>
            </div>
          </>
        )}
      </div>

      <p className="text-sm text-muted-foreground">
        Prefer to build it yourself? Start in{' '}
        <Link to="/settings/objects" className="text-primary underline">Objects &amp; Fields</Link>{' '}
        or <Link to="/settings/pipeline" className="text-primary underline">Pipeline</Link>.
      </p>

      {open && (
        <StarterTemplateModal
          open
          surface="checklist"
          onClose={() => setOpen(false)}
        />
      )}
    </div>
  );
}
