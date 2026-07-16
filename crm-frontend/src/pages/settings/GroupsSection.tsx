import GroupsManager from '../../components/settings/GroupsManager';

// User Groups section of the settings shell (U1).
export default function GroupsSection() {
  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-foreground">User Groups</h2>
        <p className="text-sm text-muted-foreground mt-0.5">
          Groups are your teams. A role set to <span className="font-medium text-foreground">“Records owned by anyone on their teams”</span>{' '}
          lets its members see every record owned by someone in a group they belong to — and a group is also a target you can
          share individual records and reports with.
        </p>
      </div>
      <div className="bg-card border border-border rounded-xl p-6">
        <GroupsManager />
      </div>
    </div>
  );
}
