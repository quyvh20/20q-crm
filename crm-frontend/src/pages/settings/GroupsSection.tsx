import GroupsManager from '../../components/settings/GroupsManager';

// User Groups section of the settings shell (U1).
export default function GroupsSection() {
  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-foreground">User Groups</h2>
        <p className="text-sm text-muted-foreground mt-0.5">Named groups of members you can share reports with.</p>
      </div>
      <div className="bg-card border border-border rounded-2xl p-6">
        <GroupsManager />
      </div>
    </div>
  );
}
