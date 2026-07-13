import { useState } from 'react';
import { useAuth } from '../../lib/auth';
import MembersList from '../../components/settings/MembersList';
import InviteMemberModal from '../../components/settings/InviteMemberModal';

// Members section of the settings shell (U1) — the former WorkspaceSettingsPage
// minus the page chrome (the shell owns the header) and minus groups (their own
// section now).
export default function MembersSection() {
  const { activeWorkspace, hasCapability } = useAuth();
  const [showInvite, setShowInvite] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  const canInvite = hasCapability('members.invite');

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold text-foreground">Members</h2>
          <p className="text-sm text-muted-foreground mt-0.5">
            People in <span className="font-medium text-foreground">{activeWorkspace?.org_name}</span> and their roles.
          </p>
        </div>
        {canInvite && (
          <button
            onClick={() => setShowInvite(true)}
            className="px-4 py-2 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity shrink-0"
          >
            + Invite member
          </button>
        )}
      </div>

      <div className="bg-card border border-border rounded-2xl p-6">
        <MembersList key={refreshKey} />
      </div>

      {showInvite && (
        <InviteMemberModal
          onClose={() => setShowInvite(false)}
          onInvited={() => setRefreshKey((k) => k + 1)}
        />
      )}
    </div>
  );
}
