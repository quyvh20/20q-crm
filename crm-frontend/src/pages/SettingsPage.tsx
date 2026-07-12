import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import ObjectsManager from '../components/settings/ObjectsManager';
import KnowledgeBase from '../components/settings/KnowledgeBase';
import PipelineStagesManager from '../components/settings/PipelineStagesManager';
import PermissionsManager from '../components/settings/PermissionsManager';
import FieldSecurityManager from '../components/settings/FieldSecurityManager';
import RolesManager from '../components/settings/RolesManager';
import AuditLogViewer from '../components/settings/AuditLogViewer';
import SecuritySessions from '../components/settings/SecuritySessions';

type Tab = { id: string; label: string; icon: string };

const BASE_TABS: Tab[] = [
  { id: 'pipeline', label: 'Pipeline', icon: '🎯' },
  { id: 'objects', label: 'Objects & Fields', icon: '📦' },
  { id: 'knowledge', label: 'Knowledge Base', icon: '🧠' },
  { id: 'templates', label: 'Templates', icon: '🏗️' },
  // Security (personal session management) is available to every member.
  { id: 'security', label: 'Security', icon: '🔒' },
];

export default function SettingsPage() {
  const { hasCapability } = useAuth();
  const navigate = useNavigate();
  // Capability-gated (P3/P4): the Permissions tab needs roles.manage and the
  // Audit Log needs audit.view, so a custom role an admin grants either to sees
  // it — not just the built-in admin/owner names. AI Logs is admin oversight
  // (members.manage). Server enforces every gate.
  const canManageRoles = hasCapability('roles.manage');
  const canViewAudit = hasCapability('audit.view');
  // AI Logs must match the capability ConversationLogPage actually enforces
  // (members.manage) — gating it on roles.manage showed some roles a tab that
  // dead-ended on an access-denied page (U0.8).
  const canManageMembers = hasCapability('members.manage');

  const TABS: Tab[] = [
    ...BASE_TABS,
    ...(canViewAudit ? [{ id: 'audit', label: 'Audit Log', icon: '📋' }] : []),
    ...(canManageRoles ? [{ id: 'permissions', label: 'Permissions', icon: '🔐' }] : []),
    ...(canManageMembers ? [{ id: 'ai-logs', label: 'AI Logs', icon: '💬' }] : []),
  ];

  const [activeTab, setActiveTab] = useState<string>('pipeline');

  const handleTab = (id: string) => {
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
            onClick={() => handleTab(tab.id)}
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

        {activeTab === 'objects' && <ObjectsManager />}

        {activeTab === 'permissions' && (
          <div className="space-y-10">
            <RolesManager />
            <PermissionsManager />
            <FieldSecurityManager />
          </div>
        )}

        {activeTab === 'audit' && <AuditLogViewer />}

        {activeTab === 'security' && <SecuritySessions />}

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
