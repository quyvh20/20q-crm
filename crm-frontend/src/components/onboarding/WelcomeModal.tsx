import { useState } from 'react';
import { createObjectDef, createFieldDef } from '../../lib/api';

interface WelcomeModalProps {
  onComplete: () => void;
}

export default function WelcomeModal({ onComplete }: WelcomeModalProps) {
  const [isDeploying, setIsDeploying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const completeOnboarding = () => {
    localStorage.setItem('onboarding_completed', 'true');
    onComplete();
  };

  const handleSkip = () => {
    completeOnboarding();
  };

  const deployTemplate = async (templateId: string) => {
    setIsDeploying(true);
    setError(null);
    try {
      if (templateId === 'real-estate') {
        // Create Properties Custom Object
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
          ]
        });
        
        // Add Lead Source to Contacts
        await createFieldDef({
          key: 'lead_source',
          label: 'Lead Source',
          type: 'select',
          entity_type: 'contact',
          options: ['Zillow', 'Referral', 'Open House', 'Direct'],
          required: false,
        });

      } else if (templateId === 'saas') {
        // Create Subscriptions Custom Object
        await createObjectDef({
          slug: 'subscription',
          label: 'Subscription',
          label_plural: 'Subscriptions',
          icon: '🔄',
          fields: [
            { key: 'plan', label: 'Plan Tier', type: 'select', required: true, position: 0, options: ['Starter', 'Pro', 'Enterprise'] },
            { key: 'mrr', label: 'MRR', type: 'number', required: true, position: 1 },
            { key: 'renewal_date', label: 'Renewal Date', type: 'date', required: false, position: 2 },
          ]
        });

        // Add Role to Contacts
        await createFieldDef({
          key: 'job_role',
          label: 'Job Role',
          type: 'text',
          entity_type: 'contact',
          required: false,
        });
      }
      
      completeOnboarding();
      window.location.reload(); // Reload to refresh sidebar and layout
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to deploy template');
      setIsDeploying(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
      <div className="w-full max-w-3xl overflow-hidden rounded-2xl bg-card shadow-2xl border">
        
        {/* Header */}
        <div className="bg-gradient-to-br from-blue-600 to-indigo-700 px-8 py-10 text-center relative overflow-hidden">
          <div className="absolute top-0 right-0 p-12 opacity-10">
             <div className="text-9xl">🚀</div>
          </div>
          <h2 className="text-3xl font-bold text-white mb-3 relative z-10">Welcome to Guerrilla CRM</h2>
          <p className="text-blue-100 max-w-lg mx-auto text-lg relative z-10">
            Let's configure your workspace. Choose a template below to auto-generate the perfect custom objects and fields for your industry.
          </p>
        </div>

        {/* Content */}
        <div className="p-8">
          {error && (
            <div className="mb-6 p-4 bg-red-500/10 border border-red-500/20 rounded-lg text-red-500 text-sm">
              {error}
            </div>
          )}

          <div className="grid md:grid-cols-2 gap-6 mb-8">
            {/* Real Estate Card */}
            <button
              onClick={() => deployTemplate('real-estate')}
              disabled={isDeploying}
              className="flex flex-col text-left border-2 rounded-xl p-6 hover:border-blue-500 hover:shadow-md transition-all group disabled:opacity-50"
            >
              <div className="text-4xl mb-4 group-hover:scale-110 transition-transform origin-left">🏠</div>
              <h3 className="text-xl font-bold mb-2 text-foreground">Real Estate Pack</h3>
              <p className="text-muted-foreground text-sm flex-1">
                Optimized for brokers and agents. Adds a <strong className="text-foreground">Properties</strong> database and custom lead source trackers for contacts.
              </p>
              <div className="mt-4 text-sm font-semibold text-blue-500 group-hover:text-blue-600 transition-colors">
                Apply Template →
              </div>
            </button>

            {/* SaaS Card */}
            <button
              onClick={() => deployTemplate('saas')}
              disabled={isDeploying}
              className="flex flex-col text-left border-2 rounded-xl p-6 hover:border-blue-500 hover:shadow-md transition-all group disabled:opacity-50"
            >
              <div className="text-4xl mb-4 group-hover:scale-110 transition-transform origin-left">💻</div>
              <h3 className="text-xl font-bold mb-2 text-foreground">B2B SaaS Sales</h3>
              <p className="text-muted-foreground text-sm flex-1">
                Perfect for software startups. Sets up a <strong className="text-foreground">Subscriptions</strong> custom object to track recurring revenue (MRR) and renewals.
              </p>
              <div className="mt-4 text-sm font-semibold text-blue-500 group-hover:text-blue-600 transition-colors">
                Apply Template →
              </div>
            </button>
          </div>

          <div className="flex items-center justify-between pt-4 border-t">
            <p className="text-sm text-muted-foreground">
              Prefer to build your own generic architecture?
            </p>
            <button
              onClick={handleSkip}
              disabled={isDeploying}
              className="px-6 py-2 border-2 border-transparent text-muted-foreground hover:text-foreground font-medium rounded-lg hover:bg-accent transition-colors disabled:opacity-50"
            >
              Skip & Start Blank
            </button>
          </div>
        </div>

        {isDeploying && (
          <div className="absolute inset-0 bg-background/50 backdrop-blur-sm flex flex-col items-center justify-center z-50">
             <div className="animate-spin h-12 w-12 border-4 border-blue-500 border-t-transparent rounded-full mb-4"></div>
             <p className="text-lg font-medium text-foreground">Constructing your database...</p>
          </div>
        )}

      </div>
    </div>
  );
}
