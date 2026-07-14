import { useQuery } from '@tanstack/react-query';
import {
  getContacts,
  getRoles,
  getStages,
  getWorkspaceMembers,
  listInvitations,
  type PipelineStage,
} from '../../lib/api';
import { usePermissions } from '../../lib/auth';

// The returnable setup checklist's data layer (U7.5).
//
// Every step's "done" is DERIVED from data the API already exposes — there is no
// per-step server state and U7 adds no endpoint. That's a feature, not a
// compromise: a checklist that stores its own ticks drifts (you invite someone in
// Settings, the tick never lands; you delete the role you made, the tick stays).
// Derived state can't lie about the workspace.
//
// Each step is also GATED on the capability it needs. A rep who cannot invite must
// not be told to invite — the step disappears for them, and its query never fires
// (three of the four probes are capability-gated server-side and would 403).

export type SetupStepId = 'invite' | 'roles' | 'pipeline' | 'contacts';

export interface SetupStep {
  id: SetupStepId;
  title: string;
  /** One line: why this is worth doing. */
  why: string;
  /** Where the user actually does it. */
  to: string;
  cta: string;
  done: boolean;
}

// ONE namespace for every probe, so any caller can refresh the whole checklist with
// a single invalidateQueries({ queryKey: setupKeys.all }).
export const setupKeys = {
  all: ['setup-checklist'] as const,
  members: () => [...setupKeys.all, 'members'] as const,
  invitations: () => [...setupKeys.all, 'invitations'] as const,
  roles: () => [...setupKeys.all, 'roles'] as const,
  stages: () => [...setupKeys.all, 'stages'] as const,
  contacts: () => [...setupKeys.all, 'contacts'] as const,
};

// The steps are completed on OTHER routes (Settings → Members, Settings → Pipeline,
// the contacts importer). Coming back to the dashboard remounts the card, and these
// options make that remount re-derive from the server instead of replaying a cached
// "not done yet" — which is the whole point of deriving from live data.
const PROBE_OPTIONS = {
  staleTime: 0,
  refetchOnMount: 'always',
  retry: false,
} as const;

/** The pipeline every new workspace is seeded with (backend: seedDefaultStages in
 *  internal/usecase/auth_usecase.go). Stages therefore ALWAYS exist — "has stages"
 *  would be true on day zero and the step would be born ticked, which is why this
 *  step asks whether the pipeline has been made your OWN. */
export const SEEDED_STAGE_NAMES = ['Lead In', 'Qualified', 'Proposal', 'Negotiation', 'Closed Won'];

/** True once the stage list differs from the untouched seed — renamed, reordered,
 *  added to or removed from. An empty pipeline is NOT customized (nothing to sell
 *  through yet). */
export function isPipelineCustomized(stages: Pick<PipelineStage, 'name' | 'position'>[]): boolean {
  if (stages.length === 0) return false;
  if (stages.length !== SEEDED_STAGE_NAMES.length) return true;
  const ordered = [...stages].sort((a, b) => a.position - b.position);
  return ordered.some((s, i) => s.name.trim() !== SEEDED_STAGE_NAMES[i]);
}

export interface SetupChecklistState {
  /** Only the steps this user is allowed to do. */
  steps: SetupStep[];
  doneCount: number;
  allDone: boolean;
  /** True while any gated probe is still in flight — render nothing rather than a
   *  card of empty circles that tick themselves a moment later. */
  loading: boolean;
}

export function useSetupChecklist(): SetupChecklistState {
  const { can, canAccess, loaded } = usePermissions();

  // Gate on `loaded`: before the capability fetch settles, can() is false for
  // everything — firing the probes then would 403 and cache a wrong answer.
  const canInvite = loaded && can('members.invite');
  const canRoles = loaded && can('roles.manage');
  const canPipeline = loaded && can('pipeline.manage');
  // canAccess fails OPEN while the OLS map is unknown; `loaded` pins that down.
  const canImport = loaded && canAccess('contact', 'create');

  const members = useQuery({
    queryKey: setupKeys.members(),
    queryFn: getWorkspaceMembers,
    enabled: canInvite,
    ...PROBE_OPTIONS,
  });

  // An invite that has been SENT counts. Waiting for the invitee to click the link
  // before ticking the step would leave the user staring at an undone task they
  // have already done.
  const invitations = useQuery({
    queryKey: setupKeys.invitations(),
    queryFn: listInvitations,
    enabled: canInvite,
    ...PROBE_OPTIONS,
  });

  const roles = useQuery({
    queryKey: setupKeys.roles(),
    queryFn: getRoles,
    enabled: canRoles,
    ...PROBE_OPTIONS,
  });

  const stages = useQuery({
    queryKey: setupKeys.stages(),
    queryFn: getStages,
    enabled: canPipeline,
    ...PROBE_OPTIONS,
  });

  // limit:1 — this asks "is there anything at all?", not "give me the contacts".
  // Note it reads through the caller's own row scope, so an 'own'-scoped member is
  // asked about THEIR contacts. That's the honest question for a personal checklist.
  const contacts = useQuery({
    queryKey: setupKeys.contacts(),
    queryFn: () => getContacts({ limit: 1 }).then((r) => r.contacts ?? []),
    enabled: canImport,
    ...PROBE_OPTIONS,
  });

  const memberCount = members.data?.length ?? 0;
  // Expired/revoked invitations are dead — they don't mean "you invited someone".
  const openInvites = (invitations.data ?? []).filter(
    (i) => i.status !== 'expired' && i.status !== 'revoked',
  ).length;

  const steps: SetupStep[] = [];

  if (canInvite) {
    steps.push({
      id: 'invite',
      title: 'Invite your team',
      why: 'A CRM only pays off when the whole team logs what they know.',
      to: '/settings/members',
      cta: 'Invite people',
      done: memberCount > 1 || openInvites > 0,
    });
  }

  if (canRoles) {
    steps.push({
      id: 'roles',
      title: 'Set up roles & permissions',
      why: 'Decide who can see and change what before the data piles up.',
      to: '/settings/roles',
      cta: 'Review roles',
      // A workspace ships with built-in roles; making one of your own is the signal
      // that someone actually thought about access.
      done: (roles.data ?? []).some((r) => !r.is_system),
    });
  }

  if (canPipeline) {
    steps.push({
      id: 'pipeline',
      title: 'Build your pipeline',
      why: 'Rename the stages to match how your deals really move.',
      to: '/settings/pipeline',
      cta: 'Edit stages',
      done: isPipelineCustomized(stages.data ?? []),
    });
  }

  if (canImport) {
    steps.push({
      id: 'contacts',
      title: 'Import your contacts',
      why: 'Bring in the people you already work with — CSV or one by one.',
      to: '/contacts',
      cta: 'Add contacts',
      done: (contacts.data ?? []).length > 0,
    });
  }

  const probes = [members, invitations, roles, stages, contacts];
  const loading = !loaded || probes.some((p) => p.isLoading && p.fetchStatus !== 'idle');

  const doneCount = steps.filter((s) => s.done).length;

  return {
    steps,
    doneCount,
    allDone: steps.length > 0 && doneCount === steps.length,
    loading,
  };
}
