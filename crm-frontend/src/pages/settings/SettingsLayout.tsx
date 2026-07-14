import { NavLink, Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '../../lib/auth';
import {
  Shield, Users, UsersRound, KeyRound, Boxes, Table2, EyeOff,
  Target, Mail, Brain, ScrollText, MessageSquare, UserRound, Building2, type LucideIcon,
} from 'lucide-react';

// The unified settings shell (U1): ONE routed area, grouped into Personal vs
// Workspace, every section URL-addressable and capability-gated. A section a
// member can't use doesn't render at all (the server still enforces every
// action); the email-templates entry links out to the existing A5 library.

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

  // Workspace — admin/config surfaces, each behind the capability its API checks.
  { path: 'general', label: 'General', icon: Building2, group: 'workspace', visible: (can) => can('org.settings') },
  { path: 'members', label: 'Members', icon: Users, group: 'workspace', visible: (can) => can('members.manage') || can('members.invite') },
  { path: 'groups', label: 'User Groups', icon: UsersRound, group: 'workspace', visible: (can) => can('groups.manage') },
  { path: 'roles', label: 'Roles', icon: KeyRound, group: 'workspace', visible: (can) => can('roles.manage') },
  { path: 'object-access', label: 'Object Access', icon: Table2, group: 'workspace', visible: (can) => can('roles.manage') },
  { path: 'field-access', label: 'Field Access', icon: EyeOff, group: 'workspace', visible: (can) => can('roles.manage') },
  { path: 'objects', label: 'Objects & Fields', icon: Boxes, group: 'workspace', visible: (can) => can('objects.manage') },
  { path: 'pipeline', label: 'Pipeline', icon: Target, group: 'workspace', visible: (can) => can('pipeline.manage') },
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

// SettingsIndexRedirect sends a bare /settings to the member's default section
// — once capabilities have loaded, so an admin isn't misrouted to Security.
export function SettingsIndexRedirect() {
  const { hasCapability, permsLoaded } = useAuth();
  if (!permsLoaded) return <SettingsSkeleton />;
  return <Navigate to={`/settings/${defaultSectionPath(hasCapability)}`} replace />;
}

function SettingsSkeleton() {
  return (
    <div className="space-y-3 py-4">
      {[...Array(4)].map((_, i) => (
        <div key={i} className="h-10 rounded-lg bg-muted/50 animate-pulse" />
      ))}
    </div>
  );
}

function NavGroup({ title, sections }: { title: string; sections: SettingsSection[] }) {
  if (sections.length === 0) return null;
  return (
    <div>
      <p className="px-3 pb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</p>
      <div className="flex md:flex-col gap-1">
        {sections.map((s) => {
          const Icon = s.icon;
          const cls = ({ isActive }: { isActive: boolean }) =>
            `flex items-center gap-2 px-3 py-2 rounded-md text-sm whitespace-nowrap transition-colors ${
              isActive && !s.externalTo
                ? 'bg-accent text-accent-foreground font-medium'
                : 'text-muted-foreground hover:bg-accent/60 hover:text-foreground'
            }`;
          // No `end`: nested pages (e.g. /settings/roles/:id) keep their parent
          // section highlighted. Section paths never prefix each other.
          return (
            <NavLink key={s.path} to={s.externalTo ?? `/settings/${s.path}`} className={cls}>
              <Icon className="w-4 h-4 shrink-0" />
              {s.label}
            </NavLink>
          );
        })}
      </div>
    </div>
  );
}

export default function SettingsLayout() {
  const { hasCapability, permsLoaded } = useAuth();
  const location = useLocation();

  // Until the capability fetch settles, hasCapability is false for EVERYTHING —
  // deciding nav or redirecting now would bounce a deep-linked admin off a page
  // they're allowed on. Render the frame with a skeleton instead.
  if (!permsLoaded) {
    return (
      <div className="max-w-6xl mx-auto">
        <div className="mb-6">
          <h1 className="text-2xl font-bold tracking-tight">Settings</h1>
          <p className="text-muted-foreground mt-1">Your account and workspace configuration.</p>
        </div>
        <div className="space-y-3">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="h-10 rounded-lg bg-muted/50 animate-pulse" />
          ))}
        </div>
      </div>
    );
  }

  const sections = visibleSections(hasCapability);
  const personal = sections.filter((s) => s.group === 'personal');
  const workspace = sections.filter((s) => s.group === 'workspace');

  // Route guard: a deep link to a section the member can't see redirects to
  // their default section (the server enforces the real gates regardless).
  const segment = location.pathname.replace(/^\/settings\/?/, '').split('/')[0];
  if (segment && !sections.some((s) => s.path === segment && !s.externalTo)) {
    return <Navigate to={`/settings/${defaultSectionPath(hasCapability)}`} replace />;
  }

  return (
    <div className="max-w-6xl mx-auto">
      <div className="mb-6">
        <h1 className="text-2xl font-bold tracking-tight">Settings</h1>
        <p className="text-muted-foreground mt-1">Your account and workspace configuration.</p>
      </div>

      <div className="flex flex-col md:flex-row gap-6 md:gap-8">
        {/* Grouped nav: vertical rail on desktop, scrollable rows on mobile. */}
        <nav aria-label="Settings sections" className="md:w-52 shrink-0 space-y-4 overflow-x-auto md:overflow-visible pb-1 md:pb-0">
          <NavGroup title="My settings" sections={personal} />
          <NavGroup title="Workspace" sections={workspace} />
        </nav>

        <div className="flex-1 min-w-0">
          <Outlet />
        </div>
      </div>
    </div>
  );
}
