import {
  Shield, Users, UsersRound, KeyRound, Boxes, Table2, EyeOff,
  Target, Mail, Brain, ScrollText, MessageSquare, UserRound, Building2, Bell, Plug,
  Sparkles, Users2, Gauge, type LucideIcon,
} from 'lucide-react';

// The settings section REGISTRY, split out of SettingsLayout.tsx.
//
// It lives in its own module because two very different callers need it: the
// settings shell (a lazily-loaded route chunk) and the global command palette
// (which is mounted in AppLayout, so it is in the eager entry chunk). While
// both lived in SettingsLayout.tsx, that static import from GlobalSearch pinned
// the whole settings shell into the entry chunk and the build said so:
//
//   [INEFFECTIVE_DYNAMIC_IMPORT] src/pages/settings/SettingsLayout.tsx is
//   dynamically imported by src/App.tsx but also statically imported by
//   src/components/common/GlobalSearch.tsx, dynamic import will not move module
//   into another chunk.
//
// That is the failure mode of route-level splitting generally: one static
// import from anything eager silently cancels a lazy() route, and nothing but
// this warning tells you. Keep this file free of component and route imports so
// it stays cheap for the eager side to depend on.

type Can = (cap: string) => boolean;

export interface SettingsSection {
  path: string;            // route segment under /settings
  label: string;
  icon: LucideIcon;
  group: 'personal' | 'workspace';
  visible: (can: Can) => boolean;
  /** Renders as a plain link to another route instead of a nested section. */
  externalTo?: string;
}

export const SETTINGS_SECTIONS: SettingsSection[] = [
  // Personal — available to every member.
  { path: 'profile', label: 'Profile', icon: UserRound, group: 'personal', visible: () => true },
  { path: 'security', label: 'Security', icon: Shield, group: 'personal', visible: () => true },
  // Personal API tokens (U6.5) — self-scoped, so every member gets the page.
  { path: 'api-tokens', label: 'API Tokens', icon: KeyRound, group: 'personal', visible: () => true },
  { path: 'notifications', label: 'Notifications', icon: Bell, group: 'personal', visible: () => true },

  // Workspace — admin/config surfaces, each behind the capability its API checks.
  { path: 'general', label: 'General', icon: Building2, group: 'workspace', visible: (can) => can('org.settings') },
  { path: 'members', label: 'Members', icon: Users, group: 'workspace', visible: (can) => can('members.manage') || can('members.invite') },
  { path: 'groups', label: 'User Groups', icon: UsersRound, group: 'workspace', visible: (can) => can('groups.manage') },
  { path: 'roles', label: 'Roles', icon: KeyRound, group: 'workspace', visible: (can) => can('roles.manage') },
  { path: 'object-access', label: 'Object Access', icon: Table2, group: 'workspace', visible: (can) => can('roles.manage') },
  { path: 'field-access', label: 'Field Access', icon: EyeOff, group: 'workspace', visible: (can) => can('roles.manage') },
  // Sits directly above Objects & Fields and Pipeline because it is the one-click
  // way to populate both. Gated on org.settings to match what
  // POST /api/templates/:slug/apply itself enforces — a looser gate here would show
  // a section whose only button 403s. Path is 'starter-templates', not 'templates':
  // that one is taken by the Email Templates link below, and section paths must not
  // prefix each other or the nav highlights two entries at once.
  { path: 'starter-templates', label: 'Starter Templates', icon: Sparkles, group: 'workspace', visible: (can) => can('org.settings') },
  { path: 'objects', label: 'Objects & Fields', icon: Boxes, group: 'workspace', visible: (can) => can('objects.manage') },
  // R9.4. objects.manage rather than a new capability: scoring rules configure
  // how a computed field on the contact object is derived — the same framing
  // the Duplicates entry below uses. Placed well AFTER 'general' deliberately:
  // defaultSectionPath returns the first visible WORKSPACE section, so anything
  // inserted above general would relocate every admin's /settings landing page.
  { path: 'lead-scoring', label: 'Lead Scoring', icon: Gauge, group: 'workspace', visible: (can) => can('objects.manage') },
  // R8.3: contact merge is capability-agnostic on the backend (it checks the
  // contact object's own edit+delete OLS bits, per caller), but the settings
  // shell's capability list has no per-object entry — objects.manage is the
  // closest existing "workspace data hygiene" gate.
  { path: 'duplicates', label: 'Duplicates', icon: Users2, group: 'workspace', visible: (can) => can('objects.manage') },
  { path: 'pipeline', label: 'Pipeline', icon: Target, group: 'workspace', visible: (can) => can('pipeline.manage') },
  { path: 'integrations', label: 'Integrations', icon: Plug, group: 'workspace', visible: (can) => can('integrations.manage') },
  { path: 'templates', label: 'Email Templates', icon: Mail, group: 'workspace', visible: (can) => can('workflows.manage'), externalTo: '/workflows/email-templates' },
  { path: 'knowledge', label: 'Knowledge Base', icon: Brain, group: 'workspace', visible: (can) => can('knowledge.manage') },
  { path: 'audit', label: 'Audit Log', icon: ScrollText, group: 'workspace', visible: (can) => can('audit.view') },
  { path: 'ai-logs', label: 'AI Logs', icon: MessageSquare, group: 'workspace', visible: (can) => can('members.manage') },
];

// visibleSections is exported for the command palette (settings destinations).
export function visibleSections(can: Can): SettingsSection[] {
  return SETTINGS_SECTIONS.filter((s) => s.visible(can));
}

// defaultSectionPath: where /settings lands. Admins go to their first workspace
// section; a member with no admin capabilities lands on their personal Profile
// page instead of a config screen they can't use.
export function defaultSectionPath(can: Can): string {
  const workspace = visibleSections(can).find((s) => s.group === 'workspace' && !s.externalTo);
  return workspace ? workspace.path : 'profile';
}
