import { Lock } from 'lucide-react';
import { capabilityLabel } from '../../lib/roles';

// AccessDeniedPanel is the shared friendly denied state (U3.7): instead of a
// surface that 403s on every action (or a silent blank), tell the user which
// permission they're missing and who can fix it. Keyed to a capability code so
// the copy stays consistent with the roles catalog.
export default function AccessDeniedPanel({
  capability,
  what,
  message,
}: {
  // The capability code the surface requires, e.g. 'workflows.manage'.
  capability?: string;
  // Short noun for what's being protected, e.g. "email templates". Defaults to
  // a generic sentence when omitted.
  what?: string;
  // Full replacement sentence for denials that aren't capability-keyed, e.g.
  // object-level read access: "Your role can't view Invoice records — ask an
  // admin for access." Wins over capability/what when set.
  message?: string;
}) {
  const label = capability ? capabilityLabel(capability) : '';
  return (
    <div className="flex flex-col items-center justify-center text-center py-16 px-6">
      <div className="w-12 h-12 rounded-2xl bg-muted flex items-center justify-center mb-4">
        <Lock className="w-6 h-6 text-muted-foreground" aria-hidden="true" />
      </div>
      <h2 className="text-lg font-semibold text-foreground">You don't have access to this</h2>
      <p className="text-sm text-muted-foreground mt-1 max-w-md">
        {message ?? (
          <>
            {what ? `Viewing ${what} requires` : 'This page requires'} the{' '}
            <span className="font-medium text-foreground">{label}</span> permission — ask a
            workspace admin if you need it.
          </>
        )}
      </p>
    </div>
  );
}
