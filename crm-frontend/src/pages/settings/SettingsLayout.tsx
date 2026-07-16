import { NavLink, Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '../../lib/auth';
import { DocumentTitle } from '../../lib/useDocumentTitle';
import { Skeleton } from '@/components/ui';
import {
  Shield, Users, UsersRound, KeyRound, Boxes, Table2, EyeOff,
  Target, Mail, Brain, ScrollText, MessageSquare, UserRound, Building2, Bell, type LucideIcon,
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
        <Skeleton key={i} className="h-10 rounded-lg" />
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
            `flex items-center gap-2 px-3 py-2 rounded-lg text-sm whitespace-nowrap transition-colors ${
              isActive && !s.externalTo
                ? 'bg-primary/10 text-primary font-medium'
                : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
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

  // One title for all ~14 settings sub-routes (U7.2): the section that owns the
  // current sub-path names the tab. /settings/roles/:id is the exception — it is
  // NOT a SETTINGS_SECTIONS entry, and RoleDetailSection titles the tab with the
  // role's own name. So when the path is nested BELOW a section we render no
  // title at all and let the child own it: this layout is the child's parent, and
  // a parent's effect runs after its children's, so setting one here would
  // overwrite the role name a moment after RoleDetailSection wrote it.
  const pathParts = location.pathname.replace(/^\/settings\/?/, '').split('/').filter(Boolean);
  const [segment, ...nested] = pathParts;
  const activeSection = SETTINGS_SECTIONS.find((s) => s.path === segment && !s.externalTo);
  const documentTitle =
    nested.length > 0 ? null : activeSection ? `${activeSection.label} · Settings` : 'Settings';
  const titleEl = documentTitle ? <DocumentTitle title={documentTitle} /> : null;

  // Until the capability fetch settles, hasCapability is false for EVERYTHING —
  // deciding nav or redirecting now would bounce a deep-linked admin off a page
  // they're allowed on. Render the frame with a skeleton instead.
  if (!permsLoaded) {
    return (
      <div className="mx-auto w-full max-w-6xl">
        {titleEl}
        <div className="mb-6">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Settings</h1>
          <p className="text-muted-foreground mt-1">Your account and workspace configuration.</p>
        </div>
        <div className="space-y-3">
          {[...Array(5)].map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
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
  // `segment` is destructured from pathParts above.
  if (segment && !sections.some((s) => s.path === segment && !s.externalTo)) {
    return <Navigate to={`/settings/${defaultSectionPath(hasCapability)}`} replace />;
  }

  return (
    <div className="mx-auto w-full max-w-6xl">
      {titleEl}
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Settings</h1>
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
