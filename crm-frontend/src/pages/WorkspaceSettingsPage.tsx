import { useState } from 'react';
import { useAuth } from '../lib/auth';
import MembersList from '../components/settings/MembersList';
import InviteMemberModal from '../components/settings/InviteMemberModal';
import GroupsManager from '../components/settings/GroupsManager';

export default function WorkspaceSettingsPage() {
  const { activeWorkspace, currentRole, hasCapability } = useAuth();
  const [showInvite, setShowInvite] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const canInvite = currentRole === 'owner' || currentRole === 'admin' || currentRole === 'manager';
  const canManageGroups = hasCapability('groups.manage');

  return (
    <div className="max-w-4xl mx-auto">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-foreground">Workspace Settings</h1>
          <p className="text-muted-foreground mt-1">
            Manage members and roles for <span className="font-medium text-foreground">{activeWorkspace?.org_name}</span>
          </p>
        </div>
        {canInvite && (
          <button
            onClick={() => setShowInvite(true)}
            className="px-4 py-2.5 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity flex items-center gap-2"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
            </svg>
            Invite Member
          </button>
        )}
      </div>

      <div className="bg-card border border-border rounded-2xl p-6">
        <h2 className="text-lg font-semibold text-foreground mb-4">Members</h2>
        <MembersList key={refreshKey} />
      </div>

      {canManageGroups && (
        <div className="bg-card border border-border rounded-2xl p-6 mt-6">
          <h2 className="text-lg font-semibold text-foreground mb-1">User Groups</h2>
          <p className="text-sm text-muted-foreground mb-4">Named groups of members you can share reports with.</p>
          <GroupsManager />
        </div>
      )}

      {showInvite && (
        <InviteMemberModal
          onClose={() => setShowInvite(false)}
          onInvited={() => setRefreshKey(k => k + 1)}
        />
      )}
    </div>
  );
}
