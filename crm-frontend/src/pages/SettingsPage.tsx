import { useState } from 'react';
import CustomFieldManager from '../components/settings/CustomFieldManager';
import ObjectDefManager from '../components/settings/ObjectDefManager';

const TABS = [
  { id: 'fields', label: 'Custom Fields', icon: '📋' },
  { id: 'objects', label: 'Custom Objects', icon: '📦' },
  { id: 'templates', label: 'Templates', icon: '🏗️' },
] as const;

type TabId = (typeof TABS)[number]['id'];

export default function SettingsPage() {
  const [activeTab, setActiveTab] = useState<TabId>('fields');

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Settings</h1>
        <p className="text-muted-foreground mt-1">
          Configure custom fields, templates, and CRM preferences.
        </p>
      </div>

      {/* Tab navigation */}
      <div className="flex gap-1 border-b">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`flex items-center gap-2 px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${
              activeTab === tab.id
                ? 'border-blue-500 text-foreground'
                : 'border-transparent text-muted-foreground hover:text-foreground hover:border-muted-foreground/30'
            }`}
          >
            <span>{tab.icon}</span>
            {tab.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      <div>
        {activeTab === 'fields' && <CustomFieldManager />}

        {activeTab === 'objects' && <ObjectDefManager />}

        {activeTab === 'templates' && (
          <div className="text-center py-16 text-muted-foreground">
            <div className="text-5xl mb-4">🏗️</div>
            <h3 className="text-lg font-semibold text-foreground mb-2">Industry Templates</h3>
            <p className="text-sm max-w-md mx-auto">
              Pre-built pipeline stages, custom fields, and AI context for your industry.
              Coming soon in a future update.
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
