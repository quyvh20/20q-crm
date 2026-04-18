import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import CustomFieldManager from '../components/settings/CustomFieldManager';
import ObjectDefManager from '../components/settings/ObjectDefManager';
import KnowledgeBase from '../components/settings/KnowledgeBase';
import PipelineStagesManager from '../components/settings/PipelineStagesManager';

const BASE_TABS = [
  { id: 'pipeline', label: 'Pipeline', icon: '🎯' },
  { id: 'fields', label: 'Custom Fields', icon: '📋' },
  { id: 'objects', label: 'Custom Objects', icon: '📦' },
  { id: 'knowledge', label: 'Knowledge Base', icon: '🧠' },
  { id: 'templates', label: 'Templates', icon: '🏗️' },
] as const;

const ADMIN_TABS = [
  ...BASE_TABS,
  { id: 'ai-logs', label: 'AI Logs', icon: '💬' },
] as const;

type BaseTabId = (typeof BASE_TABS)[number]['id'];
type AdminTabId = (typeof ADMIN_TABS)[number]['id'];
type TabId = BaseTabId | AdminTabId;

export default function SettingsPage() {
  const { currentRole } = useAuth();
  const navigate = useNavigate();
  const isAdmin = currentRole === 'admin' || currentRole === 'owner';
  const TABS = isAdmin ? ADMIN_TABS : BASE_TABS;

  const [activeTab, setActiveTab] = useState<TabId>('pipeline');

  const handleTab = (id: TabId) => {
    if (id === 'ai-logs') {
      navigate('/settings/ai-logs');
      return;
    }
    setActiveTab(id);
  };

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-2xl font-bold tracking-tight">Settings</h1>
        <p className="text-muted-foreground mt-1">
          Configure pipeline stages, custom fields, templates, and CRM preferences.
        </p>
      </div>

      {/* Tab navigation */}
      <div className="flex gap-1 border-b">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            onClick={() => handleTab(tab.id as TabId)}
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
        {activeTab === 'pipeline' && <PipelineStagesManager />}

        {activeTab === 'fields' && <CustomFieldManager />}

        {activeTab === 'objects' && <ObjectDefManager />}

        {activeTab === 'knowledge' && <KnowledgeBase />}

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
